// Package service adapta la API generada (gRPC y HTTP) a los casos de uso de biz.
package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/timestamppb"

	inventoryv1 "github.com/franbenaglia/go-microservices-lab/api/gen/go/inventory/v1"
	"github.com/franbenaglia/go-microservices-lab/pkg/platform"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/internal/biz"
)

// InventoryService implementa inventoryv1.InventoryServiceServer (gRPC) e
// inventoryv1.InventoryServiceHTTPServer (HTTP) con la misma lógica.
type InventoryService struct {
	inventoryv1.UnimplementedInventoryServiceServer

	uc     *biz.InventoryUsecase
	id     platform.Identity
	logger *slog.Logger
}

var (
	_ inventoryv1.InventoryServiceServer     = (*InventoryService)(nil)
	_ inventoryv1.InventoryServiceHTTPServer = (*InventoryService)(nil)
)

// NewInventoryService construye el servicio.
func NewInventoryService(uc *biz.InventoryUsecase, id platform.Identity, logger *slog.Logger) *InventoryService {
	return &InventoryService{uc: uc, id: id, logger: logger}
}

func (s *InventoryService) CreateProduct(ctx context.Context, req *inventoryv1.CreateProductRequest) (*inventoryv1.Product, error) {
	p, err := s.uc.CreateProduct(ctx, biz.NewProductInput{
		SKU:          req.GetSku(),
		Name:         req.GetName(),
		Description:  req.GetDescription(),
		PriceCents:   req.GetPriceCents(),
		InitialStock: req.GetInitialStock(),
	})
	if err != nil {
		return nil, s.toAPIError(ctx, "CreateProduct", err)
	}
	return toProto(p), nil
}

func (s *InventoryService) UpdateProduct(ctx context.Context, req *inventoryv1.UpdateProductRequest) (*inventoryv1.Product, error) {
	p, err := s.uc.UpdateProduct(ctx, req.GetId(), req.GetName(), req.GetDescription(), req.GetPriceCents())
	if err != nil {
		return nil, s.toAPIError(ctx, "UpdateProduct", err)
	}
	return toProto(p), nil
}

func (s *InventoryService) ListProducts(ctx context.Context, req *inventoryv1.ListProductsRequest) (*inventoryv1.ListProductsResponse, error) {
	ps, err := s.uc.ListProducts(ctx, int(req.GetLimit()), int(req.GetOffset()))
	if err != nil {
		return nil, s.toAPIError(ctx, "ListProducts", err)
	}
	out := &inventoryv1.ListProductsResponse{Products: make([]*inventoryv1.Product, len(ps))}
	for i, p := range ps {
		out.Products[i] = toProto(p)
	}
	return out, nil
}

func (s *InventoryService) GetProduct(ctx context.Context, req *inventoryv1.GetProductRequest) (*inventoryv1.Product, error) {
	p, err := s.uc.GetProduct(ctx, req.GetId())
	if err != nil {
		return nil, s.toAPIError(ctx, "GetProduct", err)
	}
	return toProto(p), nil
}

func (s *InventoryService) AdjustStock(ctx context.Context, req *inventoryv1.AdjustStockRequest) (*inventoryv1.Product, error) {
	p, err := s.uc.AdjustStock(ctx, req.GetProductId(), req.GetDelta())
	if err != nil {
		return nil, s.toAPIError(ctx, "AdjustStock", err)
	}
	return toProto(p), nil
}

func (s *InventoryService) CheckAvailability(ctx context.Context, req *inventoryv1.CheckAvailabilityRequest) (*inventoryv1.CheckAvailabilityResponse, error) {
	all, lines, err := s.uc.CheckAvailability(ctx, fromProtoLines(req.GetItems()))
	if err != nil {
		return nil, s.toAPIError(ctx, "CheckAvailability", err)
	}
	out := &inventoryv1.CheckAvailabilityResponse{AllAvailable: all, Items: make([]*inventoryv1.AvailabilityLine, len(lines))}
	for i, l := range lines {
		out.Items[i] = &inventoryv1.AvailabilityLine{
			ProductId: l.ProductID, Requested: l.Requested, Available: l.Available, Sufficient: l.Sufficient,
		}
	}
	return out, nil
}

