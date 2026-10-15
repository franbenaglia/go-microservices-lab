// Package data implementa los puertos de biz sobre PostgreSQL (pgx).
package data

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/franbenaglia/go-microservices-lab/services/inventory/internal/biz"
)

const (
	pgUniqueViolation = "23505"
	pgCheckViolation  = "23514"
)

const productColumns = `id::text, sku, name, description, price_cents, available, reserved, created_at, updated_at`

// ProductRepo implementa biz.ProductRepo.
type ProductRepo struct {
	pool *pgxpool.Pool
}

var _ biz.ProductRepo = (*ProductRepo)(nil)

// NewProductRepo construye el repositorio.
func NewProductRepo(pool *pgxpool.Pool) *ProductRepo {
	return &ProductRepo{pool: pool}
}

func scanProduct(row pgx.Row) (*biz.Product, error) {
	var p biz.Product
	if err := row.Scan(&p.ID, &p.SKU, &p.Name, &p.Description, &p.PriceCents,
		&p.Available, &p.Reserved, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, biz.ErrProductNotFound
		}
		return nil, err
	}
	return &p, nil
}

// Create inserta un producto nuevo.
func (r *ProductRepo) Create(ctx context.Context, p *biz.Product) (*biz.Product, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	row := r.pool.QueryRow(ctx, `
		INSERT INTO products (id, sku, name, description, price_cents, available)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+productColumns,
		id, p.SKU, p.Name, p.Description, p.PriceCents, p.Available)
	out, err := scanProduct(row)
	if isPgError(err, pgUniqueViolation) {
		return nil, fmt.Errorf("%w: %s", biz.ErrSKUAlreadyExists, p.SKU)
	}
	return out, err
}

// Update modifica nombre, descripción y precio.
func (r *ProductRepo) Update(ctx context.Context, id, name, description string, priceCents int64) (*biz.Product, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE products SET name = $2, description = $3, price_cents = $4, updated_at = now()
		WHERE id = $1
		RETURNING `+productColumns,
		id, name, description, priceCents)
	return scanProduct(row)
}

// Get devuelve un producto por ID.
func (r *ProductRepo) Get(ctx context.Context, id string) (*biz.Product, error) {
	return scanProduct(r.pool.QueryRow(ctx, `SELECT `+productColumns+` FROM products WHERE id = $1`, id))
}

// List devuelve productos ordenados por SKU.
func (r *ProductRepo) List(ctx context.Context, limit, offset int) ([]*biz.Product, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+productColumns+` FROM products ORDER BY sku LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*biz.Product
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetMany devuelve los productos solicitados indexados por ID.
func (r *ProductRepo) GetMany(ctx context.Context, ids []string) (map[string]*biz.Product, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+productColumns+` FROM products WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]*biz.Product, len(ids))
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		out[p.ID] = p
	}
	return out, rows.Err()
}

