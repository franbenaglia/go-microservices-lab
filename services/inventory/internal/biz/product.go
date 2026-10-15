// Package biz contiene la lógica de negocio de Inventory: reglas de productos y
// de stock. No conoce gRPC, HTTP ni PostgreSQL; solo define los puertos
// (interfaces) que implementa la capa data.
package biz

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Límites de validación.
const (
	MaxNameLen       = 200
	MaxDescLen       = 2000
	MaxLinesPerOp    = 100
	MaxQuantityLine  = 100_000
	MaxIdemKeyLen    = 128
	DefaultListLimit = 50
	MaxListLimit     = 500
)

var skuPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9-]{1,63}$`)

// Product es un artículo con su stock. Available son las unidades vendibles y
// Reserved las comprometidas por pedidos confirmados.
type Product struct {
	ID          string
	SKU         string
	Name        string
	Description string
	PriceCents  int64
	Available   int32
	Reserved    int32
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// StockLine es una cantidad de un producto.
type StockLine struct {
	ProductID string
	Quantity  int32
}

// AvailabilityLine es el resultado de disponibilidad para una línea.
type AvailabilityLine struct {
	ProductID  string
	Requested  int32
	Available  int32
	Sufficient bool
}

// Reservation es una reserva de stock identificada por una clave de idempotencia.
type Reservation struct {
	ID             string
	IdempotencyKey string
	Released       bool
	Lines          []StockLine
}

// ReleaseTarget indica qué reserva liberar: por ID o por clave de idempotencia.
type ReleaseTarget struct {
	ReservationID  string
	IdempotencyKey string
}

// ProductRepo es el puerto de persistencia de Inventory.
type ProductRepo interface {
	Create(ctx context.Context, p *Product) (*Product, error)
	Update(ctx context.Context, id, name, description string, priceCents int64) (*Product, error)
	Get(ctx context.Context, id string) (*Product, error)
	List(ctx context.Context, limit, offset int) ([]*Product, error)
	// GetMany devuelve los productos existentes entre ids (los ausentes se omiten).
	GetMany(ctx context.Context, ids []string) (map[string]*Product, error)
	// AdjustStock suma delta al stock disponible sin permitir valores negativos.
	AdjustStock(ctx context.Context, id string, delta int32) (*Product, error)
	// Reserve reserva todas las líneas o ninguna. Si la clave ya existe con el
	// mismo contenido devuelve la reserva previa y replayed=true.
	Reserve(ctx context.Context, idempotencyKey string, lines []StockLine) (res *Reservation, replayed bool, err error)
	// Release libera una reserva; si ya estaba liberada devuelve alreadyReleased=true.
	Release(ctx context.Context, target ReleaseTarget) (res *Reservation, alreadyReleased bool, err error)
}

// InventoryUsecase aplica las reglas de negocio antes de delegar en el repositorio.
type InventoryUsecase struct {
	repo ProductRepo
}

// NewInventoryUsecase construye el caso de uso.
func NewInventoryUsecase(repo ProductRepo) *InventoryUsecase {
	return &InventoryUsecase{repo: repo}
}

// NewProductInput son los datos para crear un producto.
type NewProductInput struct {
	SKU          string
	Name         string
	Description  string
	PriceCents   int64
	InitialStock int32
}

// CreateProduct valida y crea un producto.
func (uc *InventoryUsecase) CreateProduct(ctx context.Context, in NewProductInput) (*Product, error) {
	p := &Product{
		SKU:         strings.ToUpper(strings.TrimSpace(in.SKU)),
		Name:        strings.TrimSpace(in.Name),
		Description: strings.TrimSpace(in.Description),
		PriceCents:  in.PriceCents,
		Available:   in.InitialStock,
	}
	if !skuPattern.MatchString(p.SKU) {
		return nil, fmt.Errorf("%w: SKU debe tener 2-64 caracteres [A-Z0-9-]", ErrInvalidArgument)
	}
	if err := validateDetails(p.Name, p.Description, p.PriceCents); err != nil {
		return nil, err
	}
	if in.InitialStock < 0 || in.InitialStock > MaxQuantityLine {
		return nil, fmt.Errorf("%w: stock inicial fuera de rango", ErrInvalidArgument)
	}
	return uc.repo.Create(ctx, p)
}

// UpdateProduct modifica los datos descriptivos y el precio (no el stock).
func (uc *InventoryUsecase) UpdateProduct(ctx context.Context, id, name, description string, priceCents int64) (*Product, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if err := validateDetails(name, description, priceCents); err != nil {
		return nil, err
	}
	return uc.repo.Update(ctx, id, name, description, priceCents)
}

// GetProduct devuelve un producto.
func (uc *InventoryUsecase) GetProduct(ctx context.Context, id string) (*Product, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	return uc.repo.Get(ctx, id)
}

// ListProducts lista productos con paginación acotada.
func (uc *InventoryUsecase) ListProducts(ctx context.Context, limit, offset int) ([]*Product, error) {
	limit, offset = NormalizePage(limit, offset)
	return uc.repo.List(ctx, limit, offset)
}

// AdjustStock incrementa o reduce el stock disponible.
func (uc *InventoryUsecase) AdjustStock(ctx context.Context, id string, delta int32) (*Product, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	if delta == 0 || delta > MaxQuantityLine || delta < -MaxQuantityLine {
		return nil, fmt.Errorf("%w: delta debe ser distinto de 0 y |delta| <= %d", ErrInvalidArgument, MaxQuantityLine)
	}
	return uc.repo.AdjustStock(ctx, id, delta)
}

// CheckAvailability informa, sin modificar nada, si hay stock para cada línea.
func (uc *InventoryUsecase) CheckAvailability(ctx context.Context, lines []StockLine) (bool, []AvailabilityLine, error) {
	norm, err := NormalizeLines(lines)
	if err != nil {
		return false, nil, err
	}
	ids := make([]string, len(norm))
	for i, l := range norm {
		ids[i] = l.ProductID
	}
	products, err := uc.repo.GetMany(ctx, ids)
	if err != nil {
		return false, nil, err
	}
	all, out := EvaluateAvailability(norm, products)
	return all, out, nil
}

// ReserveStock reserva stock de forma atómica e idempotente.
func (uc *InventoryUsecase) ReserveStock(ctx context.Context, idempotencyKey string, lines []StockLine) (*Reservation, bool, error) {
	key := strings.TrimSpace(idempotencyKey)
	if key == "" || len(key) > MaxIdemKeyLen {
		return nil, false, fmt.Errorf("%w: idempotency_key obligatoria (máx. %d caracteres)", ErrInvalidArgument, MaxIdemKeyLen)
	}
	norm, err := NormalizeLines(lines)
	if err != nil {
		return nil, false, err
	}
	return uc.repo.Reserve(ctx, key, norm)
}

// ReleaseStock libera una reserva por ID o por clave de idempotencia.
func (uc *InventoryUsecase) ReleaseStock(ctx context.Context, target ReleaseTarget) (*Reservation, bool, error) {
	target.ReservationID = strings.TrimSpace(target.ReservationID)
	target.IdempotencyKey = strings.TrimSpace(target.IdempotencyKey)
	switch {
	case target.ReservationID != "" && target.IdempotencyKey != "":
		return nil, false, fmt.Errorf("%w: indicar reservation_id o idempotency_key, no ambos", ErrInvalidArgument)
	case target.ReservationID != "":
		if err := validateID(target.ReservationID); err != nil {
			return nil, false, err
		}
	case target.IdempotencyKey == "":
		return nil, false, fmt.Errorf("%w: falta reservation_id o idempotency_key", ErrInvalidArgument)
	}
	return uc.repo.Release(ctx, target)
}

// NormalizeLines valida las líneas, fusiona productos repetidos y las ordena por
// ID. El orden fijo evita interbloqueos cuando dos transacciones bloquean las
// mismas filas.
func NormalizeLines(lines []StockLine) ([]StockLine, error) {
	if len(lines) == 0 {
		return nil, fmt.Errorf("%w: se requiere al menos una línea", ErrInvalidArgument)
	}
	if len(lines) > MaxLinesPerOp {
		return nil, fmt.Errorf("%w: máximo %d líneas", ErrInvalidArgument, MaxLinesPerOp)
	}
	merged := make(map[string]int64, len(lines))
	for _, l := range lines {
		if err := validateID(l.ProductID); err != nil {
			return nil, err
		}
		if l.Quantity <= 0 {
			return nil, fmt.Errorf("%w: la cantidad debe ser positiva", ErrInvalidArgument)
		}
		// PostgreSQL devuelve los UUID en minúsculas; se normaliza para comparar.
		id := strings.ToLower(l.ProductID)
		merged[id] += int64(l.Quantity)
		if merged[id] > MaxQuantityLine {
			return nil, fmt.Errorf("%w: cantidad máxima por producto %d", ErrInvalidArgument, MaxQuantityLine)
		}
	}
	out := make([]StockLine, 0, len(merged))
	for id, q := range merged {
		out = append(out, StockLine{ProductID: id, Quantity: int32(q)})
	}
	slices.SortFunc(out, func(a, b StockLine) int { return strings.Compare(a.ProductID, b.ProductID) })
	return out, nil
}

// EvaluateAvailability compara las líneas con el stock actual.
func EvaluateAvailability(lines []StockLine, products map[string]*Product) (bool, []AvailabilityLine) {
	all := true
	out := make([]AvailabilityLine, len(lines))
	for i, l := range lines {
		var avail int32
		if p, ok := products[l.ProductID]; ok {
			avail = p.Available
		}
		ok := avail >= l.Quantity
		all = all && ok
		out[i] = AvailabilityLine{ProductID: l.ProductID, Requested: l.Quantity, Available: avail, Sufficient: ok}
	}
	return all, out
}

// SameLines indica si dos listas normalizadas son idénticas (para detectar la
// reutilización de una clave de idempotencia con otro contenido).
func SameLines(a, b []StockLine) bool {
	return slices.Equal(a, b)
}

// NormalizePage acota los parámetros de paginación.
func NormalizePage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func validateDetails(name, description string, priceCents int64) error {
	if name == "" || len(name) > MaxNameLen {
		return fmt.Errorf("%w: el nombre debe tener entre 1 y %d caracteres", ErrInvalidArgument, MaxNameLen)
	}
	if len(description) > MaxDescLen {
		return fmt.Errorf("%w: descripción demasiado larga", ErrInvalidArgument)
	}
	if priceCents < 0 {
		return fmt.Errorf("%w: el precio no puede ser negativo", ErrInvalidArgument)
	}
	return nil
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validateID(id string) error {
	if !uuidPattern.MatchString(id) {
		return fmt.Errorf("%w: id %q no es un UUID", ErrInvalidArgument, id)
	}
	return nil
}
