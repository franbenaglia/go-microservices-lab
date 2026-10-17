// Package resilience construye el timeout, los reintentos y el circuit breaker
// del cliente gRPC de Orders hacia Inventory, y permite reconfigurarlos en
// caliente desde la configuración dinámica (Consul KV).
package resilience

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/middleware/circuitbreaker"

	"github.com/franbenaglia/go-microservices-lab/pkg/resilience/breaker"
	"github.com/franbenaglia/go-microservices-lab/pkg/resilience/retry"
	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/conf"
)

// Resilience agrupa timeout, retrier y breaker del cliente de Inventory.
type Resilience struct {
	kind         string // classic | sre: requiere reinicio
	logger       *slog.Logger
	onTransition func(from, to string)

	mu       sync.Mutex // serializa Update
	settings atomic.Pointer[conf.DynamicResilience]
	timeout  atomic.Int64 // presupuesto total por llamada (ns)
	retrier  *retry.Retrier
	breaker  switchable
}

// New construye la resiliencia y devuelve los middlewares en el orden en que
// deben encadenarse: retry (fuera) → circuit breaker (dentro). El timeout total
// se obtiene aparte con DeadlineMiddleware, porque debe envolver toda la cadena.
//
// onTransition (opcional) se invoca en cada cambio de estado del breaker (métricas).
func New(c conf.Resilience, callTimeout time.Duration, logger *slog.Logger, onTransition func(from, to string)) (*Resilience, []middleware.Middleware, error) {
	r := &Resilience{kind: c.Breaker.Kind, logger: logger, onTransition: onTransition}
	retrier, err := retry.New(retryPolicy(c.Retry), logger)
	if err != nil {
		return nil, nil, fmt.Errorf("retry: %w", err)
	}
	r.retrier = retrier
	r.timeout.Store(int64(callTimeout))
	r.settings.Store(&conf.DynamicResilience{Breaker: c.Breaker.BreakerParams, Retry: c.Retry})
	mws := []middleware.Middleware{retrier.Middleware()}

	switch c.Breaker.Kind {
	case "sre":
		// Breaker por defecto de Kratos: no expone su estado ni sus opciones
		// (son internas), así que no es reconfigurable en caliente.
		if c.Breaker.Enabled {
			mws = append(mws, circuitbreaker.Client())
		}
	default:
		b, err := r.newBreaker(c.Breaker.BreakerParams)
		if err != nil {
			return nil, nil, err
		}
		r.breaker.cur.Store(b)
		r.breaker.enabled.Store(c.Breaker.Enabled)
		// Un único breaker para toda la dependencia: la fábrica devuelve
		// siempre el mismo envoltorio, que delega en el breaker vigente.
		mws = append(mws, circuitbreaker.Client(circuitbreaker.WithBreakerFactory(
			func() circuitbreaker.CircuitBreaker { return &r.breaker })))
	}
	return r, mws, nil
}

func retryPolicy(c conf.Retry) retry.Policy {
	return retry.Policy{
		Enabled:        c.Enabled,
		MaxAttempts:    c.MaxAttempts,
		BaseBackoff:    c.BaseBackoff.Std(),
		MaxBackoff:     c.MaxBackoff.Std(),
		AttemptTimeout: c.AttemptTimeout.Std(),
		Idempotent:     c.Idempotent,
		KeyRequired:    c.KeyRequired,
	}
}

func breakerSettings(c conf.BreakerParams) breaker.Settings {
	return breaker.Settings{
		Name:                "inventory-service",
		FailureRatio:        c.FailureRatio,
		MinRequests:         c.MinRequests,
		ConsecutiveFailures: c.ConsecutiveFailures,
		Window:              c.Window.Std(),
		OpenTimeout:         c.OpenTimeout.Std(),
		HalfOpenMaxRequests: c.HalfOpenMaxRequests,
	}
}

func (r *Resilience) newBreaker(c conf.BreakerParams) (*breaker.Breaker, error) {
	return breaker.New(breakerSettings(c), breaker.OnStateChange(func(from, to string) {
		r.logger.Warn("circuit breaker: cambio de estado",
			slog.String("breaker", "inventory-service"), slog.String("from", from), slog.String("to", to))
		if r.onTransition != nil {
			r.onTransition(from, to)
		}
	}))
}

