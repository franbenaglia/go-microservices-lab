// Package data implementa los puertos de biz: persistencia en PostgreSQL y
// cliente gRPC de Inventory.
package data

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/biz"
)

// OrderRepo implementa biz.OrderRepo.
type OrderRepo struct {
	pool *pgxpool.Pool
}

var _ biz.OrderRepo = (*OrderRepo)(nil)

// NewOrderRepo construye el repositorio.
func NewOrderRepo(pool *pgxpool.Pool) *OrderRepo { return &OrderRepo{pool: pool} }

const orderColumns = `id::text, status, total_cents, COALESCE(reservation_id::text, ''), rejection_code,
	rejection_reason, created_at, updated_at`

// Insert guarda un pedido nuevo con sus líneas.
func (r *OrderRepo) Insert(ctx context.Context, o *biz.Order) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO orders (id, status, total_cents) VALUES ($1, $2, $3)
			RETURNING created_at, updated_at`, o.ID, o.Status, o.TotalCents).
			Scan(&o.CreatedAt, &o.UpdatedAt); err != nil {
			return err
		}
		return insertItems(ctx, tx, o)
	})
}

func insertItems(ctx context.Context, tx pgx.Tx, o *biz.Order) error {
	for _, it := range o.Items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_items (order_id, product_id, sku, name, quantity, unit_price_cents)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			o.ID, it.ProductID, it.SKU, it.Name, it.Quantity, it.UnitPriceCents); err != nil {
			return err
		}
	}
	return nil
}

// Finalize guarda el resultado del procesamiento solo si sigue en PENDING.
func (r *OrderRepo) Finalize(ctx context.Context, o *biz.Order) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var resID any
		if o.ReservationID != "" {
			resID = o.ReservationID
		}
		err := tx.QueryRow(ctx, `
			UPDATE orders SET status = $2, total_cents = $3, reservation_id = $4,
				rejection_code = $5, rejection_reason = $6, updated_at = now()
			WHERE id = $1 AND status = 'PENDING'
			RETURNING updated_at`,
			o.ID, o.Status, o.TotalCents, resID, o.RejectionCode, o.RejectionReason).Scan(&o.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return biz.ErrStatusChanged
		}
		if err != nil {
			return err
		}
		for _, it := range o.Items {
			if _, err := tx.Exec(ctx, `
				UPDATE order_items SET sku = $3, name = $4, unit_price_cents = $5
				WHERE order_id = $1 AND product_id = $2`,
				o.ID, it.ProductID, it.SKU, it.Name, it.UnitPriceCents); err != nil {
				return err
			}
		}
		return nil
	})
}

// UpdateStatus cambia el estado de forma condicional (control optimista).
func (r *OrderRepo) UpdateStatus(ctx context.Context, id string, from, to biz.Status) (*biz.Order, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE orders SET status = $3, updated_at = now() WHERE id = $1 AND status = $2`, id, from, to)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, biz.ErrStatusChanged
	}
	return r.Get(ctx, id)
}

// Get devuelve un pedido con sus líneas.
func (r *OrderRepo) Get(ctx context.Context, id string) (*biz.Order, error) {
	o, err := scanOrder(r.pool.QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	if err := r.loadItems(ctx, []*biz.Order{o}); err != nil {
		return nil, err
	}
	return o, nil
}

// List devuelve pedidos recientes, opcionalmente filtrados por estado.
func (r *OrderRepo) List(ctx context.Context, status biz.Status, limit, offset int) ([]*biz.Order, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+orderColumns+` FROM orders
		WHERE ($1 = '' OR status = $1)
		ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3`, string(status), limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*biz.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := r.loadItems(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// loadItems carga las líneas de varios pedidos con una sola consulta.
func (r *OrderRepo) loadItems(ctx context.Context, orders []*biz.Order) error {
	if len(orders) == 0 {
		return nil
	}
	byID := make(map[string]*biz.Order, len(orders))
	ids := make([]string, len(orders))
	for i, o := range orders {
		byID[o.ID], ids[i] = o, o.ID
	}
	rows, err := r.pool.Query(ctx, `
		SELECT order_id::text, product_id::text, sku, name, quantity, unit_price_cents
		FROM order_items WHERE order_id = ANY($1::uuid[]) ORDER BY order_id, product_id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			orderID string
			it      biz.Item
		)
		if err := rows.Scan(&orderID, &it.ProductID, &it.SKU, &it.Name, &it.Quantity, &it.UnitPriceCents); err != nil {
			return err
		}
		if o, ok := byID[orderID]; ok {
			o.Items = append(o.Items, it)
		}
	}
	return rows.Err()
}

func scanOrder(row pgx.Row) (*biz.Order, error) {
	var (
		o      biz.Order
		status string
	)
	if err := row.Scan(&o.ID, &status, &o.TotalCents, &o.ReservationID, &o.RejectionCode,
		&o.RejectionReason, &o.CreatedAt, &o.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, biz.ErrOrderNotFound
		}
		return nil, fmt.Errorf("leer pedido: %w", err)
	}
	o.Status = biz.Status(status)
	return &o, nil
}
