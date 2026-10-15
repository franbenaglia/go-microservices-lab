// Package server construye los servidores HTTP y gRPC de Orders y el cliente
// gRPC hacia Inventory.
package server

import (
	"context"
	"log/slog"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/middleware/logging"
	"github.com/go-kratos/kratos/v3/middleware/metadata"
	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/transport/grpc"
	"github.com/go-kratos/kratos/v3/transport/http"
	ggrpc "google.golang.org/grpc"

	ordersv1 "github.com/franbenaglia/go-microservices-lab/api/gen/go/orders/v1"
	"github.com/franbenaglia/go-microservices-lab/pkg/httpcodec"
	"github.com/franbenaglia/go-microservices-lab/pkg/platform"
	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/conf"
	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/service"
)

func middlewares(logger *slog.Logger) []middleware.Middleware {
	return []middleware.Middleware{
		recovery.Recovery(recovery.WithLogger(logger)),
		metadata.Server(),
		logging.Server(logger),
	}
}

// NewHTTPServer publica la API REST y los endpoints de salud.
func NewHTTPServer(c conf.Listener, svc *service.OrdersService, health *platform.Health, logger *slog.Logger) *http.Server {
	opts := append([]http.ServerOption{
		http.Address(c.Addr),
		http.Timeout(c.Timeout.Std()),
		http.Middleware(middlewares(logger)...),
	}, httpcodec.ServerOptions()...)
	srv := http.NewServer(opts...)
	srv.HandleFunc("/livez", health.LivenessHandler())
	srv.HandleFunc("/readyz", health.ReadinessHandler())
	ordersv1.RegisterOrdersServiceHTTPServer(srv, svc)
	return srv
}

// NewGRPCServer publica la API de Orders por gRPC.
func NewGRPCServer(c conf.Listener, svc *service.OrdersService, logger *slog.Logger) *grpc.Server {
	srv := grpc.NewServer(
		grpc.Address(c.Addr),
		grpc.Timeout(c.Timeout.Std()),
		grpc.Middleware(middlewares(logger)...),
	)
	ordersv1.RegisterOrdersServiceServer(srv, svc)
	return srv
}

// NewInventoryConn crea la conexión gRPC hacia Inventory. El timeout se aplica a
// cada llamada; si el contexto entrante ya tiene un deadline más corto, gRPC
// respeta el menor de los dos.
func NewInventoryConn(ctx context.Context, c conf.Inventory, logger *slog.Logger) (*ggrpc.ClientConn, error) {
	return grpc.NewClient(ctx,
		grpc.WithEndpoint(c.Endpoint),
		grpc.WithTimeout(c.Timeout.Std()),
		grpc.WithMiddleware(
			metadata.Client(),
			logging.Client(logger),
		),
	)
}
