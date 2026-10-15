// Package breaker adapta sony/gobreaker (circuit breaker clásico de tres
// estados: CLOSED, OPEN y HALF-OPEN) a la interfaz que espera el middleware
// oficial de Kratos v3:
//
//	circuitbreaker.Client(circuitbreaker.WithBreakerFactory(...))
//
// La interfaz de Kratos (Allow / MarkSuccess / MarkFailed) no asocia el
// resultado con la petición que lo produjo, mientras que gobreaker devuelve un
// callback por petición. El adaptador guarda esos callbacks en un pool y los
// consume al llegar cada resultado. Los callbacks son intercambiables: solo
// difieren en la generación y el bucket de la ventana en que se crearon, así
// que la imprecisión queda acotada a las peticiones en vuelo durante un cambio
// de estado.
//
// Además, el middleware de Kratos llama a MarkFailed cuando el breaker rechaza
// una petición (para el breaker SRE, eso eleva la tasa de rechazo). En un
// breaker clásico, ese fallo no debe contar, y el adaptador lo descarta.
package breaker

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sony/gobreaker/v2"
)

// Settings configura el breaker.
type Settings struct {
	Name string
	// FailureRatio abre el circuito si, con al menos MinRequests peticiones en
	// la ventana, la proporción de fallos es >= FailureRatio (0 lo desactiva).
	FailureRatio float64
	MinRequests  uint32
	// ConsecutiveFailures abre el circuito tras N fallos seguidos (0 lo desactiva).
	ConsecutiveFailures uint32
	// Window es la ventana deslizante de evaluación en CLOSED (10 buckets).
	Window time.Duration
	// OpenTimeout es el tiempo en OPEN antes de pasar a HALF-OPEN.
	OpenTimeout time.Duration
	// HalfOpenMaxRequests es el número de peticiones de prueba en HALF-OPEN;
	// si todas tienen éxito, el circuito se cierra.
	HalfOpenMaxRequests uint32
}

// Validate comprueba los valores.
func (s Settings) Validate() error {
	var errs []error
	if s.FailureRatio < 0 || s.FailureRatio > 1 {
		errs = append(errs, errors.New("failure_ratio debe estar entre 0 y 1"))
	}
	if s.FailureRatio == 0 && s.ConsecutiveFailures == 0 {
		errs = append(errs, errors.New("hay que definir failure_ratio o consecutive_failures"))
	}
	if s.FailureRatio > 0 && s.MinRequests == 0 {
		errs = append(errs, errors.New("min_requests debe ser > 0 si se usa failure_ratio"))
	}
	if s.Window < time.Second {
		errs = append(errs, errors.New("window debe ser >= 1s"))
	}
	if s.OpenTimeout < 100*time.Millisecond {
		errs = append(errs, errors.New("open_timeout debe ser >= 100ms"))
	}
	if s.HalfOpenMaxRequests == 0 {
		errs = append(errs, errors.New("half_open_max_requests debe ser > 0"))
	}
	return errors.Join(errs...)
}

// errFailed es el error que se pasa a gobreaker para registrar un fallo.
var errFailed = errors.New("fallo de la llamada")

// Transition es un cambio de estado.
type Transition struct {
	From string    `json:"from"`
	To   string    `json:"to"`
	At   time.Time `json:"at"`
}

// Snapshot es una foto del breaker para diagnóstico.
type Snapshot struct {
	Name                 string
	State                string
	Requests             uint32
	TotalSuccesses       uint32
	TotalFailures        uint32
	ConsecutiveSuccesses uint32
	ConsecutiveFailures  uint32
	// Acumulados desde el arranque.
	Allowed     uint64
	Rejected    uint64
	Successes   uint64
	Failures    uint64
	Transitions []Transition
	Settings    Settings
}

const maxHistory = 50

// Breaker implementa la interfaz CircuitBreaker de Kratos sobre gobreaker.
type Breaker struct {
	settings Settings
	cb       *gobreaker.TwoStepCircuitBreaker[struct{}]

	mu       sync.Mutex
	pending  []func(error) // callbacks de peticiones permitidas en vuelo
	rejected int           // MarkFailed pendientes de peticiones rechazadas

	histMu  sync.Mutex
	history []Transition

	allowed, rejectedTotal, successes, failures atomic.Uint64

	onChange func(from, to string)
}