func (s *InventoryService) ReserveStock(ctx context.Context, req *inventoryv1.ReserveStockRequest) (*inventoryv1.ReserveStockResponse, error) {
	res, replayed, err := s.uc.ReserveStock(ctx, req.GetIdempotencyKey(), fromProtoLines(req.GetItems()))
	if err != nil {
		return nil, s.toAPIError(ctx, "ReserveStock", err)
	}
	return &inventoryv1.ReserveStockResponse{ReservationId: res.ID, Replayed: replayed}, nil
}

func (s *InventoryService) ReleaseStock(ctx context.Context, req *inventoryv1.ReleaseStockRequest) (*inventoryv1.ReleaseStockResponse, error) {
	res, already, err := s.uc.ReleaseStock(ctx, biz.ReleaseTarget{
		ReservationID:  req.GetReservationId(),
		IdempotencyKey: req.GetIdempotencyKey(),
	})
	if err != nil {
		return nil, s.toAPIError(ctx, "ReleaseStock", err)
	}
	return &inventoryv1.ReleaseStockResponse{ReservationId: res.ID, AlreadyReleased: already}, nil
}

func (s *InventoryService) Diagnose(_ context.Context, _ *inventoryv1.DiagnoseRequest) (*inventoryv1.DiagnoseResponse, error) {
	return &inventoryv1.DiagnoseResponse{
		Service:    s.id.Service,
		InstanceId: s.id.Instance,
		Hostname:   platform.Hostname(),
		Version:    s.id.Version,
		Time:       timestamppb.New(time.Now()),
	}, nil
}

// toAPIError traduce errores de dominio a errores de la API. Los errores
// inesperados se registran y se devuelven sin detalles internos.
func (s *InventoryService) toAPIError(ctx context.Context, op string, err error) error {
	msg := err.Error()
	switch {
	case errors.Is(err, biz.ErrInvalidArgument):
		return inventoryv1.ErrorInvalidArgument("%s", msg)
	case errors.Is(err, biz.ErrProductNotFound):
		return inventoryv1.ErrorProductNotFound("%s", msg)
	case errors.Is(err, biz.ErrSKUAlreadyExists):
		return inventoryv1.ErrorSkuAlreadyExists("%s", msg)
	case errors.Is(err, biz.ErrInsufficientStock):
		return inventoryv1.ErrorInsufficientStock("%s", msg)
	case errors.Is(err, biz.ErrReservationNotFound):
		return inventoryv1.ErrorReservationNotFound("%s", msg)
	case errors.Is(err, biz.ErrIdempotencyKeyConflict):
		return inventoryv1.ErrorIdempotencyKeyConflict("%s", msg)
	case errors.Is(err, context.DeadlineExceeded):
		return kerrors.GatewayTimeout("DEADLINE_EXCEEDED", "tiempo de espera agotado")
	case errors.Is(err, context.Canceled):
		return kerrors.ClientClosed("CANCELED", "solicitud cancelada")
	}
	s.logger.ErrorContext(ctx, "error interno", slog.String("operation", op), slog.Any("error", err))
	return kerrors.InternalServer("INTERNAL", "error interno")
}

func toProto(p *biz.Product) *inventoryv1.Product {
	return &inventoryv1.Product{
		Id:          p.ID,
		Sku:         p.SKU,
		Name:        p.Name,
		Description: p.Description,
		PriceCents:  p.PriceCents,
		Available:   p.Available,
		Reserved:    p.Reserved,
		CreatedAt:   timestamppb.New(p.CreatedAt),
		UpdatedAt:   timestamppb.New(p.UpdatedAt),
	}
}

func fromProtoLines(in []*inventoryv1.StockLine) []biz.StockLine {
	out := make([]biz.StockLine, len(in))
	for i, l := range in {
		out[i] = biz.StockLine{ProductID: l.GetProductId(), Quantity: l.GetQuantity()}
	}
	return out
}
