// Package retry implementa un middleware cliente de Kratos que reintenta de
// forma controlada solo las operaciones en las que es seguro hacerlo.
//
// Reglas:
//   - Solo se reintentan las operaciones de la lista de idempotentes, o las de la
//     lista "requiere clave" cuando la petición lleva una clave de idempotencia
//     no vacía (GetIdempotencyKey).
//   - Solo se reintentan los errores transitorios: 503 (Unavailable) y 504
//     (timeout del intento). Nunca un rechazo del circuit breaker
//     (reason CIRCUITBREAKER) ni los errores de negocio.
//   - Backoff exponencial con "full jitter": espera = rand[0, min(max, base·2^n)).
//   - Se respeta el deadline del contexto original: no se inicia un reintento si
//     no queda tiempo para la espera más un intento mínimo.
//   - Cada intento puede tener su propio timeout (AttemptTimeout), menor que el
//     presupuesto total de la llamada.
//
// Debe colocarse por fuera del circuit breaker: así cada intento cuenta para el
// breaker, y con el circuito abierto no se insiste.
package retry

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync/atomic"
	"time"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/middleware/circuitbreaker"
	"github.com/go-kratos/kratos/v3/transport"
)

// minAttemptBudget es el tiempo mínimo que debe quedar para lanzar un intento.
const minAttemptBudget = 20 * time.Millisecond

// Policy configura los reintentos.
type Policy struct {
	Enabled        bool
	MaxAttempts    int // intentos totales, incluido el primero
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
	AttemptTimeout time.Duration // 0: sin timeout propio por intento
	// Nombres de método (p. ej. "GetProduct") seguros de reintentar siempre.
	Idempotent []string
	// Métodos que solo se reintentan si la petición lleva clave de idempotencia.
	KeyRequired []string
}

// Validate comprueba los valores.
func (p Policy) Validate() error {
	var errs []error
	if p.MaxAttempts < 1 || p.MaxAttempts > 10 {
		errs = append(errs, errors.New("max_attempts debe estar entre 1 y 10"))
	}
	if p.BaseBackoff <= 0 || p.MaxBackoff < p.BaseBackoff {
		errs = append(errs, errors.New("se requiere 0 < base_backoff <= max_backoff"))
	}
	if p.AttemptTimeout < 0 {
		errs = append(errs, errors.New("attempt_timeout no puede ser negativo"))
	}
	return errors.Join(errs...)
}

// Stats son contadores acumulados desde el arranque.
type Stats struct {
	Calls        uint64 `json:"calls"`         // llamadas lógicas que pasaron por el middleware
	Attempts     uint64 `json:"attempts"`      // intentos totales (incluye los primeros)
	Retries      uint64 `json:"retries"`       // reintentos efectuados
	Recovered    uint64 `json:"recovered"`     // llamadas que fallaron y se recuperaron con un reintento
	Exhausted    uint64 `json:"exhausted"`     // llamadas que agotaron los intentos o el deadline
	NotRetryable uint64 `json:"not_retryable"` // fallos que no se reintentaron por la política
}

// Retrier aplica la política y lleva estadísticas.
type Retrier struct {
	policy      Policy
	idempotent  map[string]bool
	keyRequired map[string]bool
	logger      *slog.Logger

	calls, attempts, retries, recovered, exhausted, notRetryable atomic.Uint64
}

// New crea el retrier.
func New(p Policy, logger *slog.Logger) (*Retrier, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	r := &Retrier{policy: p, idempotent: set(p.Idempotent), keyRequired: set(p.KeyRequired), logger: logger}
	return r, nil
}

func set(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// Policy devuelve la política configurada.
func (r *Retrier) Policy() Policy { return r.policy }

// Stats devuelve los contadores.
func (r *Retrier) Stats() Stats {
	return Stats{
		Calls: r.calls.Load(), Attempts: r.attempts.Load(), Retries: r.retries.Load(),
		Recovered: r.recovered.Load(), Exhausted: r.exhausted.Load(), NotRetryable: r.notRetryable.Load(),
	}
}

type idempotencyKeyer interface{ GetIdempotencyKey() string }

// safe indica si la operación se puede reintentar para esta petición.
func (r *Retrier) safe(operation string, req any) bool {
	method := operation[strings.LastIndex(operation, "/")+1:]
	if r.idempotent[method] {
		return true
	}
	if r.keyRequired[method] {
		k, ok := req.(idempotencyKeyer)
		return ok && k.GetIdempotencyKey() != ""
	}
	return false
}

// retryable indica si el error es transitorio.
func retryable(err error) bool {
	if errors.Is(err, circuitbreaker.ErrNotAllowed) {
		return false
	}
	se := kerrors.FromError(err)
	if se.Reason == circuitbreaker.ErrNotAllowed.Reason {
		return false
	}
	return se.Code == 503 || se.Code == 504
}

// Backoff calcula la espera antes del reintento n (1, 2, ...) con full jitter.
func (p Policy) Backoff(n int) time.Duration {
	ceil := p.BaseBackoff << (n - 1)
	if ceil <= 0 || ceil > p.MaxBackoff {
		ceil = p.MaxBackoff
	}
	return time.Duration(rand.Int64N(int64(ceil) + 1))
}

// Middleware devuelve el middleware cliente.
func (r *Retrier) Middleware() middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			if !r.policy.Enabled {
				return next(ctx, req)
			}
			r.calls.Add(1)
			op := ""
			if tr, ok := transport.FromClientContext(ctx); ok {
				op = tr.Operation()
			}
			var (
				reply any
				err   error
			)
			for attempt := 1; ; attempt++ {
				r.attempts.Add(1)
				reply, err = r.attempt(ctx, next, req)
				if err == nil {
					if attempt > 1 {
						r.recovered.Add(1)
					}
					return reply, nil
				}
				if !retryable(err) || !r.safe(op, req) {
					if retryable(err) {
						r.notRetryable.Add(1)
					}
					return reply, err
				}
				if attempt >= r.policy.MaxAttempts || ctx.Err() != nil {
					r.exhausted.Add(1)
					return reply, err
				}
				wait := r.policy.Backoff(attempt)
				if dl, ok := ctx.Deadline(); ok && time.Until(dl) < wait+minAttemptBudget {
					r.exhausted.Add(1)
					return reply, err // no queda presupuesto: se respeta el deadline original
				}
				r.retries.Add(1)
				if r.logger != nil {
					r.logger.WarnContext(ctx, "reintento",
						slog.String("operation", op), slog.Int("attempt", attempt+1),
						slog.Duration("backoff", wait), slog.String("reason", kerrors.FromError(err).Reason))
				}
				select {
				case <-time.After(wait):
				case <-ctx.Done():
					r.exhausted.Add(1)
					return reply, err
				}
			}
		}
	}
}

func (r *Retrier) attempt(ctx context.Context, next middleware.Handler, req any) (any, error) {
	if r.policy.AttemptTimeout <= 0 {
		return next(ctx, req)
	}
	actx, cancel := context.WithTimeout(ctx, r.policy.AttemptTimeout)
	defer cancel()
	return next(actx, req)
}