// Validate comprueba una configuración nueva sin aplicarla.
func (r *Resilience) Validate(c conf.DynamicResilience, callTimeout time.Duration) error {
	if r.kind == "sre" && c.Breaker.Enabled != r.settings.Load().Breaker.Enabled {
		return fmt.Errorf("con kind=sre, resilience.breaker.enabled requiere reinicio")
	}
	if err := retryPolicy(c.Retry).Validate(); err != nil {
		return fmt.Errorf("retry: %w", err)
	}
	if c.Retry.Enabled && c.Retry.AttemptTimeout.Std() >= callTimeout {
		return fmt.Errorf("retry.attempt_timeout (%s) debe ser menor que inventory.timeout (%s)",
			c.Retry.AttemptTimeout.Std(), callTimeout)
	}
	if r.kind == "classic" {
		if err := breakerSettings(c.Breaker).Validate(); err != nil {
			return fmt.Errorf("breaker: %w", err)
		}
	}
	return nil
}

// Update aplica una configuración ya validada. El breaker solo se recrea si
// cambian sus parámetros (arranca en CLOSED y pierde el historial); un cambio
// de otro parámetro no altera su estado.
func (r *Resilience) Update(c conf.DynamicResilience, callTimeout time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	prev := *r.settings.Load()
	r.timeout.Store(int64(callTimeout))
	if err := r.retrier.SetPolicy(retryPolicy(c.Retry)); err != nil {
		return err
	}
	if r.kind == "classic" {
		if !reflect.DeepEqual(breakerSettings(prev.Breaker), breakerSettings(c.Breaker)) {
			b, err := r.newBreaker(c.Breaker)
			if err != nil {
				return err
			}
			r.breaker.cur.Store(b)
			r.logger.Warn("circuit breaker reconfigurado: nuevo breaker en estado closed")
		}
		r.breaker.enabled.Store(c.Breaker.Enabled)
	}
	r.settings.Store(&c)
	return nil
}

// Settings devuelve la configuración vigente (incluido kind, fijo desde el arranque).
func (r *Resilience) Settings() conf.Resilience {
	d := r.settings.Load()
	return conf.Resilience{Breaker: conf.Breaker{Kind: r.kind, BreakerParams: d.Breaker}, Retry: d.Retry}
}

// CallTimeout devuelve el presupuesto total vigente por llamada.
func (r *Resilience) CallTimeout() time.Duration { return time.Duration(r.timeout.Load()) }

// Retrier devuelve el middleware de reintentos (estadísticas y política).
func (r *Resilience) Retrier() *retry.Retrier { return r.retrier }

// Breaker devuelve el breaker clásico vigente, o nil si está desactivado o es sre.
func (r *Resilience) Breaker() *breaker.Breaker {
	if r.kind != "classic" || !r.breaker.enabled.Load() {
		return nil
	}
	return r.breaker.cur.Load()
}

// DeadlineMiddleware aplica el presupuesto total por llamada (dinámico). Debe
// ser el primer middleware del cliente: envuelve trazas, métricas, reintentos
// y breaker, igual que hacía el WithTimeout fijo de Kratos.
func (r *Resilience) DeadlineMiddleware() middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			if d := r.CallTimeout(); d > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, d)
				defer cancel()
			}
			return next(ctx, req)
		}
	}
}

// switchable implementa circuitbreaker.CircuitBreaker delegando en el breaker
// vigente; desactivado, deja pasar todo y no cuenta nada. Si el breaker se
// sustituye con peticiones en vuelo, sus resultados llegan al breaker nuevo,
// que los descarta por no tener callbacks pendientes (imprecisión acotada al
// instante del cambio).
type switchable struct {
	cur     atomic.Pointer[breaker.Breaker]
	enabled atomic.Bool
}

func (s *switchable) Allow() error {
	if !s.enabled.Load() {
		return nil
	}
	return s.cur.Load().Allow()
}

func (s *switchable) MarkSuccess() {
	if s.enabled.Load() {
		s.cur.Load().MarkSuccess()
	}
}

func (s *switchable) MarkFailed() {
	if s.enabled.Load() {
		s.cur.Load().MarkFailed()
	}
}
