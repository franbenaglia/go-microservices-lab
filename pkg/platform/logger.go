package platform

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	klog "github.com/go-kratos/kratos/v3/log"
	"go.opentelemetry.io/otel/trace"
)

// sensitiveKeys son claves que nunca deben aparecer en los logs.
var sensitiveKeys = []string{"password", "dsn", "token", "secret", "authorization"}

// Identity identifica una instancia concreta de un servicio.
type Identity struct {
	Service  string
	Instance string
	Version  string
}

// NewLogger crea un logger JSON con los atributos de servicio e instancia y un
// nivel modificable en tiempo de ejecución mediante el LevelVar devuelto.
func NewLogger(id Identity, level string) (*slog.Logger, *slog.LevelVar, error) {
	lv := new(slog.LevelVar)
	if err := SetLogLevel(lv, level); err != nil {
		return nil, nil, err
	}
	handler := klog.NewHandler(
		klog.WithWriter(os.Stdout),
		klog.WithFormat(klog.FormatJSON),
		klog.WithLevel(lv),
		klog.WithFilter(klog.FilterKey(sensitiveKeys...)),
		klog.WithExtractor(traceAttrs),
	)
	logger := slog.New(handler).With(
		slog.String("service", id.Service),
		slog.String("instance", id.Instance),
		slog.String("version", id.Version),
	)
	return logger, lv, nil
}

// SetLogLevel valida y aplica un nivel (debug, info, warn, error).
func SetLogLevel(lv *slog.LevelVar, level string) error {
	var l slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		l = slog.LevelDebug
	case "", "info":
		l = slog.LevelInfo
	case "warn", "warning":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		return fmt.Errorf("nivel de log inválido %q", level)
	}
	lv.Set(l)
	return nil
}

// traceAttrs añade trace_id y span_id a cada log emitido con un contexto que
// tenga un span activo (correlación logs ↔ trazas en Jaeger). Sin span no
// añade nada, a diferencia de tracing.TraceAttrs, que añadiría valores vacíos.
func traceAttrs(ctx context.Context) []slog.Attr {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return nil
	}
	return []slog.Attr{
		slog.String("trace_id", sc.TraceID().String()),
		slog.String("span_id", sc.SpanID().String()),
	}
}
