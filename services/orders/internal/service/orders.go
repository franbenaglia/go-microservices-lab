// Package service adapta la API generada de Orders a los casos de uso de biz.
package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/types/known/timestamppb"

	ordersv1 "github.com/franbenaglia/go-microservices-lab/api/gen/go/orders/v1"
	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/biz"
)

// OrdersService implementa las interfaces gRPC y HTTP de Orders.
type OrdersService struct {
	ordersv1.UnimplementedOrdersServiceServer

	uc     *biz.OrderUsecase
	logger *slog.Logger
	orders metric.Int64Counter
}

var (
	_ ordersv1.OrdersServiceServer     = (*OrdersService)(nil)
	_ ordersv1.OrdersServiceHTTPServer = (*OrdersService)(nil)
)

// NewOrdersService construye el servicio. m publica
// orders_processed_total{status, rejection_code} (nunca el ID del pedido como etiqueta).
func NewOrdersService(uc *biz.OrderUsecase, logger *slog.Logger, m metric.Meter) (*OrdersService, error) {
	c, err := m.Int64Counter("orders_processed_total",
		metric.WithDescription("Pedidos creados o cancelados por estado resultante y código de rechazo"))
	if err != nil {
		return nil, err
	}
	return &OrdersService{uc: uc, logger: logger, orders: c}, nil
}

func (s *OrdersService) count(ctx context.Context, o *biz.Order) {
	s.orders.Add(ctx, 1, metric.WithAttributes(
		attribute.String("status", string(o.Status)),
		attribute.String("rejection_code", o.RejectionCode)))
}

func (s *OrdersService) CreateOrder(ctx context.Context, req *ordersv1.CreateOrderRequest) (*ordersv1.Order, error) {
	lines := make([]biz.Line, len(req.GetItems()))
	for i, it := range req.GetItems() {
		lines[i] = biz.Line{ProductID: it.GetProductId(), Quantity: it.GetQuantity()}
	}
	o, err := s.uc.CreateOrder(ctx, lines)
	if err != nil {
		return nil, s.toAPIError(ctx, "CreateOrder", err)
	}
	s.count(ctx, o)
	if o.Status == biz.StatusRejected {
		s.logger.WarnContext(ctx, "pedido rechazado",
			slog.String("operation", "CreateOrder"),
			slog.String("order_id", o.ID),
			slog.String("rejection_code", o.RejectionCode),
			slog.String("reason", o.RejectionReason))
	}
	return toProto(o), nil
}

func (s *OrdersService) GetOrder(ctx context.Context, req *ordersv1.GetOrderRequest) (*ordersv1.Order, error) {
	o, err := s.uc.GetOrder(ctx, req.GetId())
	if err != nil {
		return nil, s.toAPIError(ctx, "GetOrder", err)
	}
	return toProto(o), nil
}

func (s *OrdersService) GetOrderStatus(ctx context.Context, req *ordersv1.GetOrderRequest) (*ordersv1.OrderStatusResponse, error) {
	o, err := s.uc.GetOrder(ctx, req.GetId())
	if err != nil {
		return nil, s.toAPIError(ctx, "GetOrderStatus", err)
	}
	return &ordersv1.OrderStatusResponse{
		Id: o.ID, Status: statusToProto(o.Status),
		RejectionCode: o.RejectionCode, RejectionReason: o.RejectionReason,
	}, nil
}

func (s *OrdersService) ListOrders(ctx context.Context, req *ordersv1.ListOrdersRequest) (*ordersv1.ListOrdersResponse, error) {
	var status biz.Status
	if req.GetStatus() != ordersv1.OrderStatus_ORDER_STATUS_UNSPECIFIED {
		status = statusFromProto(req.GetStatus())
	}
	os, err := s.uc.ListOrders(ctx, status, int(req.GetLimit()), int(req.GetOffset()))
	if err != nil {
		return nil, s.toAPIError(ctx, "ListOrders", err)
	}
	out := &ordersv1.ListOrdersResponse{Orders: make([]*ordersv1.Order, len(os))}
	for i, o := range os {
		out.Orders[i] = toProto(o)
	}
	return out, nil
}

func (s *OrdersService) CancelOrder(ctx context.Context, req *ordersv1.CancelOrderRequest) (*ordersv1.Order, error) {
	o, err := s.uc.CancelOrder(ctx, req.GetId())
	if err != nil {
		return nil, s.toAPIError(ctx, "CancelOrder", err)
	}
	s.count(ctx, o)
	return toProto(o), nil
}

func (s *OrdersService) toAPIError(ctx context.Context, op string, err error) error {
	msg := err.Error()
	switch {
	case errors.Is(err, biz.ErrInvalidArgument):
		return ordersv1.ErrorInvalidArgument("%s", msg)
	case errors.Is(err, biz.ErrOrderNotFound):
		return ordersv1.ErrorOrderNotFound("%s", msg)
	case errors.Is(err, biz.ErrInvalidTransition):
		return ordersv1.ErrorInvalidTransition("%s", msg)
	case errors.Is(err, biz.ErrInventoryUnavailable):
		return ordersv1.ErrorInventoryUnavailable("%s", msg)
	case errors.Is(err, context.DeadlineExceeded):
		return kerrors.GatewayTimeout("DEADLINE_EXCEEDED", "tiempo de espera agotado")
	case errors.Is(err, context.Canceled):
		return kerrors.ClientClosed("CANCELED", "solicitud cancelada")
	}
	s.logger.ErrorContext(ctx, "error interno", slog.String("operation", op), slog.Any("error", err))
	return kerrors.InternalServer("INTERNAL", "error interno")
}

func toProto(o *biz.Order) *ordersv1.Order {
	out := &ordersv1.Order{
		Id:              o.ID,
		Status:          statusToProto(o.Status),
		Items:           make([]*ordersv1.OrderItem, len(o.Items)),
		TotalCents:      o.TotalCents,
		RejectionCode:   o.RejectionCode,
		RejectionReason: o.RejectionReason,
		ReservationId:   o.ReservationID,
		CreatedAt:       timestamppb.New(o.CreatedAt),
		UpdatedAt:       timestamppb.New(o.UpdatedAt),
	}
	for i, it := range o.Items {
		out.Items[i] = &ordersv1.OrderItem{
			ProductId: it.ProductID, Sku: it.SKU, Name: it.Name, Quantity: it.Quantity,
			UnitPriceCents: it.UnitPriceCents, SubtotalCents: it.SubtotalCents(),
		}
	}
	return out
}

func statusToProto(s biz.Status) ordersv1.OrderStatus {
	if v, ok := ordersv1.OrderStatus_value["ORDER_STATUS_"+string(s)]; ok {
		return ordersv1.OrderStatus(v)
	}
	return ordersv1.OrderStatus_ORDER_STATUS_UNSPECIFIED
}

func statusFromProto(s ordersv1.OrderStatus) biz.Status {
	return biz.Status(strings.TrimPrefix(s.String(), "ORDER_STATUS_"))
}
