package data

import (
	"context"
	"errors"
	"fmt"

	kerrors "github.com/go-kratos/kratos/v3/errors"

	inventoryv1 "github.com/franbenaglia/go-microservices-lab/api/gen/go/inventory/v1"
	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/biz"
)

// InventoryClient adapta el cliente gRPC generado al puerto biz.Inventory y
// traduce los errores de transporte a errores de dominio.
type InventoryClient struct {
	client inventoryv1.InventoryServiceClient
}

var _ biz.Inventory = (*InventoryClient)(nil)

// NewInventoryClient construye el adaptador sobre un cliente gRPC ya conectado.
func NewInventoryClient(c inventoryv1.InventoryServiceClient) *InventoryClient {
	return &InventoryClient{client: c}
}

// GetProduct obtiene los datos de catálogo de un producto.
func (c *InventoryClient) GetProduct(ctx context.Context, id string) (biz.ProductInfo, error) {
	p, err := c.client.GetProduct(ctx, &inventoryv1.GetProductRequest{Id: id})
	if err != nil {
		return biz.ProductInfo{}, mapError(err)
	}
	return biz.ProductInfo{ID: p.GetId(), SKU: p.GetSku(), Name: p.GetName(), PriceCents: p.GetPriceCents()}, nil
}

// CheckAvailability indica si hay stock para todas las líneas.
func (c *InventoryClient) CheckAvailability(ctx context.Context, lines []biz.Line) (bool, error) {
	resp, err := c.client.CheckAvailability(ctx, &inventoryv1.CheckAvailabilityRequest{Items: toStockLines(lines)})
	if err != nil {
		return false, mapError(err)
	}
	return resp.GetAllAvailable(), nil
}

// Reserve reserva stock con una clave de idempotencia.
func (c *InventoryClient) Reserve(ctx context.Context, key string, lines []biz.Line) (string, error) {
	resp, err := c.client.ReserveStock(ctx, &inventoryv1.ReserveStockRequest{IdempotencyKey: key, Items: toStockLines(lines)})
	if err != nil {
		return "", mapError(err)
	}
	return resp.GetReservationId(), nil
}

// ReleaseByID libera una reserva conocida.
func (c *InventoryClient) ReleaseByID(ctx context.Context, reservationID string) error {
	_, err := c.client.ReleaseStock(ctx, &inventoryv1.ReleaseStockRequest{
		Target: &inventoryv1.ReleaseStockRequest_ReservationId{ReservationId: reservationID},
	})
	return mapError(err)
}

// ReleaseByKey libera (o anula de antemano) la reserva asociada a una clave.
func (c *InventoryClient) ReleaseByKey(ctx context.Context, key string) error {
	_, err := c.client.ReleaseStock(ctx, &inventoryv1.ReleaseStockRequest{
		Target: &inventoryv1.ReleaseStockRequest_IdempotencyKey{IdempotencyKey: key},
	})
	return mapError(err)
}

func toStockLines(lines []biz.Line) []*inventoryv1.StockLine {
	out := make([]*inventoryv1.StockLine, len(lines))
	for i, l := range lines {
		out[i] = &inventoryv1.StockLine{ProductId: l.ProductID, Quantity: l.Quantity}
	}
	return out
}

// mapError convierte un error gRPC/Kratos en un error de dominio. Los errores de
// negocio conocidos se conservan; cualquier otro (Unavailable, DeadlineExceeded,
// circuito abierto, sin instancias...) se considera indisponibilidad.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case inventoryv1.IsProductNotFound(err):
		return fmt.Errorf("%w: %s", biz.ErrProductNotFound, kerrors.FromError(err).Message)
	case inventoryv1.IsInsufficientStock(err):
		return fmt.Errorf("%w: %s", biz.ErrInsufficientStock, kerrors.FromError(err).Message)
	case inventoryv1.IsInvalidArgument(err):
		return fmt.Errorf("%w: %s", biz.ErrInvalidArgument, kerrors.FromError(err).Message)
	case errors.Is(err, context.Canceled):
		return err
	}
	// No se propaga el mensaje original (puede contener direcciones internas);
	// el detalle completo lo registra el middleware logging.Client.
	se := kerrors.FromError(err)
	return fmt.Errorf("%w (code=%d reason=%s)", biz.ErrInventoryUnavailable, se.Code, se.Reason)
}
