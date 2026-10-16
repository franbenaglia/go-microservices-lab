// Package biz contiene la lógica de negocio de Orders: validación de pedidos,
// máquina de estados y orquestación con Inventory a través de un puerto.
package biz

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// tracer usa la API de OpenTelemetry (independiente del proveedor): si la
// telemetría no está configurada, los spans no tienen efecto.
var tracer = otel.Tracer("orders-service/biz")

// endSpan marca el resultado del span y lo cierra. Solo registra el tipo de
// error, nunca datos de la petición.
func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, errorType(err))
		span.SetAttributes(attribute.String("error.type", errorType(err)))
	}
	span.End()
}

// errorType clasifica un error de dominio para trazas y métricas.
func errorType(err error) string {
	switch {
	case errors.Is(err, ErrInvalidArgument):
		return "invalid_argument"
	case errors.Is(err, ErrInventoryUnavailable):
		return "inventory_unavailable"
	case errors.Is(err, ErrProductNotFound):
		return "product_not_found"
	case errors.Is(err, ErrInsufficientStock):
		return "insufficient_stock"
	case errors.Is(err, ErrStatusChanged):
		return "status_changed"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	}
	return "internal"
}

// Status es el estado de un pedido.
type Status string

// Estados posibles de un pedido.
const (
	StatusPending   Status = "PENDING"
	StatusConfirmed Status = "CONFIRMED"
	StatusRejected  Status = "REJECTED"
	StatusCancelled Status = "CANCELLED"
)

// Códigos de rechazo estables (los consume el frontend).
const (
	RejectInsufficientStock    = "INSUFFICIENT_STOCK"
	RejectProductNotFound      = "PRODUCT_NOT_FOUND"
	RejectInventoryUnavailable = "INVENTORY_UNAVAILABLE"
	RejectTotalOutOfRange      = "TOTAL_OUT_OF_RANGE"
)

// Límites de validación.
const (
	MaxItemsPerOrder = 50
	MaxQuantityItem  = 1000
	DefaultListLimit = 50
	MaxListLimit     = 500
)

// Errores de dominio.
var (
	ErrInvalidArgument   = errors.New("argumento inválido")
	ErrOrderNotFound     = errors.New("pedido no encontrado")
	ErrInvalidTransition = errors.New("transición de estado no permitida")
	// ErrStatusChanged indica que otro proceso cambió el estado en paralelo.
	ErrStatusChanged = errors.New("el estado del pedido cambió concurrentemente")
)

// Errores que devuelve el puerto de Inventory.
var (
	ErrInventoryUnavailable = errors.New("inventory no disponible")
	ErrProductNotFound      = errors.New("producto no encontrado en inventory")
	ErrInsufficientStock    = errors.New("stock insuficiente")
)

// transitions define la máquina de estados permitida.
var transitions = map[Status][]Status{
	StatusPending:   {StatusConfirmed, StatusRejected, StatusCancelled},
	StatusConfirmed: {StatusCancelled},
}

// CanTransition indica si se permite pasar de from a to.
func CanTransition(from, to Status) bool {
	return slices.Contains(transitions[from], to)
}

// Item es una línea del pedido. Precio, SKU y nombre son una copia tomada de
// Inventory al crear el pedido (el pedido no cambia si después cambia el catálogo).
type Item struct {
	ProductID      string
	SKU            string
	Name           string
	Quantity       int32
	UnitPriceCents int64
}

// SubtotalCents devuelve cantidad × precio unitario.
func (i Item) SubtotalCents() int64 { return int64(i.Quantity) * i.UnitPriceCents }

