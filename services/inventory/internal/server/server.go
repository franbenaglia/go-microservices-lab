// Package server construye los servidores HTTP y gRPC de Inventory.
package server

import (
	"log/slog"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/middleware/logging"
	"github.com/go-kratos/kratos/v3/middleware/metadata"
	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/transport/grpc"
	"github.com/go-kratos/kratos/v3/transport/http"

	inventoryv1 "github.com/franbenaglia/go-microservices-lab/api/gen/go/inventory/v1"
	"github.com/franbenaglia/go-microservices-lab/pkg/httpcodec"
	"github.com/franbenaglia/go-microservices-lab/pkg/platform"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/internal/conf"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/internal/service"
)

func middlewares(logger *slog.Logger) []middleware.Middleware {
	return []middleware.Middleware{
		recovery.Recovery(recovery.WithLogger(logger)),
		metadata.Server(),
		logging.Server(logger),
	}
}

// NewHTTPServer publica la API REST y los endpoints de salud.
func NewHTTPServer(c conf.Listener, svc *service.InventoryService, health *platform.Health, logger *slog.Logger) *http.Server {
	opts := append([]http.ServerOption{
		http.Address(c.Addr),
		http.Timeout(c.Timeout.Std()),
		http.Middleware(middlewares(logger)...),
	}, httpcodec.ServerOptions()...)
	srv := http.NewServer(opts...)
	srv.HandleFunc("/livez", health.LivenessHandler())
	srv.HandleFunc("/readyz", health.ReadinessHandler())
	inventoryv1.RegisterInventoryServiceHTTPServer(srv, svc)
	return srv
}

// NewGRPCServer publica la API interna. Kratos registra grpc.health.v1.
func NewGRPCServer(c conf.Listener, svc *service.InventoryService, logger *slog.Logger) *grpc.Server {
	srv := grpc.NewServer(
		grpc.Address(c.Addr),
		grpc.Timeout(c.Timeout.Std()),
		grpc.Middleware(middlewares(logger)...),
	)
	inventoryv1.RegisterInventoryServiceServer(srv, svc)
	return srv
}
