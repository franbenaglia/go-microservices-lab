// Package server construye los servidores HTTP y gRPC de Orders y el cliente
// gRPC hacia Inventory.
package server

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/middleware/logging"
	"github.com/go-kratos/kratos/v3/middleware/metadata"
	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/registry"
	"github.com/go-kratos/kratos/v3/selector"
	"github.com/go-kratos/kratos/v3/selector/p2c"
	"github.com/go-kratos/kratos/v3/selector/random"
	"github.com/go-kratos/kratos/v3/selector/wrr"
	"github.com/go-kratos/kratos/v3/transport/grpc"
	"github.com/go-kratos/kratos/v3/transport/http"
	ggrpc "google.golang.org/grpc"

	ordersv1 "github.com/franbenaglia/go-microservices-lab/api/gen/go/orders/v1"
	"github.com/franbenaglia/go-microservices-lab/pkg/httpcodec"
	"github.com/franbenaglia/go-microservices-lab/pkg/platform"

	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/conf"
	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/service"
)

// serverChain: recovery → telemetry (tracing, métricas) → metadata → logging.
func serverChain(logger *slog.Logger, telemetry []middleware.Middleware) []middleware.Middleware {
	out := []middleware.Middleware{recovery.Recovery(recovery.WithLogger(logger))}
	out = append(out, telemetry...)
	return append(out, metadata.Server(), logging.Server(logger))
}

// NewHTTPServer crea el servidor HTTP con los endpoints de salud. La API REST
// se registra después con ordersv1.RegisterOrdersServiceHTTPServer, para que el
// endpoint (y la URL de readiness) se conozca antes de construir el resto.
func NewHTTPServer(c conf.Listener, health *platform.Health, logger *slog.Logger, telemetry []middleware.Middleware) *http.Server {
	opts := append([]http.ServerOption{
		http.Address(c.Addr),
		http.Timeout(c.Timeout.Std()),
		http.Middleware(serverChain(logger, telemetry)...),
	}, httpcodec.ServerOptions()...)
	srv := http.NewServer(opts...)
	srv.HandleFunc("/livez", health.LivenessHandler())
	srv.HandleFunc("/readyz", health.ReadinessHandler())
	return srv
}

// NewGRPCServer publica la API de Orders por gRPC.
func NewGRPCServer(c conf.Listener, svc *service.OrdersService, logger *slog.Logger, telemetry []middleware.Middleware) *grpc.Server {
	srv := grpc.NewServer(
		grpc.Address(c.Addr),
		grpc.Timeout(c.Timeout.Std()),
		grpc.Middleware(serverChain(logger, telemetry)...),
	)
	ordersv1.RegisterOrdersServiceServer(srv, svc)
	return srv
}

// SetBalancer fija el selector global de Kratos para los clientes gRPC del
// proceso. Debe llamarse antes de crear las conexiones.
//
//   - wrr: weighted round robin (algoritmo de nginx); respeta el metadato
//     "weight" de cada instancia (100 por defecto).
//   - p2c: power of two choices; elige entre dos nodos al azar el de menor
//     carga estimada (EWMA de latencia, peticiones en curso y errores).
//   - random: aleatorio uniforme.
//
// Nota: el balancer gRPC de Kratos captura el selector global en el init()
// del paquete transport/grpc. Funciona porque transport/http (que también
// importamos) fija wrr antes; SetGlobalSelector cambia el builder envuelto.
func SetBalancer(name string) error {
	switch name {
	case "wrr":
		selector.SetGlobalSelector(wrr.NewBuilder())
	case "p2c":
		selector.SetGlobalSelector(p2c.NewBuilder())
	case "random":
		selector.SetGlobalSelector(random.NewBuilder())
	default:
		return fmt.Errorf("balancer desconocido %q", name)
	}
	return nil
}

// NewInventoryConn crea la conexión gRPC hacia Inventory. El timeout se aplica a
// cada llamada; si el contexto entrante ya tiene un deadline más corto, gRPC
// respeta el menor de los dos.
//
// Con un endpoint "discovery:///inventory-service", el resolver de Kratos
// consulta Consul (disc) y mantiene la lista de instancias sanas; el balanceo
// entre ellas lo hace el cliente (por defecto, weighted round robin).
//
// Orden de los middlewares (de fuera hacia dentro):
//
//	[telemetría: tracing → métricas → instancia] → metadata → logging →
//	[resiliencia: retry → circuit breaker] → invocación gRPC
//
// El timeout de Kratos (WithTimeout) envuelve toda la cadena: es el
// presupuesto total de la llamada, reintentos incluidos.
//
// El timeout total no se fija con grpc.WithTimeout (sería inmutable): lo aplica
// deadline, el primer middleware, con el valor vigente de la configuración dinámica.
func NewInventoryConn(ctx context.Context, c conf.Inventory, disc registry.Discovery, logger *slog.Logger, deadline middleware.Middleware, telemetry, resilience []middleware.Middleware) (*ggrpc.ClientConn, error) {
	mws := append([]middleware.Middleware{deadline}, telemetry...)
	mws = append(mws, metadata.Client(), logging.Client(logger))
	mws = append(mws, resilience...)
	opts := []grpc.ClientOption{
		grpc.WithEndpoint(c.Endpoint),
		grpc.WithTimeout(0), // sin timeout fijo de Kratos (por defecto 2s): lo aplica deadline
		grpc.WithMiddleware(mws...),
	}
	if disc != nil {
		opts = append(opts, grpc.WithDiscovery(disc))
	}
	return grpc.NewClient(ctx, opts...)
}
