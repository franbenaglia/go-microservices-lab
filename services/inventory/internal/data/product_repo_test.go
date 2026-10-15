package data

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/franbenaglia/go-microservices-lab/pkg/postgres/pgtest"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/internal/biz"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/migrations"
)

func newRepo(t *testing.T) *ProductRepo {
	t.Helper()
	return NewProductRepo(pgtest.NewPool(t, migrations.FS))
}

func mustCreate(t *testing.T, r *ProductRepo, sku string, stock int32) *biz.Product {
	t.Helper()
	p, err := r.Create(context.Background(), &biz.Product{SKU: sku, Name: sku, PriceCents: 100, Available: stock})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func stock(t *testing.T, r *ProductRepo, id string) (int32, int32) {
	t.Helper()
	p, err := r.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return p.Available, p.Reserved
}

func TestCreateAndDuplicateSKU(t *testing.T) {
	r := newRepo(t)
	p := mustCreate(t, r, "SKU-1", 5)
	got, err := r.Get(context.Background(), p.ID)
	if err != nil || got.SKU != "SKU-1" || got.Available != 5 {
		t.Fatalf("got %+v err %v", got, err)
	}
	if _, err := r.Create(context.Background(), &biz.Product{SKU: "SKU-1", Name: "x"}); !errors.Is(err, biz.ErrSKUAlreadyExists) {
		t.Fatalf("esperaba ErrSKUAlreadyExists, got %v", err)
	}
	if _, err := r.Get(context.Background(), "0199aaaa-0000-7000-8000-000000000999"); !errors.Is(err, biz.ErrProductNotFound) {
		t.Fatalf("esperaba ErrProductNotFound, got %v", err)
	}
}

func TestAdjustStockNeverNegative(t *testing.T) {
	r := newRepo(t)
	p := mustCreate(t, r, "SKU-ADJ", 3)
	if _, err := r.AdjustStock(context.Background(), p.ID, -4); !errors.Is(err, biz.ErrInsufficientStock) {
		t.Fatalf("got %v", err)
	}
	if got, err := r.AdjustStock(context.Background(), p.ID, -3); err != nil || got.Available != 0 {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestReserveAndRelease(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	a, b := mustCreate(t, r, "SKU-A", 10), mustCreate(t, r, "SKU-B", 1)
	lines, _ := biz.NormalizeLines([]biz.StockLine{{ProductID: a.ID, Quantity: 4}, {ProductID: b.ID, Quantity: 1}})

	res, replayed, err := r.Reserve(ctx, "k1", lines)
	if err != nil || replayed {
		t.Fatalf("reserve: %v replayed=%v", err, replayed)
	}
	if av, rs := stock(t, r, a.ID); av != 6 || rs != 4 {
		t.Fatalf("A: available=%d reserved=%d", av, rs)
	}

	// Todo o nada: B ya no tiene stock, así que A tampoco debe cambiar.
	if _, _, err := r.Reserve(ctx, "k2", lines); !errors.Is(err, biz.ErrInsufficientStock) {
		t.Fatalf("esperaba ErrInsufficientStock, got %v", err)
	}
	if av, _ := stock(t, r, a.ID); av != 6 {
		t.Fatalf("la reserva fallida modificó A: available=%d", av)
	}

	if _, already, err := r.Release(ctx, biz.ReleaseTarget{ReservationID: res.ID}); err != nil || already {
		t.Fatalf("release: %v already=%v", err, already)
	}
	if av, rs := stock(t, r, a.ID); av != 10 || rs != 0 {
		t.Fatalf("tras liberar A: available=%d reserved=%d", av, rs)
	}
	if _, already, err := r.Release(ctx, biz.ReleaseTarget{IdempotencyKey: "k1"}); err != nil || !already {
		t.Fatalf("segunda liberación debe ser idempotente: %v already=%v", err, already)
	}
	if _, _, err := r.Release(ctx, biz.ReleaseTarget{ReservationID: "0199aaaa-0000-7000-8000-000000000999"}); !errors.Is(err, biz.ErrReservationNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestReserveIdempotency(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	a := mustCreate(t, r, "SKU-I", 10)
	lines := []biz.StockLine{{ProductID: a.ID, Quantity: 3}}

	first, _, err := r.Reserve(ctx, "order:1", lines)
	if err != nil {
		t.Fatal(err)
	}
	again, replayed, err := r.Reserve(ctx, "order:1", lines)
	if err != nil || !replayed || again.ID != first.ID {
		t.Fatalf("reintento: id=%s replayed=%v err=%v", again.ID, replayed, err)
	}
	if av, _ := stock(t, r, a.ID); av != 7 {
		t.Fatalf("el reintento descontó stock dos veces: available=%d", av)
	}
	if _, _, err := r.Reserve(ctx, "order:1", []biz.StockLine{{ProductID: a.ID, Quantity: 5}}); !errors.Is(err, biz.ErrIdempotencyKeyConflict) {
		t.Fatalf("misma clave con otro contenido: got %v", err)
	}
}

// Una liberación por clave que llega antes que la reserva deja una lápida:
// la reserva tardía no debe descontar stock.
func TestReleaseBeforeReserveTombstone(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	a := mustCreate(t, r, "SKU-T", 5)
	if _, _, err := r.Release(ctx, biz.ReleaseTarget{IdempotencyKey: "order:late"}); err != nil {
		t.Fatal(err)
	}
	res, replayed, err := r.Reserve(ctx, "order:late", []biz.StockLine{{ProductID: a.ID, Quantity: 2}})
	if err != nil || !replayed || !res.Released {
		t.Fatalf("got res=%+v replayed=%v err=%v", res, replayed, err)
	}
	if av, rs := stock(t, r, a.ID); av != 5 || rs != 0 {
		t.Fatalf("la reserva tardía modificó el stock: available=%d reserved=%d", av, rs)
	}
}

// Muchas reservas concurrentes sobre el mismo producto nunca producen sobreventa.
func TestConcurrentReservationsNoOversell(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	const stockUnits, workers = 20, 60
	a := mustCreate(t, r, "SKU-C", stockUnits)

	var ok, insufficient atomic.Int32
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			_, _, err := r.Reserve(ctx, fmt.Sprintf("k-%d", i), []biz.StockLine{{ProductID: a.ID, Quantity: 1}})
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, biz.ErrInsufficientStock):
				insufficient.Add(1)
			default:
				t.Errorf("error inesperado: %v", err)
			}
		})
	}
	wg.Wait()
	if ok.Load() != stockUnits || insufficient.Load() != workers-stockUnits {
		t.Fatalf("ok=%d insuficientes=%d", ok.Load(), insufficient.Load())
	}
	if av, rs := stock(t, r, a.ID); av != 0 || rs != stockUnits {
		t.Fatalf("available=%d reserved=%d", av, rs)
	}
}

// La misma clave enviada en paralelo (reintentos simultáneos) reserva una sola vez.
func TestConcurrentSameKey(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	a := mustCreate(t, r, "SKU-K", 10)
	ids := make([]string, 10)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Go(func() {
			res, _, err := r.Reserve(ctx, "order:same", []biz.StockLine{{ProductID: a.ID, Quantity: 2}})
			if err != nil {
				t.Errorf("reserve: %v", err)
				return
			}
			ids[i] = res.ID
		})
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("se crearon reservas distintas: %v", ids)
		}
	}
	if av, rs := stock(t, r, a.ID); av != 8 || rs != 2 {
		t.Fatalf("available=%d reserved=%d", av, rs)
	}
}
