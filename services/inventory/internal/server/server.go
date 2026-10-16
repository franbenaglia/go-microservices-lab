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

// Middlewares agrupa los middlewares opcionales de los servidores.
type Middlewares struct {
	// Telemetry (tracing y métricas) va justo después de recovery: así el span
	// cubre toda la petición y los logs posteriores llevan trace_id.
	Telemetry []middleware.Middleware
	// Extra (p. ej. la inyección de fallos del laboratorio) va al final, para
	// que logging, métricas y trazas registren sus efectos.
	Extra []middleware.Middleware
}

// chain: recovery → telemetry → metadata → logging → extra.
func (m Middlewares) chain(logger *slog.Logger) []middleware.Middleware {
	out := []middleware.Middleware{recovery.Recovery(recovery.WithLogger(logger))}
	out = append(out, m.Telemetry...)
	out = append(out, metadata.Server(), logging.Server(logger))
	return append(out, m.Extra...)
}

// NewHTTPServer crea el servidor HTTP con los endpoints de salud. La API REST
// se registra después con inventoryv1.RegisterInventoryServiceHTTPServer, para que el
// endpoint (y la URL de readiness) se conozca antes de construir el resto.
func NewHTTPServer(c conf.Listener, health *platform.Health, logger *slog.Logger, mws Middlewares) *http.Server {
	opts := append([]http.ServerOption{
		http.Address(c.Addr),
		http.Timeout(c.Timeout.Std()),
		http.Middleware(mws.chain(logger)...),
	}, httpcodec.ServerOptions()...)
	srv := http.NewServer(opts...)
	srv.HandleFunc("/livez", health.LivenessHandler())
	srv.HandleFunc("/readyz", health.ReadinessHandler())
	return srv
}

// NewGRPCServer publica la API interna. Kratos registra grpc.health.v1.
func NewGRPCServer(c conf.Listener, svc *service.InventoryService, logger *slog.Logger, mws Middlewares) *grpc.Server {
	srv := grpc.NewServer(
		grpc.Address(c.Addr),
		grpc.Timeout(c.Timeout.Std()),
		grpc.Middleware(mws.chain(logger)...),
	)
	inventoryv1.RegisterInventoryServiceServer(srv, svc)
	return srv
}
