package data

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	"github.com/google/uuid"

	inventoryv1 "github.com/franbenaglia/go-microservices-lab/api/gen/go/inventory/v1"
	"github.com/franbenaglia/go-microservices-lab/pkg/postgres/pgtest"
	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/biz"
	"github.com/franbenaglia/go-microservices-lab/services/orders/migrations"
)

const prodA = "0199aaaa-0000-7000-8000-000000000001"

func newOrderID(t *testing.T) string {
	t.Helper()
	u, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return u.String()
}

func TestOrderRepoLifecycle(t *testing.T) {
	ctx := context.Background()
	r := NewOrderRepo(pgtest.NewPool(t, migrations.FS))

	o := &biz.Order{ID: newOrderID(t), Status: biz.StatusPending, Items: []biz.Item{{ProductID: prodA, Quantity: 2}}}
	if err := r.Insert(ctx, o); err != nil {
		t.Fatal(err)
	}

	o.Items[0].SKU, o.Items[0].Name, o.Items[0].UnitPriceCents = "A", "Producto A", 300
	o.TotalCents, o.Status, o.ReservationID = 600, biz.StatusConfirmed, newOrderID(t)
	if err := r.Finalize(ctx, o); err != nil {
		t.Fatal(err)
	}
	if err := r.Finalize(ctx, o); !errors.Is(err, biz.ErrStatusChanged) {
		t.Fatalf("Finalize solo se permite desde PENDING: got %v", err)
	}

	got, err := r.Get(ctx, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != biz.StatusConfirmed || got.TotalCents != 600 || got.Items[0].UnitPriceCents != 300 || got.ReservationID != o.ReservationID {
		t.Fatalf("pedido leído: %+v", got)
	}

	if _, err := r.UpdateStatus(ctx, o.ID, biz.StatusPending, biz.StatusCancelled); !errors.Is(err, biz.ErrStatusChanged) {
		t.Fatalf("transición desde un estado que no es el actual: got %v", err)
	}
	c, err := r.UpdateStatus(ctx, o.ID, biz.StatusConfirmed, biz.StatusCancelled)
	if err != nil || c.Status != biz.StatusCancelled {
		t.Fatalf("cancelar: %+v %v", c, err)
	}

	rej := &biz.Order{ID: newOrderID(t), Status: biz.StatusPending, Items: []biz.Item{{ProductID: prodA, Quantity: 1}}}
	_ = r.Insert(ctx, rej)
	rej.Status, rej.RejectionCode, rej.RejectionReason = biz.StatusRejected, biz.RejectInsufficientStock, "sin stock"
	if err := r.Finalize(ctx, rej); err != nil {
		t.Fatal(err)
	}
	list, err := r.List(ctx, biz.StatusRejected, 10, 0)
	if err != nil || len(list) != 1 || list[0].RejectionCode != biz.RejectInsufficientStock || len(list[0].Items) != 1 {
		t.Fatalf("List REJECTED: %+v %v", list, err)
	}
	all, _ := r.List(ctx, "", 10, 0)
	if len(all) != 2 || all[0].ID != rej.ID {
		t.Fatalf("List sin filtro (más reciente primero): %d pedidos", len(all))
	}
	if _, err := r.Get(ctx, newOrderID(t)); !errors.Is(err, biz.ErrOrderNotFound) {
		t.Fatalf("got %v", err)
	}
}

// fakeInventory es un servidor gRPC de Inventory controlado por la prueba.
type fakeInventory struct {
	inventoryv1.UnimplementedInventoryServiceServer
	delay atomic.Int64 // nanosegundos; se lee desde los handlers gRPC
}

func (f *fakeInventory) GetProduct(ctx context.Context, req *inventoryv1.GetProductRequest) (*inventoryv1.Product, error) {
	if d := time.Duration(f.delay.Load()); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if req.GetId() != prodA {
		return nil, inventoryv1.ErrorProductNotFound("producto %s", req.GetId())
	}
	return &inventoryv1.Product{Id: prodA, Sku: "A", Name: "Producto A", PriceCents: 300}, nil
}

func (f *fakeInventory) ReserveStock(context.Context, *inventoryv1.ReserveStockRequest) (*inventoryv1.ReserveStockResponse, error) {
	return nil, inventoryv1.ErrorInsufficientStock("sin stock")
}

func (f *fakeInventory) ReleaseStock(context.Context, *inventoryv1.ReleaseStockRequest) (*inventoryv1.ReleaseStockResponse, error) {
	return nil, kerrors.InternalServer("BOOM", "fallo interno")
}

func startFake(t *testing.T, f *fakeInventory, clientTimeout time.Duration) (*InventoryClient, func()) {
	t.Helper()
	srv := kgrpc.NewServer(kgrpc.Address("127.0.0.1:0"))
	inventoryv1.RegisterInventoryServiceServer(srv, f)
	ep, err := srv.Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Start(context.Background()) }()
	stop := func() { _ = srv.Stop(context.Background()) }
	t.Cleanup(stop)

	conn, err := kgrpc.NewClient(context.Background(), kgrpc.WithEndpoint(ep.Host), kgrpc.WithTimeout(clientTimeout))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return NewInventoryClient(inventoryv1.NewInventoryServiceClient(conn)), stop
}

// Comprueba la traducción de errores gRPC a errores de dominio.
func TestInventoryClientErrorMapping(t *testing.T) {
	ctx := context.Background()
	f := &fakeInventory{}
	c, stop := startFake(t, f, time.Second)

	if p, err := c.GetProduct(ctx, prodA); err != nil || p.PriceCents != 300 {
		t.Fatalf("GetProduct: %+v %v", p, err)
	}
	if _, err := c.GetProduct(ctx, "0199aaaa-0000-7000-8000-000000000002"); !errors.Is(err, biz.ErrProductNotFound) {
		t.Fatalf("NotFound: got %v", err)
	}
	if _, err := c.Reserve(ctx, "k", []biz.Line{{ProductID: prodA, Quantity: 1}}); !errors.Is(err, biz.ErrInsufficientStock) {
		t.Fatalf("InsufficientStock: got %v", err)
	}
	if err := c.ReleaseByKey(ctx, "k"); !errors.Is(err, biz.ErrInventoryUnavailable) {
		t.Fatalf("un 500 se trata como indisponibilidad: got %v", err)
	}

	// Timeout de cliente: el servidor tarda más que el timeout configurado.
	f.delay.Store(int64(2 * time.Second))
	start := time.Now()
	if _, err := c.GetProduct(ctx, prodA); !errors.Is(err, biz.ErrInventoryUnavailable) {
		t.Fatalf("timeout: got %v", err)
	}
	if el := time.Since(start); el > 1500*time.Millisecond {
		t.Fatalf("no se respetó el timeout del cliente (%s)", el)
	}

	// Servidor detenido: Unavailable.
	f.delay.Store(0)
	stop()
	_, err := c.GetProduct(ctx, prodA)
	if !errors.Is(err, biz.ErrInventoryUnavailable) {
		t.Fatalf("servidor caído: got %v", err)
	}
	if msg := fmt.Sprint(err); len(msg) > 120 {
		t.Fatalf("el mensaje no debe filtrar detalles de transporte: %q", msg)
	}
}