// Order es un pedido.
type Order struct {
	ID              string
	Status          Status
	Items           []Item
	TotalCents      int64
	ReservationID   string
	RejectionCode   string
	RejectionReason string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ComputeTotal calcula el importe total y detecta desbordamientos.
func ComputeTotal(items []Item) (int64, error) {
	var total int64
	for _, it := range items {
		if it.Quantity < 0 || it.UnitPriceCents < 0 {
			return 0, fmt.Errorf("%w: valores negativos", ErrInvalidArgument)
		}
		if it.UnitPriceCents > 0 && int64(it.Quantity) > math.MaxInt64/it.UnitPriceCents {
			return 0, fmt.Errorf("%w: importe fuera de rango", ErrInvalidArgument)
		}
		sub := it.SubtotalCents()
		if total > math.MaxInt64-sub {
			return 0, fmt.Errorf("%w: importe fuera de rango", ErrInvalidArgument)
		}
		total += sub
	}
	return total, nil
}

// reject marca el pedido como rechazado (solo desde PENDING).
func (o *Order) reject(code, reason string) {
	o.Status = StatusRejected
	o.RejectionCode = code
	o.RejectionReason = reason
}

// ProductInfo son los datos de catálogo que Orders necesita de Inventory.
type ProductInfo struct {
	ID         string
	SKU        string
	Name       string
	PriceCents int64
}

// Line es una cantidad pedida de un producto.
type Line struct {
	ProductID string
	Quantity  int32
}

// Inventory es el puerto hacia Inventory Service. Las implementaciones deben
// traducir los errores de transporte a ErrInventoryUnavailable,
// ErrProductNotFound o ErrInsufficientStock.
type Inventory interface {
	GetProduct(ctx context.Context, id string) (ProductInfo, error)
	CheckAvailability(ctx context.Context, lines []Line) (bool, error)
	Reserve(ctx context.Context, idempotencyKey string, lines []Line) (reservationID string, err error)
	ReleaseByID(ctx context.Context, reservationID string) error
	ReleaseByKey(ctx context.Context, idempotencyKey string) error
}

// OrderRepo es el puerto de persistencia.
type OrderRepo interface {
	Insert(ctx context.Context, o *Order) error
	// Finalize guarda el resultado de la creación (precios, total, estado,
	// reserva) solo si el pedido sigue en PENDING; si no, devuelve ErrStatusChanged.
	Finalize(ctx context.Context, o *Order) error
	// UpdateStatus cambia el estado de from a to de forma condicional.
	UpdateStatus(ctx context.Context, id string, from, to Status) (*Order, error)
	Get(ctx context.Context, id string) (*Order, error)
	List(ctx context.Context, status Status, limit, offset int) ([]*Order, error)
}

// IDGenerator genera identificadores de pedido.
type IDGenerator func() (string, error)

// OrderUsecase orquesta la creación y cancelación de pedidos.
type OrderUsecase struct {
	repo        OrderRepo
	inventory   Inventory
	newID       IDGenerator
	compTimeout time.Duration
}

// NewOrderUsecase construye el caso de uso. compensationTimeout limita la
// liberación compensatoria que se ejecuta cuando una reserva queda incierta.
func NewOrderUsecase(repo OrderRepo, inv Inventory, newID IDGenerator, compensationTimeout time.Duration) *OrderUsecase {
	if compensationTimeout <= 0 {
		compensationTimeout = 2 * time.Second
	}
	return &OrderUsecase{repo: repo, inventory: inv, newID: newID, compTimeout: compensationTimeout}
}

// ReservationKey es la clave de idempotencia de la reserva de un pedido.
func ReservationKey(orderID string) string { return "order:" + orderID }

// CreateOrder valida, persiste el pedido en PENDING y lo confirma o rechaza
// según la respuesta de Inventory. Devuelve un error solo si la petición es
// inválida o falla la persistencia; los rechazos de negocio se reflejan en el
// estado del pedido devuelto.
func (uc *OrderUsecase) CreateOrder(ctx context.Context, lines []Line) (*Order, error) {
	ctx, span := tracer.Start(ctx, "order.create")
	defer span.End()

	_, vspan := tracer.Start(ctx, "order.validate")
	norm, err := NormalizeLines(lines)
	vspan.SetAttributes(attribute.Int("order.lines", len(lines)))
	endSpan(vspan, err)
	if err != nil {
		endSpan(span, err)
		return nil, err
	}
	id, err := uc.newID()
	if err != nil {
		return nil, err
	}
	o := &Order{ID: id, Status: StatusPending, Items: make([]Item, len(norm))}
	for i, l := range norm {
		o.Items[i] = Item{ProductID: l.ProductID, Quantity: l.Quantity}
	}
	span.SetAttributes(attribute.String("order.id", o.ID), attribute.Int("order.items", len(norm)))
	pctx, pspan := tracer.Start(ctx, "order.persist_pending")
	err = uc.repo.Insert(pctx, o)
	endSpan(pspan, err)
	if err != nil {
		endSpan(span, err)
		return nil, err
	}

	uc.process(ctx, o, norm)

	fctx, fspan := tracer.Start(ctx, "order.finalize", trace.WithAttributes(attribute.String("order.status", string(o.Status))))
	err = uc.repo.Finalize(fctx, o)
	endSpan(fspan, err)
	if err != nil {
		if errors.Is(err, ErrStatusChanged) && o.ReservationID != "" {
			// Se canceló mientras se procesaba: deshacer la reserva.
			uc.compensate(ctx, o.ID)
		}
		endSpan(span, err)
		return nil, err
	}
	span.SetAttributes(attribute.String("order.status", string(o.Status)))
	if o.RejectionCode != "" {
		span.SetAttributes(attribute.String("order.rejection_code", o.RejectionCode))
	}
	return o, nil
}

// process consulta precios, disponibilidad y reserva; deja el pedido
// CONFIRMED o REJECTED. Cada paso es un span hijo de "order.create", y las
// llamadas gRPC a Inventory cuelgan de él.
func (uc *OrderUsecase) process(ctx context.Context, o *Order, lines []Line) {
	qctx, qspan := tracer.Start(ctx, "order.price_lookup")
	for i := range o.Items {
		info, err := uc.inventory.GetProduct(qctx, o.Items[i].ProductID)
		if err != nil {
			uc.rejectFromError(o, err)
			endSpan(qspan, err)
			return
		}
		o.Items[i].SKU, o.Items[i].Name, o.Items[i].UnitPriceCents = info.SKU, info.Name, info.PriceCents
	}
	total, err := ComputeTotal(o.Items)
	if err != nil {
		o.reject(RejectTotalOutOfRange, err.Error())
		endSpan(qspan, err)
		return
	}
	o.TotalCents = total
	endSpan(qspan, nil)

	actx, aspan := tracer.Start(ctx, "order.check_availability")
	ok, err := uc.inventory.CheckAvailability(actx, lines)
	aspan.SetAttributes(attribute.Bool("inventory.all_available", ok))
	endSpan(aspan, err)
	if err != nil {
		uc.rejectFromError(o, err)
		return
	}
	if !ok {
		o.reject(RejectInsufficientStock, "no hay stock suficiente para todas las líneas")
		return
	}

	rctx, rspan := tracer.Start(ctx, "order.reserve_stock")
	resID, err := uc.inventory.Reserve(rctx, ReservationKey(o.ID), lines)
	endSpan(rspan, err)
	if err != nil {
		if errors.Is(err, ErrInventoryUnavailable) {
			// Resultado incierto: la reserva pudo haberse aplicado. Se libera por
			// clave; Inventory crea una "lápida" si la reserva aún no existe.
			uc.compensate(ctx, o.ID)
		}
		uc.rejectFromError(o, err)
		return
	}
	o.ReservationID = resID
	o.Status = StatusConfirmed
}

func (uc *OrderUsecase) rejectFromError(o *Order, err error) {
	switch {
	case errors.Is(err, ErrProductNotFound):
		o.reject(RejectProductNotFound, err.Error())
	case errors.Is(err, ErrInsufficientStock):
		o.reject(RejectInsufficientStock, err.Error())
	default:
		o.reject(RejectInventoryUnavailable, err.Error())
	}
}

// compensate libera la reserva del pedido con un contexto propio: el original
// puede haber expirado precisamente por el fallo que se está compensando.
// Conserva los valores del contexto (trazas, metadatos) pero no su cancelación.
func (uc *OrderUsecase) compensate(ctx context.Context, orderID string) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), uc.compTimeout)
	defer cancel()
	cctx, span := tracer.Start(cctx, "order.compensate_reservation")
	endSpan(span, uc.inventory.ReleaseByKey(cctx, ReservationKey(orderID)))
}