// AdjustStock suma delta al disponible con una actualización condicional: la
// condición WHERE impide que el resultado sea negativo.
func (r *ProductRepo) AdjustStock(ctx context.Context, id string, delta int32) (*biz.Product, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE products SET available = available + $2, updated_at = now()
		WHERE id = $1 AND available + $2 >= 0
		RETURNING `+productColumns, id, delta)
	p, err := scanProduct(row)
	if errors.Is(err, biz.ErrProductNotFound) {
		// 0 filas: o no existe o el stock quedaría negativo.
		exists, exErr := r.exists(ctx, r.pool, id)
		if exErr != nil {
			return nil, exErr
		}
		if exists {
			return nil, fmt.Errorf("%w: producto %s", biz.ErrInsufficientStock, id)
		}
	}
	if isPgError(err, pgCheckViolation) {
		return nil, fmt.Errorf("%w: producto %s", biz.ErrInsufficientStock, id)
	}
	return p, err
}

// Reserve implementa la reserva atómica e idempotente.
//
// Concurrencia: cada línea se descuenta con
//
//	UPDATE ... SET available = available - q WHERE id = $1 AND available >= q
//
// PostgreSQL bloquea la fila y, en READ COMMITTED, vuelve a evaluar la
// condición con el valor confirmado más reciente, así que dos reservas
// simultáneas nunca pueden dejar el stock negativo. Las líneas llegan
// ordenadas por ID para evitar interbloqueos entre reservas de varios productos.
//
// Idempotencia: la inserción en reservations usa ON CONFLICT
// (idempotency_key) DO NOTHING. Una segunda petición con la misma clave espera
// a que la primera confirme y después lee la reserva existente.
func (r *ProductRepo) Reserve(ctx context.Context, key string, lines []biz.StockLine) (*biz.Reservation, bool, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, false, err
	}
	var (
		res      *biz.Reservation
		replayed bool
	)
	err = pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO reservations (id, idempotency_key, status) VALUES ($1, $2, 'ACTIVE')
			ON CONFLICT (idempotency_key) DO NOTHING`, id, key)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			existing, err := loadReservation(ctx, tx, `idempotency_key = $1`, key, false)
			if err != nil {
				return err
			}
			// Una reserva liberada sin líneas es una "lápida": se liberó antes de
			// que llegara la reserva (ver Release). No se reserva nada.
			if !existing.Released || len(existing.Lines) > 0 {
				if !biz.SameLines(existing.Lines, lines) {
					return biz.ErrIdempotencyKeyConflict
				}
			}
			res, replayed = existing, true
			return nil
		}
		for _, l := range lines {
			tag, err := tx.Exec(ctx, `
				UPDATE products SET available = available - $2, reserved = reserved + $2, updated_at = now()
				WHERE id = $1 AND available >= $2`, l.ProductID, l.Quantity)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				exists, err := r.exists(ctx, tx, l.ProductID)
				if err != nil {
					return err
				}
				if !exists {
					return fmt.Errorf("%w: %s", biz.ErrProductNotFound, l.ProductID)
				}
				return fmt.Errorf("%w: producto %s", biz.ErrInsufficientStock, l.ProductID)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO reservation_items (reservation_id, product_id, quantity) VALUES ($1, $2, $3)`,
				id, l.ProductID, l.Quantity); err != nil {
				return err
			}
		}
		res = &biz.Reservation{ID: id.String(), IdempotencyKey: key, Lines: lines}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return res, replayed, nil
}

// Release devuelve al disponible las unidades de una reserva activa.
//
// Si se libera por clave y la reserva todavía no existe (p. ej. la reserva
// original sigue en vuelo tras un timeout del cliente), se crea una lápida
// RELEASED con esa clave. Así una reserva que llegue tarde con la misma clave
// no reservará stock que nadie liberaría después.
func (r *ProductRepo) Release(ctx context.Context, target biz.ReleaseTarget) (*biz.Reservation, bool, error) {
	var (
		res             *biz.Reservation
		alreadyReleased bool
	)
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var (
			existing *biz.Reservation
			err      error
		)
		if target.ReservationID != "" {
			existing, err = loadReservation(ctx, tx, `id = $1`, target.ReservationID, true)
		} else {
			existing, err = loadReservation(ctx, tx, `idempotency_key = $1`, target.IdempotencyKey, true)
			if errors.Is(err, biz.ErrReservationNotFound) {
				tombID, idErr := uuid.NewV7()
				if idErr != nil {
					return idErr
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO reservations (id, idempotency_key, status, released_at)
					VALUES ($1, $2, 'RELEASED', now())
					ON CONFLICT (idempotency_key) DO NOTHING`, tombID, target.IdempotencyKey); err != nil {
					return err
				}
				existing, err = loadReservation(ctx, tx, `idempotency_key = $1`, target.IdempotencyKey, true)
			}
		}
		if err != nil {
			return err
		}
		if existing.Released {
			res, alreadyReleased = existing, true
			return nil
		}
		for _, l := range existing.Lines {
			if _, err := tx.Exec(ctx, `
				UPDATE products SET available = available + $2, reserved = reserved - $2, updated_at = now()
				WHERE id = $1`, l.ProductID, l.Quantity); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE reservations SET status = 'RELEASED', released_at = now() WHERE id = $1`, existing.ID); err != nil {
			return err
		}
		existing.Released = true
		res = existing
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return res, alreadyReleased, nil
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (r *ProductRepo) exists(ctx context.Context, q querier, id string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM products WHERE id = $1)`, id).Scan(&ok)
	return ok, err
}

// loadReservation lee una reserva y sus líneas; con forUpdate bloquea la fila.
func loadReservation(ctx context.Context, q querier, where string, arg any, forUpdate bool) (*biz.Reservation, error) {
	sql := `SELECT id::text, idempotency_key, status FROM reservations WHERE ` + where
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	var (
		res    biz.Reservation
		status string
	)
	if err := q.QueryRow(ctx, sql, arg).Scan(&res.ID, &res.IdempotencyKey, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, biz.ErrReservationNotFound
		}
		return nil, err
	}
	res.Released = status == "RELEASED"
	rows, err := q.Query(ctx,
		`SELECT product_id::text, quantity FROM reservation_items WHERE reservation_id = $1`, res.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l biz.StockLine
		if err := rows.Scan(&l.ProductID, &l.Quantity); err != nil {
			return nil, err
		}
		res.Lines = append(res.Lines, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(res.Lines, func(a, b biz.StockLine) int { return strings.Compare(a.ProductID, b.ProductID) })
	return &res, nil
}

func isPgError(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
