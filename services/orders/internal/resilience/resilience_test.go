package resilience

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"

	"github.com/franbenaglia/go-microservices-lab/pkg/platform"
	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/conf"
)

func cfg() conf.Resilience {
	return conf.Resilience{
		Breaker: conf.Breaker{Kind: "classic", BreakerParams: conf.BreakerParams{
			Enabled: true, FailureRatio: 0.6, MinRequests: 10, ConsecutiveFailures: 3,
			Window: platform.Duration(10 * time.Second), OpenTimeout: platform.Duration(5 * time.Second), HalfOpenMaxRequests: 3,
		}},
		Retry: conf.Retry{
			Enabled: false, MaxAttempts: 3, BaseBackoff: platform.Duration(time.Millisecond),
			MaxBackoff: platform.Duration(5 * time.Millisecond), AttemptTimeout: platform.Duration(100 * time.Millisecond),
			Idempotent: []string{"GetProduct"},
		},
	}
}

type tr struct{}

func (tr) Kind() transport.Kind            { return transport.KindGRPC }
func (tr) Endpoint() string                { return "" }
func (tr) Operation() string               { return "/inventory.v1.InventoryService/GetProduct" }
func (tr) RequestHeader() transport.Header { return nil }
func (tr) ReplyHeader() transport.Header   { return nil }

func dyn(c conf.Resilience) conf.DynamicResilience {
	return conf.DynamicResilience{Breaker: c.Breaker.BreakerParams, Retry: c.Retry}
}

func build(t *testing.T) (*Resilience, middleware.Handler) {
	t.Helper()
	r, mws, err := New(cfg(), time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	failing := func(context.Context, any) (any, error) { return nil, kerrors.ServiceUnavailable("UNAVAILABLE", "x") }
	chain := append([]middleware.Middleware{r.DeadlineMiddleware()}, mws...)
	return r, middleware.Chain(chain...)(failing)
}

func call(h middleware.Handler, n int) {
	ctx := transport.NewClientContext(context.Background(), tr{})
	for range n {
		_, _ = h(ctx, nil)
	}
}

func TestBreakerSwapOnlyWhenSettingsChange(t *testing.T) {
	r, h := build(t)
	call(h, 3)
	if r.Breaker().State() != "open" {
		t.Fatalf("tras 3 fallos: %s", r.Breaker().State())
	}
	before := r.Breaker()

	// Cambiar otro parámetro (retry) no recrea el breaker ni altera su estado.
	c := cfg()
	c.Retry.MaxAttempts = 2
	if err := r.Update(dyn(c), time.Second); err != nil {
		t.Fatal(err)
	}
	if r.Breaker() != before || r.Breaker().State() != "open" {
		t.Fatal("el breaker no debe cambiar si sus parámetros no cambian")
	}

	// Cambiar un umbral recrea el breaker en CLOSED.
	c.Breaker.ConsecutiveFailures = 5
	if err := r.Update(dyn(c), time.Second); err != nil {
		t.Fatal(err)
	}
	if r.Breaker() == before || r.Breaker().State() != "closed" {
		t.Fatal("con otros umbrales debe haber un breaker nuevo en closed")
	}
	call(h, 4)
	if r.Breaker().State() != "closed" {
		t.Fatal("con consecutive_failures=5, 4 fallos no abren")
	}
	call(h, 1)
	if r.Breaker().State() != "open" {
		t.Fatal("el quinto fallo abre")
	}
}

func TestBreakerDisableAtRuntime(t *testing.T) {
	r, h := build(t)
	c := cfg()
	c.Breaker.Enabled = false
	if err := r.Update(dyn(c), time.Second); err != nil {
		t.Fatal(err)
	}
	if r.Breaker() != nil {
		t.Fatal("desactivado no debe exponer breaker")
	}
	ctx := transport.NewClientContext(context.Background(), tr{})
	for range 10 {
		if _, err := h(ctx, nil); kerrors.Reason(err) == "CIRCUITBREAKER" {
			t.Fatal("desactivado no debe rechazar")
		}
	}
}

func TestDynamicDeadline(t *testing.T) {
	r, _, err := New(cfg(), time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	deadlineOf := func() time.Duration {
		var got time.Duration
		_, _ = r.DeadlineMiddleware()(func(ctx context.Context, _ any) (any, error) {
			dl, _ := ctx.Deadline()
			got = time.Until(dl)
			return nil, nil
		})(context.Background(), nil)
		return got
	}
	if d := deadlineOf(); d < 900*time.Millisecond || d > time.Second {
		t.Fatalf("deadline inicial %s", d)
	}
	if err := r.Update(dyn(cfg()), 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if d := deadlineOf(); d > 300*time.Millisecond {
		t.Fatalf("tras Update el deadline debe ser 300ms: %s", d)
	}
}

func TestValidate(t *testing.T) {
	r, _, _ := New(cfg(), time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	c := cfg()
	c.Retry.Enabled = true
	if r.Validate(dyn(c), 50*time.Millisecond) == nil {
		t.Fatal("attempt_timeout >= timeout debe rechazarse")
	}
	c = cfg()
	c.Breaker.FailureRatio = 2
	if r.Validate(dyn(c), time.Second) == nil {
		t.Fatal("failure_ratio > 1 debe rechazarse")
	}
	if err := r.Validate(dyn(cfg()), time.Second); err != nil {
		t.Fatal(err)
	}
}

// Reconfigurar mientras hay llamadas en curso no produce carreras (go test -race).
func TestConcurrentUpdates(t *testing.T) {
	r, h := build(t)
	done := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
					call(h, 1)
				}
			}
		})
	}
	for i := range 200 {
		c := cfg()
		c.Breaker.ConsecutiveFailures = uint32(3 + i%5)
		c.Breaker.Enabled = i%3 != 0
		c.Retry.Enabled = i%2 == 0
		if err := r.Update(dyn(c), time.Duration(500+i)*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		_ = r.Settings()
		_ = r.Breaker()
	}
	close(done)
	wg.Wait()
}
