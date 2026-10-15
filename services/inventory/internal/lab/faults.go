// Package lab contiene la inyección de fallos de Inventory para los
// experimentos de resiliencia. Está separado de la lógica de negocio, viene
// desactivado por defecto y solo se registra con LAB_MODE=true.
package lab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
)

// Faults describe los fallos a simular. El valor cero no simula nada.
type Faults struct {
	// LatencyMs añade una espera fija a cada petición afectada.
	LatencyMs int `json:"latency_ms"`
	// JitterMs añade una espera aleatoria adicional en [0, JitterMs].
	JitterMs int `json:"jitter_ms"`
	// ErrorRate es la probabilidad (0..1) de responder con ErrorCode.
	ErrorRate float64 `json:"error_rate"`
	// ErrorCode: 500 (Internal) o 503 (Unavailable). Por defecto, 503.
	ErrorCode int `json:"error_code"`
	// Unavailable responde 503 a todas las peticiones afectadas.
	Unavailable bool `json:"unavailable"`
	// FailReadiness hace que /readyz falle, y Consul retira la instancia.
	FailReadiness bool `json:"fail_readiness"`
	// Operations limita los fallos a esos métodos (p. ej. "Diagnose"); vacío = todos.
	Operations []string `json:"operations"`
}

// Active indica si hay algún fallo configurado.
func (f Faults) Active() bool {
	return f.LatencyMs > 0 || f.JitterMs > 0 || f.ErrorRate > 0 || f.Unavailable || f.FailReadiness
}

// Validate comprueba los valores.
func (f Faults) Validate() error {
	var errs []error
	if f.LatencyMs < 0 || f.LatencyMs > 30_000 || f.JitterMs < 0 || f.JitterMs > 30_000 {
		errs = append(errs, errors.New("latency_ms y jitter_ms deben estar entre 0 y 30000"))
	}
	if f.ErrorRate < 0 || f.ErrorRate > 1 {
		errs = append(errs, errors.New("error_rate debe estar entre 0 y 1"))
	}
	if f.ErrorCode != 0 && f.ErrorCode != 500 && f.ErrorCode != 503 {
		errs = append(errs, errors.New("error_code debe ser 500 o 503"))
	}
	return errors.Join(errs...)
}

// Injector guarda la configuración activa. Las lecturas y escrituras son
// atómicas (atomic.Pointer), sin bloqueos en el camino de cada petición.
type Injector struct {
	cur      atomic.Pointer[Faults]
	instance string
	injected atomic.Uint64
}

// NewInjector crea un inyector sin fallos.
func NewInjector(instance string) *Injector {
	i := &Injector{instance: instance}
	i.cur.Store(&Faults{})
	return i
}

// Get devuelve la configuración actual.
func (i *Injector) Get() Faults { return *i.cur.Load() }

// Set valida y aplica una configuración nueva.
func (i *Injector) Set(f Faults) error {
	if err := f.Validate(); err != nil {
		return err
	}
	if f.ErrorCode == 0 {
		f.ErrorCode = 503
	}
	f.Operations = slices.Clone(f.Operations)
	i.cur.Store(&f)
	return nil
}

func (f *Faults) applies(operation string) bool {
	if len(f.Operations) == 0 {
		return true
	}
	method := operation[strings.LastIndex(operation, "/")+1:]
	return slices.Contains(f.Operations, method)
}

// Middleware inyecta los fallos en el servidor (gRPC y HTTP). No afecta a
// /livez, /readyz ni /admin, que no pasan por los middlewares de Kratos.
func (i *Injector) Middleware() middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			f := i.cur.Load()
			if !f.Active() {
				return next(ctx, req)
			}
			op := ""
			if tr, ok := transport.FromServerContext(ctx); ok {
				op = tr.Operation()
			}
			if !f.applies(op) {
				return next(ctx, req)
			}
			if d := f.delay(); d > 0 {
				select {
				case <-time.After(d):
				case <-ctx.Done():
					return nil, kerrors.GatewayTimeout("DEADLINE_EXCEEDED", "cancelado durante la latencia simulada")
				}
			}
			if f.Unavailable {
				i.injected.Add(1)
				return nil, kerrors.ServiceUnavailable("FAULT_INJECTED", "indisponibilidad simulada en "+i.instance)
			}
			if f.ErrorRate > 0 && rand.Float64() < f.ErrorRate {
				i.injected.Add(1)
				if f.ErrorCode == 500 {
					return nil, kerrors.InternalServer("FAULT_INJECTED", "error simulado en "+i.instance)
				}
				return nil, kerrors.ServiceUnavailable("FAULT_INJECTED", "error transitorio simulado en "+i.instance)
			}
			return next(ctx, req)
		}
	}
}

func (f *Faults) delay() time.Duration {
	d := time.Duration(f.LatencyMs) * time.Millisecond
	if f.JitterMs > 0 {
		d += time.Duration(rand.IntN(f.JitterMs+1)) * time.Millisecond
	}
	return d
}

// ReadinessCheck falla si se simula una instancia no preparada.
func (i *Injector) ReadinessCheck(context.Context) error {
	if i.cur.Load().FailReadiness {
		return errors.New("readiness fallida simulada (laboratorio)")
	}
	return nil
}

type status struct {
	Instance string `json:"instance"`
	Faults   Faults `json:"faults"`
	Active   bool   `json:"active"`
	Injected uint64 `json:"injected_errors"`
}

// Handler expone /admin/faults: GET devuelve la configuración, PUT la
// reemplaza y DELETE la restablece.
func (i *Injector) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
		case http.MethodPut, http.MethodPost:
			var f Faults
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&f); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":%q}`, "JSON inválido: "+err.Error()), http.StatusBadRequest)
				return
			}
			if err := i.Set(f); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
				return
			}
		case http.MethodDelete:
			_ = i.Set(Faults{})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		f := i.Get()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status{Instance: i.instance, Faults: f, Active: f.Active(), Injected: i.injected.Load()})
	}
}
