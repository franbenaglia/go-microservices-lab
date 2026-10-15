package biz

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"
)

const (
	pA = "0199aaaa-0000-7000-8000-000000000001"
	pB = "0199aaaa-0000-7000-8000-000000000002"
)

// --- fakes ---

type memRepo struct {
	mu          sync.Mutex
	orders      map[string]*Order
	finalizeErr error
}

func newMemRepo() *memRepo { return &memRepo{orders: map[string]*Order{}} }

func (r *memRepo) Insert(_ context.Context, o *Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *o
	r.orders[o.ID] = &cp
	return nil
}

func (r *memRepo) Finalize(_ context.Context, o *Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finalizeErr != nil {
		return r.finalizeErr
	}
	if r.orders[o.ID].Status != StatusPending {
		return ErrStatusChanged
	}
	cp := *o
	r.orders[o.ID] = &cp
	return nil
}

func (r *memRepo) UpdateStatus(_ context.Context, id string, from, to Status) (*Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.orders[id]
	if !ok {
		return nil, ErrOrderNotFound
	}
	if o.Status != from {
		return nil, ErrStatusChanged
	}
	o.Status = to
	cp := *o
	return &cp, nil
}

func (r *memRepo) Get(_ context.Context, id string) (*Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.orders[id]
	if !ok {
		return nil, ErrOrderNotFound
	}
	cp := *o
	return &cp, nil
}

func (r *memRepo) List(context.Context, Status, int, int) ([]*Order, error) { return nil, nil }

type fakeInventory struct {
	products     map[string]ProductInfo
	getErr       error
	available    bool
	checkErr     error
	reserveErr   error
	releaseErr   error
	releasedIDs  []string
	releasedKeys []string
	reserveCalls int
}

func (f *fakeInventory) GetProduct(_ context.Context, id string) (ProductInfo, error) {
	if f.getErr != nil {
		return ProductInfo{}, f.getErr
	}
	p, ok := f.products[id]
	if !ok {
		return ProductInfo{}, fmt.Errorf("%w: %s", ErrProductNotFound, id)
	}
	return p, nil
}

func (f *fakeInventory) CheckAvailability(context.Context, []Line) (bool, error) {
	return f.available, f.checkErr
}

func (f *fakeInventory) Reserve(context.Context, string, []Line) (string, error) {
	f.reserveCalls++
	if f.reserveErr != nil {
		return "", f.reserveErr
	}
	return "res-1", nil
}

func (f *fakeInventory) ReleaseByID(_ context.Context, id string) error {
	f.releasedIDs = append(f.releasedIDs, id)
	return f.releaseErr
}

func (f *fakeInventory) ReleaseByKey(_ context.Context, key string) error {
	f.releasedKeys = append(f.releasedKeys, key)
	return f.releaseErr
}

func newInventory() *fakeInventory {
	return &fakeInventory{
		products: map[string]ProductInfo{
			pA: {ID: pA, SKU: "A", Name: "Producto A", PriceCents: 250},
			pB: {ID: pB, SKU: "B", Name: "Producto B", PriceCents: 1000},
		},
		available: true,
	}
}

var seq int

func fixedID() (string, error) {
	seq++
	return fmt.Sprintf("0199bbbb-0000-7000-8000-%012d", seq), nil
}

func newUC(repo *memRepo, inv *fakeInventory) *OrderUsecase {
	return NewOrderUsecase(repo, inv, fixedID, time.Second)
}

var twoLines = []Line{{ProductID: pA, Quantity: 2}, {ProductID: pB, Quantity: 1}}

// --- tests ---

func TestCanTransition(t *testing.T) {
	allowed := map[[2]Status]bool{
		{StatusPending, StatusConfirmed}:   true,
		{StatusPending, StatusRejected}:    true,
		{StatusPending, StatusCancelled}:   true,
		{StatusConfirmed, StatusCancelled}: true,
	}
	all := []Status{StatusPending, StatusConfirmed, StatusRejected, StatusCancelled}
	for _, from := range all {
		for _, to := range all {
			if got := CanTransition(from, to); got != allowed[[2]Status{from, to}] {
				t.Errorf("CanTransition(%s, %s) = %v", from, to, got)
			}
		}
	}
}

func TestComputeTotal(t *testing.T) {
	total, err := ComputeTotal([]Item{{Quantity: 2, UnitPriceCents: 250}, {Quantity: 1, UnitPriceCents: 1000}})
	if err != nil || total != 1500 {
		t.Fatalf("total=%d err=%v", total, err)
	}
	if _, err := ComputeTotal([]Item{{Quantity: 2, UnitPriceCents: math.MaxInt64 / 2}, {Quantity: 1, UnitPriceCents: 10}}); err == nil {
		t.Fatal("esperaba desbordamiento")
	}
}