// Option configura el breaker.
type Option func(*Breaker)

// OnStateChange registra un callback para cada transición (p. ej. métricas o logs).
// Se ejecuta con el lock interno de gobreaker tomado: no debe bloquear.
func OnStateChange(fn func(from, to string)) Option {
	return func(b *Breaker) { b.onChange = fn }
}

// New crea un breaker. Devuelve error si los ajustes no son válidos.
func New(s Settings, opts ...Option) (*Breaker, error) {
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("circuit breaker: %w", err)
	}
	b := &Breaker{settings: s}
	for _, o := range opts {
		o(b)
	}
	b.cb = gobreaker.NewTwoStepCircuitBreaker[struct{}](gobreaker.Settings{
		Name:         s.Name,
		MaxRequests:  s.HalfOpenMaxRequests,
		Interval:     s.Window,
		BucketPeriod: s.Window / 10,
		Timeout:      s.OpenTimeout,
		ReadyToTrip:  s.readyToTrip,
		OnStateChange: func(_ string, from, to gobreaker.State) {
			b.record(from.String(), to.String())
		},
	})
	return b, nil
}

func (s Settings) readyToTrip(c gobreaker.Counts) bool {
	if s.ConsecutiveFailures > 0 && c.ConsecutiveFailures >= s.ConsecutiveFailures {
		return true
	}
	if s.FailureRatio > 0 && c.Requests >= s.MinRequests {
		return float64(c.TotalFailures)/float64(c.Requests) >= s.FailureRatio
	}
	return false
}

func (b *Breaker) record(from, to string) {
	b.histMu.Lock()
	b.history = append(b.history, Transition{From: from, To: to, At: time.Now()})
	if len(b.history) > maxHistory {
		b.history = b.history[len(b.history)-maxHistory:]
	}
	b.histMu.Unlock()
	if b.onChange != nil {
		b.onChange(from, to)
	}
}

// Allow indica si la petición puede continuar.
func (b *Breaker) Allow() error {
	done, err := b.cb.Allow()
	if err != nil {
		b.mu.Lock()
		b.rejected++
		b.mu.Unlock()
		b.rejectedTotal.Add(1)
		return err
	}
	b.mu.Lock()
	b.pending = append(b.pending, done)
	b.mu.Unlock()
	b.allowed.Add(1)
	return nil
}

// MarkSuccess registra el éxito de una petición permitida.
func (b *Breaker) MarkSuccess() {
	b.successes.Add(1)
	b.finish(nil)
}

// MarkFailed registra un fallo. Si corresponde a una petición rechazada por el
// propio breaker, se descarta.
func (b *Breaker) MarkFailed() {
	b.mu.Lock()
	if b.rejected > 0 {
		b.rejected--
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	b.failures.Add(1)
	b.finish(errFailed)
}

func (b *Breaker) finish(err error) {
	b.mu.Lock()
	n := len(b.pending)
	if n == 0 {
		b.mu.Unlock()
		return
	}
	done := b.pending[n-1]
	b.pending = b.pending[:n-1]
	b.mu.Unlock()
	// Fuera del lock propio: gobreaker toma el suyo y puede llamar a record.
	done(err)
}

// State devuelve "closed", "open" o "half-open".
func (b *Breaker) State() string { return b.cb.State().String() }

// Snapshot devuelve el estado actual y el historial de transiciones.
func (b *Breaker) Snapshot() Snapshot {
	c := b.cb.Counts()
	b.histMu.Lock()
	hist := append([]Transition(nil), b.history...)
	b.histMu.Unlock()
	return Snapshot{
		Name:                 b.settings.Name,
		State:                b.State(),
		Requests:             c.Requests,
		TotalSuccesses:       c.TotalSuccesses,
		TotalFailures:        c.TotalFailures,
		ConsecutiveSuccesses: c.ConsecutiveSuccesses,
		ConsecutiveFailures:  c.ConsecutiveFailures,
		Allowed:              b.allowed.Load(),
		Rejected:             b.rejectedTotal.Load(),
		Successes:            b.successes.Load(),
		Failures:             b.failures.Load(),
		Transitions:          hist,
		Settings:             b.settings,
	}
}
