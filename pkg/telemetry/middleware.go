package telemetry

import (
	"context"
	"fmt"

	kmetrics "github.com/go-kratos/kratos/contrib/otel/v3/metrics"
	"github.com/go-kratos/kratos/contrib/otel/v3/tracing"
	kerrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/selector"
	"github.com/go-kratos/kratos/v3/transport"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// TraceIDHeader es la cabecera de respuesta con el ID de traza (para el frontend).
const TraceIDHeader = "X-Trace-Id"

// ServerMiddlewares devuelve tracing y métricas de servidor (contrib/otel de
// Kratos) y añade el ID de traza a las cabeceras de respuesta. Deben ir antes
// que logging, para que los logs lleven trace_id.
func (t *Telemetry) ServerMiddlewares() ([]middleware.Middleware, error) {
	requests, err := kmetrics.DefaultRequestsCounter(t.Meter, kmetrics.DefaultServerRequestsCounterName)
	if err != nil {
		return nil, err
	}
	seconds, err := t.Meter.Float64Histogram(kmetrics.DefaultServerSecondsHistogramName,
		metric.WithUnit("s"), metric.WithDescription("Latencia de las peticiones atendidas"))
	if err != nil {
		return nil, err
	}
	return []middleware.Middleware{
		tracing.Server(),
		traceIDReply(),
		kmetrics.Server(kmetrics.WithRequests(requests), kmetrics.WithSeconds(seconds)),
	}, nil
}

func traceIDReply() middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			if tr, ok := transport.FromServerContext(ctx); ok {
				if id := tracing.TraceID(ctx); id != "" {
					tr.ReplyHeader().Set(TraceIDHeader, id)
				}
			}
			return next(ctx, req)
		}
	}
}

// ClientMiddlewares devuelve tracing y métricas de cliente, más un contador
// por instancia de destino. Van por fuera de retry y breaker: miden la llamada
// lógica; los intentos se cuentan en las métricas de resiliencia.
func (t *Telemetry) ClientMiddlewares() ([]middleware.Middleware, error) {
	requests, err := kmetrics.DefaultRequestsCounter(t.Meter, kmetrics.DefaultClientRequestsCounterName)
	if err != nil {
		return nil, err
	}
	seconds, err := t.Meter.Float64Histogram(kmetrics.DefaultClientSecondsHistogramName,
		metric.WithUnit("s"), metric.WithDescription("Latencia de las llamadas salientes (incluye reintentos)"))
	if err != nil {
		return nil, err
	}
	byInstance, err := t.Meter.Int64Counter("client_requests_by_instance_total",
		metric.WithDescription("Llamadas salientes por instancia de destino que las atendió (último intento)"))
	if err != nil {
		return nil, err
	}
	return []middleware.Middleware{
		tracing.Client(),
		kmetrics.Client(kmetrics.WithRequests(requests), kmetrics.WithSeconds(seconds)),
		instanceCounter(byInstance),
	}, nil
}

// instanceCounter registra qué instancia atendió cada llamada. El ID de
// instancia tiene cardinalidad baja (unas pocas réplicas), a diferencia de los
// IDs de pedido o de usuario, que nunca se usan como etiqueta.
func instanceCounter(c metric.Int64Counter) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			reply, err := next(ctx, req)
			instance := "none"
			if p, ok := selector.FromPeerContext(ctx); ok && p.Node != nil {
				if id := p.Node.Metadata()["instance"]; id != "" {
					instance = id
				}
			}
			code := 200
			if err != nil {
				code = int(kerrors.FromError(err).Code)
			}
			service := ""
			if tr, ok := transport.FromClientContext(ctx); ok {
				service = serviceOf(tr.Operation())
			}
			c.Add(ctx, 1, metric.WithAttributes(
				attribute.String("target_service", service),
				attribute.String("target_instance", instance),
				attribute.String("code", fmt.Sprint(code)),
			))
			return reply, err
		}
	}
}

// serviceOf extrae "inventory.v1.InventoryService" de "/inventory.v1.InventoryService/GetProduct".
func serviceOf(op string) string {
	if len(op) > 0 && op[0] == '/' {
		op = op[1:]
	}
	for i := 0; i < len(op); i++ {
		if op[i] == '/' {
			return op[:i]
		}
	}
	return op
}

// RegisterReadiness publica service_ready (1 = preparada, 0 = no) a partir de
// la función de readiness de la instancia. Se evalúa en cada exportación.
func (t *Telemetry) RegisterReadiness(ready func(context.Context) int64) error {
	g, err := t.Meter.Int64ObservableGauge("service_ready",
		metric.WithDescription("Readiness de la instancia: 1 preparada, 0 no preparada"))
	if err != nil {
		return err
	}
	_, err = t.Meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		o.ObserveInt64(g, ready(ctx))
		return nil
	}, g)
	return err
}
