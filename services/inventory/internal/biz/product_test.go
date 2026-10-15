package biz

import (
	"context"
	"errors"
	"testing"
)

const (
	idA = "0199aaaa-0000-7000-8000-000000000001"
	idB = "0199aaaa-0000-7000-8000-000000000002"
)

// fakeRepo registra lo que recibe; solo se usa para comprobar la validación.
type fakeRepo struct {
	ProductRepo
	created  *Product
	reserved []StockLine
	products map[string]*Product
}

func (f *fakeRepo) Create(_ context.Context, p *Product) (*Product, error) {
	f.created = p
	return p, nil
}

func (f *fakeRepo) GetMany(_ context.Context, _ []string) (map[string]*Product, error) {
	return f.products, nil
}

func (f *fakeRepo) Reserve(_ context.Context, key string, lines []StockLine) (*Reservation, bool, error) {
	f.reserved = lines
	return &Reservation{ID: "r1", IdempotencyKey: key, Lines: lines}, false, nil
}

func (f *fakeRepo) Release(_ context.Context, t ReleaseTarget) (*Reservation, bool, error) {
	return &Reservation{ID: t.ReservationID, IdempotencyKey: t.IdempotencyKey}, false, nil
}

func (f *fakeRepo) AdjustStock(_ context.Context, id string, _ int32) (*Product, error) {
	return &Product{ID: id}, nil
}

func TestNormalizeLines(t *testing.T) {
	upperB := "0199AAAA-0000-7000-8000-000000000002"
	got, err := NormalizeLines([]StockLine{
		{ProductID: upperB, Quantity: 2},
		{ProductID: idA, Quantity: 1},
		{ProductID: idB, Quantity: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []StockLine{{ProductID: idA, Quantity: 1}, {ProductID: idB, Quantity: 5}}
	if !SameLines(got, want) {
		t.Fatalf("got %v, want %v (fusión, minúsculas y orden)", got, want)
	}
}

func TestNormalizeLinesInvalid(t *testing.T) {
	cases := map[string][]StockLine{
		"vacío":             nil,
		"cantidad cero":     {{ProductID: idA, Quantity: 0}},
		"cantidad negativa": {{ProductID: idA, Quantity: -1}},
		"id inválido":       {{ProductID: "abc", Quantity: 1}},
		"suma excesiva":     {{ProductID: idA, Quantity: MaxQuantityLine}, {ProductID: idA, Quantity: 1}},
	}
	for name, lines := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeLines(lines); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("esperaba ErrInvalidArgument, got %v", err)
			}
		})
	}
}

func TestEvaluateAvailability(t *testing.T) {
	products := map[string]*Product{idA: {ID: idA, Available: 5}}
	all, lines := EvaluateAvailability(
		[]StockLine{{ProductID: idA, Quantity: 5}, {ProductID: idB, Quantity: 1}}, products)
	if all {
		t.Fatal("idB no existe: no debería estar todo disponible")
	}
	if !lines[0].Sufficient || lines[0].Available != 5 {
		t.Fatalf("línea A: %+v", lines[0])
	}
	if lines[1].Sufficient || lines[1].Available != 0 {
		t.Fatalf("línea B: %+v", lines[1])
	}
}

func TestCreateProductValidation(t *testing.T) {
	repo := &fakeRepo{}
	uc := NewInventoryUsecase(repo)
	ctx := context.Background()

	if _, err := uc.CreateProduct(ctx, NewProductInput{SKU: " kb-01 ", Name: " Teclado ", PriceCents: 100}); err != nil {
		t.Fatal(err)
	}
	if repo.created.SKU != "KB-01" || repo.created.Name != "Teclado" {
		t.Fatalf("no se normalizó: %+v", repo.created)
	}

	bad := []NewProductInput{
		{SKU: "", Name: "x", PriceCents: 1},
		{SKU: "A B", Name: "x", PriceCents: 1},
		{SKU: "KB-01", Name: "", PriceCents: 1},
		{SKU: "KB-01", Name: "x", PriceCents: -1},
		{SKU: "KB-01", Name: "x", PriceCents: 1, InitialStock: -1},
	}
	for _, in := range bad {
		if _, err := uc.CreateProduct(ctx, in); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("%+v: esperaba ErrInvalidArgument, got %v", in, err)
		}
	}
}

func TestAdjustStockValidation(t *testing.T) {
	uc := NewInventoryUsecase(&fakeRepo{})
	for _, d := range []int32{0, MaxQuantityLine + 1, -MaxQuantityLine - 1} {
		if _, err := uc.AdjustStock(context.Background(), idA, d); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("delta %d: esperaba ErrInvalidArgument, got %v", d, err)
		}
	}
	if _, err := uc.AdjustStock(context.Background(), idA, -3); err != nil {
		t.Fatal(err)
	}
}

func TestReserveStockRequiresKey(t *testing.T) {
	repo := &fakeRepo{}
	uc := NewInventoryUsecase(repo)
	lines := []StockLine{{ProductID: idA, Quantity: 1}}
	if _, _, err := uc.ReserveStock(context.Background(), "  ", lines); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("esperaba ErrInvalidArgument, got %v", err)
	}
	if _, _, err := uc.ReserveStock(context.Background(), "order:1", lines); err != nil {
		t.Fatal(err)
	}
	if len(repo.reserved) != 1 {
		t.Fatal("no llegó al repositorio")
	}
}

func TestReleaseStockTarget(t *testing.T) {
	uc := NewInventoryUsecase(&fakeRepo{})
	ctx := context.Background()
	invalid := []ReleaseTarget{
		{},
		{ReservationID: idA, IdempotencyKey: "k"},
		{ReservationID: "no-uuid"},
	}
	for _, tg := range invalid {
		if _, _, err := uc.ReleaseStock(ctx, tg); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("%+v: esperaba ErrInvalidArgument, got %v", tg, err)
		}
	}
	if _, _, err := uc.ReleaseStock(ctx, ReleaseTarget{IdempotencyKey: "order:1"}); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizePage(t *testing.T) {
	cases := []struct{ l, o, wl, wo int }{
		{0, -1, DefaultListLimit, 0},
		{MaxListLimit + 1, 5, MaxListLimit, 5},
		{10, 0, 10, 0},
	}
	for _, c := range cases {
		l, o := NormalizePage(c.l, c.o)
		if l != c.wl || o != c.wo {
			t.Errorf("NormalizePage(%d,%d) = %d,%d", c.l, c.o, l, o)
		}
	}
}
