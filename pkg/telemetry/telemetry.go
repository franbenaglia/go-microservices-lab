// Package telemetry configura OpenTelemetry (trazas y métricas) para los
// servicios: exportación OTLP/gRPC al OpenTelemetry Collector, un resource que
// identifica servicio, instancia y versión, y propagación W3C Trace Context.
//
// Flujo: servicio ──OTLP──► Collector ──► Jaeger (trazas)
//
//	└──► exportador Prometheus ◄── scrape de Prometheus (métricas)
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/franbenaglia/go-microservices-lab/pkg/platform"
)

// Config configura la telemetría.
type Config struct {
	// Endpoint OTLP/gRPC del Collector (host:puerto). Vacío desactiva la exportación.
	Endpoint string `json:"endpoint"`
	// SampleRatio es la fracción de trazas que se muestrean (0..1).
	SampleRatio float64 `json:"sample_ratio"`
	// MetricInterval es la frecuencia de exportación de métricas.
	MetricInterval platform.Duration `json:"metric_interval"`
}

// Enabled indica si se exporta telemetría.
func (c Config) Enabled() bool { return c.Endpoint != "" }

// Validate comprueba los valores.
func (c Config) Validate() error {
	if !c.Enabled() {
		return nil
	}
	var errs []error
	if c.SampleRatio < 0 || c.SampleRatio > 1 {
		errs = append(errs, errors.New("telemetry.sample_ratio debe estar entre 0 y 1"))
	}
	if c.MetricInterval.Std() < time.Second {
		errs = append(errs, errors.New("telemetry.metric_interval debe ser >= 1s"))
	}
	return errors.Join(errs...)
}

// Telemetry da acceso al tracer y al meter del servicio.
type Telemetry struct {
	Tracer   trace.Tracer
	Meter    metric.Meter
	shutdown []func(context.Context) error
}

// Shutdown vacía los buffers y cierra los exportadores.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var errs []error
	for _, fn := range t.shutdown {
		errs = append(errs, fn(ctx))
	}
	return errors.Join(errs...)
}

// LatencyBuckets son los límites de los histogramas de latencia (segundos).
// Cubren desde 1ms hasta 5s, más que el presupuesto de 2s de las llamadas y
// la latencia simulada de los experimentos.
var LatencyBuckets = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 3, 5}

// Setup inicializa los proveedores globales de OpenTelemetry. Sin endpoint,
// se usan proveedores no-op (las llamadas a la API no tienen coste) y solo se
// configura la propagación W3C.
func Setup(ctx context.Context, c Config, id platform.Identity) (*Telemetry, error) {
	// W3C Trace Context + Baggage: el formato estándar entre navegador,
	// Traefik y los servicios.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))

	t := &Telemetry{}
	if !c.Enabled() {
		t.Tracer = otel.Tracer(id.Service)
		t.Meter = otel.Meter(id.Service)
		return t, nil
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}

	// Sin schema URL: resource.Merge falla si dos resources declaran versiones
	// de semconv distintas (Default usa la del SDK).
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(id.Service),
		semconv.ServiceInstanceID(id.Instance),
		semconv.ServiceVersion(id.Version),
		attribute.String("deployment.environment.name", "lab"),
	))
	if err != nil {
		return nil, fmt.Errorf("resource: %w", err)
	}

	traceExp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(c.Endpoint), otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("exportador de trazas: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExp, sdktrace.WithBatchTimeout(2*time.Second)),
		// Respeta la decisión de muestreo del llamador (p. ej. Traefik).
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(c.SampleRatio))),
	)
	otel.SetTracerProvider(tp)
	t.shutdown = append(t.shutdown, tp.Shutdown)

	metricExp, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpoint(c.Endpoint), otlpmetricgrpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("exportador de métricas: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(c.MetricInterval.Std()))),
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Kind: sdkmetric.InstrumentKindHistogram, Unit: "s"},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: LatencyBuckets}},
		)),
	)
	otel.SetMeterProvider(mp)
	t.shutdown = append(t.shutdown, mp.Shutdown)

	// Métricas del runtime de Go (memoria, GC, goroutines): recursos del proceso.
	if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
		return nil, fmt.Errorf("métricas de runtime: %w", err)
	}

	t.Tracer = tp.Tracer(id.Service)
	t.Meter = mp.Meter(id.Service)
	return t, nil
}
