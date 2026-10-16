package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/franbenaglia/go-microservices-lab/pkg/platform"
)

// Con endpoint, Setup construye el resource y los proveedores sin errores
// (los exportadores gRPC conectan de forma perezosa).
func TestSetupEnabled(t *testing.T) {
	tel, err := Setup(context.Background(), Config{
		Endpoint: "127.0.0.1:1", SampleRatio: 1, MetricInterval: platform.Duration(time.Minute),
	}, platform.Identity{Service: "test-svc", Instance: "i-1", Version: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := tel.Tracer.Start(context.Background(), "op")
	if !span.SpanContext().IsValid() || !span.SpanContext().IsSampled() {
		t.Fatal("el span debería ser válido y estar muestreado")
	}
	span.End()
	_ = ctx
	sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tel.Shutdown(sctx) // el Collector no existe: el error de exportación es esperable
}

func TestValidate(t *testing.T) {
	if (Config{}).Validate() != nil || (Config{}).Enabled() {
		t.Fatal("sin endpoint: desactivado y válido")
	}
	if (Config{Endpoint: "x:4317", SampleRatio: 2, MetricInterval: platform.Duration(time.Second)}).Validate() == nil {
		t.Fatal("sample_ratio > 1 debe rechazarse")
	}
}

var testID = platform.Identity{Service: "test-svc", Instance: "i-1", Version: "v1"}
