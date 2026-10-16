package telemetry

import (
	"context"
	"testing"

	"github.com/go-kratos/kratos/v3/registry"
	"github.com/go-kratos/kratos/v3/selector"
	"github.com/go-kratos/kratos/v3/transport"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type header map[string]string

func (h header) Get(k string) string      { return h[k] }
func (h header) Set(k, v string)          { h[k] = v }
func (h header) Add(k, v string)          { h[k] = v }
func (h header) Keys() []string           { return nil }
func (h header) Values(k string) []string { return []string{h[k]} }

type tr struct {
	op         string
	req, reply header
}

func (t tr) Kind() transport.Kind            { return transport.KindGRPC }
func (t tr) Endpoint() string                { return "" }
func (t tr) Operation() string               { return t.op }
func (t tr) RequestHeader() transport.Header { return t.req }
func (t tr) ReplyHeader() transport.Header   { return t.reply }

func newTel(t *testing.T) (*Telemetry, *tracetest.InMemoryExporter, *sdkmetric.ManualReader) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return &Telemetry{Tracer: tp.Tracer("t"), Meter: mp.Meter("t")}, exp, reader
}

// El ID de traza del span de servidor se devuelve en la cabecera X-Trace-Id.
func TestServerReturnsTraceID(t *testing.T) {
	tel, _, _ := newTel(t)
	mws, err := tel.ServerMiddlewares()
	if err != nil {
		t.Fatal(err)
	}
	// Se aplica solo traceIDReply sobre un contexto con span activo.
	reply := header{}
	ctx := transport.NewServerContext(context.Background(), tr{op: "/x.v1.S/M", req: header{}, reply: reply})
	ctx, span := tel.Tracer.Start(ctx, "server")
	defer span.End()
	_, err = mws[1](func(context.Context, any) (any, error) { return nil, nil })(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := reply[TraceIDHeader], span.SpanContext().TraceID().String(); got != want {
		t.Fatalf("X-Trace-Id = %q, want %q", got, want)
	}
}

// El contador por instancia usa el nodo elegido por el balanceador.
func TestInstanceCounter(t *testing.T) {
	tel, _, reader := newTel(t)
	mws, err := tel.ClientMiddlewares()
	if err != nil {
		t.Fatal(err)
	}
	counter := mws[2]
	ctx := transport.NewClientContext(context.Background(), tr{op: "/inventory.v1.InventoryService/Diagnose", req: header{}, reply: header{}})
	p := &selector.Peer{}
	ctx = selector.NewPeerContext(ctx, p)
	handler := func(context.Context, any) (any, error) {
		// Lo que hace el selector de Kratos al elegir nodo.
		p.Node = selector.NewNode("grpc", "10.0.0.1:9000",
			&registry.ServiceInstance{Name: "inventory-service", Metadata: map[string]string{"instance": "inv-1"}})
		return "ok", nil
	}
	for range 3 {
		if _, err := counter(handler)(ctx, nil); err != nil {
			t.Fatal(err)
		}
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "client_requests_by_instance_total" {
				continue
			}
			dp := m.Data.(metricdata.Sum[int64]).DataPoints[0]
			inst, _ := dp.Attributes.Value(attribute.Key("target_instance"))
			svc, _ := dp.Attributes.Value(attribute.Key("target_service"))
			if dp.Value != 3 || inst.AsString() != "inv-1" || svc.AsString() != "inventory.v1.InventoryService" {
				t.Fatalf("punto inesperado: valor=%d instance=%s service=%s", dp.Value, inst.AsString(), svc.AsString())
			}
			return
		}
	}
	t.Fatal("no se registró client_requests_by_instance_total")
}

// El middleware de tracing de cliente inyecta traceparent (W3C) en la petición.
func TestClientPropagatesW3C(t *testing.T) {
	tel, _, _ := newTel(t)
	if _, err := Setup(context.Background(), Config{}, testID); err != nil { // fija el propagador W3C
		t.Fatal(err)
	}
	mws, err := tel.ClientMiddlewares()
	if err != nil {
		t.Fatal(err)
	}
	req := header{}
	ctx := transport.NewClientContext(context.Background(), tr{op: "/inventory.v1.InventoryService/GetProduct", req: req, reply: header{}})
	ctx, parent := tel.Tracer.Start(ctx, "order.create")
	defer parent.End()
	_, _ = mws[0](func(context.Context, any) (any, error) { return nil, nil })(ctx, nil)
	if tp := req["traceparent"]; len(tp) != 55 || tp[3:35] != parent.SpanContext().TraceID().String() {
		t.Fatalf("traceparent = %q (trace %s)", tp, parent.SpanContext().TraceID())
	}
}