func TestNormalizeLines(t *testing.T) {
	got, err := NormalizeLines([]Line{{ProductID: pB, Quantity: 1}, {ProductID: pA, Quantity: 1}, {ProductID: pB, Quantity: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ProductID != pA || got[1].Quantity != 3 {
		t.Fatalf("got %+v", got)
	}
	for _, bad := range [][]Line{nil, {{ProductID: pA, Quantity: 0}}, {{ProductID: "x", Quantity: 1}}, {{ProductID: pA, Quantity: MaxQuantityItem + 1}}} {
		if _, err := NormalizeLines(bad); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("%+v: esperaba ErrInvalidArgument, got %v", bad, err)
		}
	}
}

func TestCreateOrderConfirmed(t *testing.T) {
	repo, inv := newMemRepo(), newInventory()
	o, err := newUC(repo, inv).CreateOrder(context.Background(), twoLines)
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != StatusConfirmed || o.ReservationID != "res-1" || o.TotalCents != 1500 {
		t.Fatalf("pedido inesperado: %+v", o)
	}
	if o.Items[0].Name != "Producto A" || o.Items[0].UnitPriceCents != 250 {
		t.Fatalf("no se copiaron los datos de catálogo: %+v", o.Items[0])
	}
	stored, _ := repo.Get(context.Background(), o.ID)
	if stored.Status != StatusConfirmed {
		t.Fatalf("no se persistió el estado final: %s", stored.Status)
	}
}

func TestCreateOrderRejections(t *testing.T) {
	cases := []struct {
		name        string
		setup       func(*fakeInventory)
		code        string
		compensated bool
	}{
		{"sin stock en la consulta", func(f *fakeInventory) { f.available = false }, RejectInsufficientStock, false},
		{"sin stock al reservar (carrera)", func(f *fakeInventory) { f.reserveErr = ErrInsufficientStock }, RejectInsufficientStock, false},
		{"producto inexistente", func(f *fakeInventory) { delete(f.products, pB) }, RejectProductNotFound, false},
		{"inventory caído al consultar precio", func(f *fakeInventory) { f.getErr = ErrInventoryUnavailable }, RejectInventoryUnavailable, false},
		{"inventory caído al consultar stock", func(f *fakeInventory) { f.checkErr = ErrInventoryUnavailable }, RejectInventoryUnavailable, false},
		{"reserva incierta (timeout)", func(f *fakeInventory) { f.reserveErr = ErrInventoryUnavailable }, RejectInventoryUnavailable, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, inv := newMemRepo(), newInventory()
			c.setup(inv)
			o, err := newUC(repo, inv).CreateOrder(context.Background(), twoLines)
			if err != nil {
				t.Fatal(err)
			}
			if o.Status != StatusRejected || o.RejectionCode != c.code {
				t.Fatalf("got %s/%s, want REJECTED/%s", o.Status, o.RejectionCode, c.code)
			}
			if got := len(inv.releasedKeys) == 1; got != c.compensated {
				t.Fatalf("compensación = %v, want %v", got, c.compensated)
			}
			if c.compensated && inv.releasedKeys[0] != ReservationKey(o.ID) {
				t.Fatalf("clave de compensación %q", inv.releasedKeys[0])
			}
		})
	}
}

func TestCreateOrderInvalidDoesNotPersist(t *testing.T) {
	repo := newMemRepo()
	if _, err := newUC(repo, newInventory()).CreateOrder(context.Background(), nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("got %v", err)
	}
	if len(repo.orders) != 0 {
		t.Fatal("no debería persistir pedidos inválidos")
	}
}

func TestCreateOrderCancelledWhileProcessing(t *testing.T) {
	repo, inv := newMemRepo(), newInventory()
	repo.finalizeErr = ErrStatusChanged
	if _, err := newUC(repo, inv).CreateOrder(context.Background(), twoLines); !errors.Is(err, ErrStatusChanged) {
		t.Fatalf("got %v", err)
	}
	if len(inv.releasedKeys) != 1 {
		t.Fatal("debería liberar la reserva si el pedido cambió de estado mientras se procesaba")
	}
}

func TestCancelOrder(t *testing.T) {
	ctx := context.Background()
	repo, inv := newMemRepo(), newInventory()
	uc := newUC(repo, inv)
	o, _ := uc.CreateOrder(ctx, twoLines)

	inv.releaseErr = ErrInventoryUnavailable
	if _, err := uc.CancelOrder(ctx, o.ID); !errors.Is(err, ErrInventoryUnavailable) {
		t.Fatalf("got %v", err)
	}
	if cur, _ := repo.Get(ctx, o.ID); cur.Status != StatusConfirmed {
		t.Fatalf("si no se pudo liberar, el pedido debe seguir CONFIRMED; está %s", cur.Status)
	}

	inv.releaseErr = nil
	c, err := uc.CancelOrder(ctx, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusCancelled || inv.releasedIDs[len(inv.releasedIDs)-1] != "res-1" {
		t.Fatalf("cancelación incorrecta: %+v, liberados %v", c, inv.releasedIDs)
	}
	if _, err := uc.CancelOrder(ctx, o.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("cancelar dos veces: got %v", err)
	}
}

func TestCancelRejectedOrderNotAllowed(t *testing.T) {
	ctx := context.Background()
	repo, inv := newMemRepo(), newInventory()
	inv.available = false
	uc := newUC(repo, inv)
	o, _ := uc.CreateOrder(ctx, twoLines)
	if _, err := uc.CancelOrder(ctx, o.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("got %v", err)
	}
}
