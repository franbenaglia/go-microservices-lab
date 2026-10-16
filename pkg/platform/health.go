package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// CheckFunc comprueba una dependencia. Debe respetar la cancelación del contexto.
type CheckFunc func(ctx context.Context) error

// Health distingue entre liveness (el proceso está vivo) y readiness (la
// instancia puede atender tráfico porque sus dependencias responden).
type Health struct {
	timeout  time.Duration
	mu       sync.RWMutex
	checks   map[string]CheckFunc
	draining atomic.Bool
}

// NewHealth crea un Health cuyo timeout se aplica a cada comprobación.
func NewHealth(timeout time.Duration) *Health {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Health{timeout: timeout, checks: map[string]CheckFunc{}}
}

// AddReadinessCheck registra una dependencia necesaria para recibir tráfico.
func (h *Health) AddReadinessCheck(name string, fn CheckFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks[name] = fn
}

// SetDraining marca la instancia como no preparada (apagado ordenado), para
// que deje de recibir tráfico antes de cerrar los servidores.
func (h *Health) SetDraining(v bool) { h.draining.Store(v) }

type healthReport struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// Ready ejecuta todas las comprobaciones en paralelo y devuelve el resultado.
func (h *Health) Ready(ctx context.Context) (bool, map[string]string) {
	h.mu.RLock()
	names := make([]string, 0, len(h.checks))
	for n := range h.checks {
		names = append(names, n)
	}
	h.mu.RUnlock()
	sort.Strings(names)

	results := make(map[string]string, len(names)+1)
	ok := true
	if h.draining.Load() {
		results["draining"] = "true"
		ok = false
	}

	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range names {
		h.mu.RLock()
		fn := h.checks[n]
		h.mu.RUnlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := "ok"
			if err := fn(ctx); err != nil {
				res = err.Error()
			}
			mu.Lock()
			defer mu.Unlock()
			results[n] = res
			if res != "ok" {
				ok = false
			}
		}()
	}
	wg.Wait()
	return ok, results
}

// LivenessHandler responde 200 mientras el proceso pueda atender HTTP.
func (h *Health) LivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, healthReport{Status: "alive"})
	}
}

// ReadinessHandler responde 200 si todas las dependencias están disponibles y
// 503 en caso contrario.
func (h *Health) ReadinessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ok, checks := h.Ready(r.Context())
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, healthReport{Status: "not_ready", Checks: checks})
			return
		}
		writeJSON(w, http.StatusOK, healthReport{Status: "ready", Checks: checks})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// RunHealthcheck consulta url y devuelve un error si no responde 200. Se usa
// como healthcheck de Docker en imágenes sin shell ni curl (distroless).
func RunHealthcheck(url string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck %s: estado %d", url, resp.StatusCode)
	}
	return nil
}

// ReadyFunc devuelve una función que informa (1/0) de si la instancia está
// preparada, para publicarla como métrica.
func (h *Health) ReadyFunc() func(context.Context) int64 {
	return func(ctx context.Context) int64 {
		if ok, _ := h.Ready(ctx); ok {
			return 1
		}
		return 0
	}
}