// CancelOrder cancela un pedido PENDING o CONFIRMED. Si estaba confirmado,
// primero libera el stock; si Inventory no responde, el pedido no cambia.
func (uc *OrderUsecase) CancelOrder(ctx context.Context, id string) (*Order, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	o, err := uc.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !CanTransition(o.Status, StatusCancelled) {
		return nil, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, o.Status, StatusCancelled)
	}
	if o.Status == StatusConfirmed && o.ReservationID != "" {
		if err := uc.inventory.ReleaseByID(ctx, o.ReservationID); err != nil {
			return nil, err
		}
	}
	updated, err := uc.repo.UpdateStatus(ctx, id, o.Status, StatusCancelled)
	if errors.Is(err, ErrStatusChanged) {
		return nil, fmt.Errorf("%w: el pedido cambió de estado, reintentar", ErrInvalidTransition)
	}
	return updated, err
}

// GetOrder devuelve un pedido.
func (uc *OrderUsecase) GetOrder(ctx context.Context, id string) (*Order, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	return uc.repo.Get(ctx, id)
}

// ListOrders lista pedidos, opcionalmente filtrados por estado.
func (uc *OrderUsecase) ListOrders(ctx context.Context, status Status, limit, offset int) ([]*Order, error) {
	if status != "" && !validStatus(status) {
		return nil, fmt.Errorf("%w: estado %q", ErrInvalidArgument, status)
	}
	if limit <= 0 {
		limit = DefaultListLimit
	}
	limit = min(limit, MaxListLimit)
	offset = max(offset, 0)
	return uc.repo.List(ctx, status, limit, offset)
}

