package server

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v3/errors"
	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	inventoryv1 "github.com/franbenaglia/go-microservices-lab/api/gen/go/inventory/v1"
	"github.com/franbenaglia/go-microservices-lab/pkg/platform"
	"github.com/franbenaglia/go-microservices-lab/pkg/postgres/pgtest"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/internal/biz"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/internal/conf"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/internal/data"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/internal/service"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/migrations"
)

// startServer levanta el servidor gRPC real de Inventory sobre PostgreSQL y
// devuelve un cliente generado conectado a él.
func startServer(t *testing.T) inventoryv1.InventoryServiceClient {
	t.Helper()
	pool := pgtest.NewPool(t, migrations.FS)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	id := platform.Identity{Service: "inventory-service", Instance: "test-1", Version: "test"}
	svc := service.NewInventoryService(biz.NewInventoryUsecase(data.NewProductRepo(pool)), id, logger)

	srv := NewGRPCServer(conf.Listener{Addr: "127.0.0.1:0", Timeout: platform.Duration(5 * time.Second)}, svc, logger)
	endpoint, err := srv.Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = srv.Stop(context.Background())
	})

	conn, err := kgrpc.NewClient(context.Background(), kgrpc.WithEndpoint(endpoint.Host))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return inventoryv1.NewInventoryServiceClient(conn)
}

func TestGRPCContract(t *testing.T) {
	c := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p, err := c.CreateProduct(ctx, &inventoryv1.CreateProductRequest{Sku: "GRPC-1", Name: "Ratón", PriceCents: 1500, InitialStock: 2})
	if err != nil {
		t.Fatal(err)
	}

	got, err := c.GetProduct(ctx, &inventoryv1.GetProductRequest{Id: p.GetId()})
	if err != nil || got.GetPriceCents() != 1500 {
		t.Fatalf("GetProduct: %v %v", got, err)
	}

	items := []*inventoryv1.StockLine{{ProductId: p.GetId(), Quantity: 2}}
	av, err := c.CheckAvailability(ctx, &inventoryv1.CheckAvailabilityRequest{Items: items})
	if err != nil || !av.GetAllAvailable() {
		t.Fatalf("CheckAvailability: %v %v", av, err)
	}

	res, err := c.ReserveStock(ctx, &inventoryv1.ReserveStockRequest{IdempotencyKey: "order:g1", Items: items})
	if err != nil || res.GetReservationId() == "" {
		t.Fatalf("ReserveStock: %v %v", res, err)
	}
	again, err := c.ReserveStock(ctx, &inventoryv1.ReserveStockRequest{IdempotencyKey: "order:g1", Items: items})
	if err != nil || !again.GetReplayed() || again.GetReservationId() != res.GetReservationId() {
		t.Fatalf("reintento ReserveStock: %v %v", again, err)
	}

	// Los errores de negocio viajan con código gRPC y razón de Kratos.
	_, err = c.ReserveStock(ctx, &inventoryv1.ReserveStockRequest{IdempotencyKey: "order:g2", Items: items})
	if !inventoryv1.IsInsufficientStock(err) {
		t.Fatalf("esperaba INSUFFICIENT_STOCK, got %v", err)
	}
	if errors.Code(err) != 409 {
		t.Fatalf("código Kratos %d", errors.Code(err))
	}

	_, err = c.GetProduct(ctx, &inventoryv1.GetProductRequest{Id: "0199aaaa-0000-7000-8000-000000000999"})
	if !inventoryv1.IsProductNotFound(err) || status.Code(err) != codes.NotFound {
		t.Fatalf("esperaba NotFound, got %v (%s)", err, status.Code(err))
	}

	rel, err := c.ReleaseStock(ctx, &inventoryv1.ReleaseStockRequest{
		Target: &inventoryv1.ReleaseStockRequest_IdempotencyKey{IdempotencyKey: "order:g1"},
	})
	if err != nil || rel.GetAlreadyReleased() {
		t.Fatalf("ReleaseStock: %v %v", rel, err)
	}

	d, err := c.Diagnose(ctx, &inventoryv1.DiagnoseRequest{})
	if err != nil || d.GetInstanceId() != "test-1" {
		t.Fatalf("Diagnose: %v %v", d, err)
	}
}

// El deadline del cliente se propaga: una llamada con un deadline ya vencido
// falla con DeadlineExceeded sin llegar a ejecutarse.
func TestGRPCDeadlinePropagation(t *testing.T) {
	c := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	_, err := c.Diagnose(ctx, &inventoryv1.DiagnoseRequest{})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("esperaba DeadlineExceeded, got %v", err)
	}
}
