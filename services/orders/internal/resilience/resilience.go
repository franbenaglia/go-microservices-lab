// Package resilience construye el circuit breaker y los reintentos del cliente
// gRPC de Orders hacia Inventory a partir de la configuración.
package resilience

import (
	"fmt"
	"log/slog"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/middleware/circuitbreaker"

	"github.com/franbenaglia/go-microservices-lab/pkg/resilience/breaker"
	"github.com/franbenaglia/go-microservices-lab/pkg/resilience/retry"
	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/conf"
)

// Resilience agrupa el breaker y el retrier del cliente de Inventory.
type Resilience struct {
	Breaker  *breaker.Breaker // nil si está desactivado o es de tipo sre
	Retrier  *retry.Retrier
	Settings conf.Resilience
}

// New construye los middlewares de resiliencia según la configuración, en el
// orden en que deben encadenarse: retry (fuera) → circuit breaker (dentro).
func New(c conf.Resilience, logger *slog.Logger) (*Resilience, []middleware.Middleware, error) {
	r := &Resilience{Settings: c}
	retrier, err := retry.New(retry.Policy{
		Enabled:        c.Retry.Enabled,
		MaxAttempts:    c.Retry.MaxAttempts,
		BaseBackoff:    c.Retry.BaseBackoff.Std(),
		MaxBackoff:     c.Retry.MaxBackoff.Std(),
		AttemptTimeout: c.Retry.AttemptTimeout.Std(),
		Idempotent:     c.Retry.Idempotent,
		KeyRequired:    c.Retry.KeyRequired,
	}, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("retry: %w", err)
	}
	r.Retrier = retrier
	mws := []middleware.Middleware{retrier.Middleware()}

	if !c.Breaker.Enabled {
		return r, mws, nil
	}
	switch c.Breaker.Kind {
	case "sre":
		// Breaker por defecto de Kratos: no expone su estado.
		mws = append(mws, circuitbreaker.Client())
	default:
		b, err := breaker.New(breaker.Settings{
			Name:                "inventory-service",
			FailureRatio:        c.Breaker.FailureRatio,
			MinRequests:         c.Breaker.MinRequests,
			ConsecutiveFailures: c.Breaker.ConsecutiveFailures,
			Window:              c.Breaker.Window.Std(),
			OpenTimeout:         c.Breaker.OpenTimeout.Std(),
			HalfOpenMaxRequests: c.Breaker.HalfOpenMaxRequests,
		}, breaker.OnStateChange(func(from, to string) {
			logger.Warn("circuit breaker: cambio de estado",
				slog.String("breaker", "inventory-service"), slog.String("from", from), slog.String("to", to))
		}))
		if err != nil {
			return nil, nil, err
		}
		r.Breaker = b
		// Un único breaker para toda la dependencia (inventory-service): la
		// fábrica devuelve siempre la misma instancia en lugar de una por operación.
		mws = append(mws, circuitbreaker.Client(circuitbreaker.WithBreakerFactory(
			func() circuitbreaker.CircuitBreaker { return b })))
	}
	return r, mws, nil
}