// NormalizeLines valida y fusiona líneas repetidas, ordenadas por producto.
func NormalizeLines(lines []Line) ([]Line, error) {
	if len(lines) == 0 {
		return nil, fmt.Errorf("%w: el pedido debe tener al menos un producto", ErrInvalidArgument)
	}
	if len(lines) > MaxItemsPerOrder {
		return nil, fmt.Errorf("%w: máximo %d productos por pedido", ErrInvalidArgument, MaxItemsPerOrder)
	}
	merged := map[string]int32{}
	for _, l := range lines {
		if err := validateID(l.ProductID); err != nil {
			return nil, err
		}
		if l.Quantity <= 0 || l.Quantity > MaxQuantityItem {
			return nil, fmt.Errorf("%w: la cantidad debe estar entre 1 y %d", ErrInvalidArgument, MaxQuantityItem)
		}
		id := strings.ToLower(l.ProductID)
		merged[id] += l.Quantity
		if merged[id] > MaxQuantityItem {
			return nil, fmt.Errorf("%w: la cantidad debe estar entre 1 y %d", ErrInvalidArgument, MaxQuantityItem)
		}
	}
	out := make([]Line, 0, len(merged))
	for id, q := range merged {
		out = append(out, Line{ProductID: id, Quantity: q})
	}
	slices.SortFunc(out, func(a, b Line) int { return strings.Compare(a.ProductID, b.ProductID) })
	return out, nil
}

func validStatus(s Status) bool {
	switch s {
	case StatusPending, StatusConfirmed, StatusRejected, StatusCancelled:
		return true
	}
	return false
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validateID(id string) error {
	if !uuidPattern.MatchString(id) {
		return fmt.Errorf("%w: id %q no es un UUID", ErrInvalidArgument, id)
	}
	return nil
}
