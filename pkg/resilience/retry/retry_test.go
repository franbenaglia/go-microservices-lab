package retry

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware/circuitbreaker"
	"github.com/go-kratos/kratos/v3/transport"
)

type tr struct{ op string }

func (t tr) Kind() transport.Kind            { return transport.KindGRPC }
func (t tr) Endpoint() string                { return "" }
func (t tr) Operation() string               { return t.op }
func (t tr) RequestHeader() transport.Header { return nil }
func (t tr) ReplyHeader() transport.Header   { return nil }

type keyed struct{ key string }

func (k keyed) GetIdempotencyKey() string { return k.key }

func policy() Policy {
	return Policy{
		Enabled: true, MaxAttempts: 3, BaseBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond,
		Idempotent: []string{"GetProduct"}, KeyRequired: []string{"ReserveStock"},
	}
}

func opCtx(op string) context.Context {
	return transport.NewClientContext(context.Background(), tr{op: "/inventory.v1.InventoryService/" + op})
}

// handler falla con err las primeras `fails` veces.
func handler(fails int32, err error, calls *atomic.Int32) func(context.Context, any) (any, error) {
	return func(context.Context, any) (any, error) {
		if calls.Add(1) <= fails {
			return nil, err
		}
		return "ok", nil
	}
}

func run(t *testing.T, p Policy, ctx context.Context, req any, fails int32, err error) (int32, error, Stats) {
	t.Helper()
	r, e := New(p, nil)
	if e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int32
	_, got := r.Middleware()(handler(fails, err, &calls))(ctx, req)
	return calls.Load(), got, r.Stats()
}

var unavailable = kerrors.ServiceUnavailable("UNAVAILABLE", "x")

func TestRetriesTransientErrors(t *testing.T) {
	calls, err, st := run(t, policy(), opCtx("GetProduct"), nil, 2, unavailable)
	if err != nil || calls != 3 || st.Retries != 2 || st.Recovered != 1 {
		t.Fatalf("calls=%d err=%v stats=%+v", calls, err, st)
	}
}

func TestStopsAtMaxAttempts(t *testing.T) {
	calls, err, st := run(t, policy(), opCtx("GetProduct"), nil, 10, unavailable)
	if err == nil || calls != 3 || st.Exhausted != 1 {
		t.Fatalf("calls=%d err=%v stats=%+v", calls, err, st)
	}
}

func TestDoesNotRetryBusinessErrors(t *testing.T) {
	calls, err, _ := run(t, policy(), opCtx("GetProduct"), nil, 10, kerrors.NotFound("PRODUCT_NOT_FOUND", "x"))
	if err == nil || calls != 1 {
		t.Fatalf("un 404 no se reintenta: calls=%d", calls)
	}
}

func TestDoesNotRetryOpenCircuit(t *testing.T) {
	calls, _, _ := run(t, policy(), opCtx("GetProduct"), nil, 10, circuitbreaker.ErrNotAllowed)
	if calls != 1 {
		t.Fatalf("con el circuito abierto no se insiste: calls=%d", calls)
	}
}

func TestUnsafeOperationsNotRetried(t *testing.T) {
	calls, _, st := run(t, policy(), opCtx("CreateProduct"), nil, 10, unavailable)
	if calls != 1 || st.NotRetryable != 1 {
		t.Fatalf("operación no idempotente: calls=%d stats=%+v", calls, st)
	}
}

func TestKeyRequiredOperations(t *testing.T) {
	if calls, _, _ := run(t, policy(), opCtx("ReserveStock"), keyed{}, 10, unavailable); calls != 1 {
		t.Fatalf("ReserveStock sin clave no se reintenta: calls=%d", calls)
	}
	if calls, err, _ := run(t, policy(), opCtx("ReserveStock"), keyed{key: "order:1"}, 1, unavailable); calls != 2 || err != nil {
		t.Fatalf("ReserveStock con clave sí: calls=%d err=%v", calls, err)
	}
}

func TestDisabled(t *testing.T) {
	p := policy()
	p.Enabled = false
	if calls, _, _ := run(t, p, opCtx("GetProduct"), nil, 10, unavailable); calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}

// No se inicia un reintento si la espera no cabe en el deadline original.
func TestRespectsDeadline(t *testing.T) {
	p := policy()
	p.MaxAttempts, p.BaseBackoff, p.MaxBackoff = 10, 50*time.Millisecond, 50*time.Millisecond
	ctx, cancel := context.WithTimeout(opCtx("GetProduct"), 120*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err, st := run(t, p, ctx, nil, 100, unavailable)
	if err == nil || st.Exhausted != 1 {
		t.Fatalf("err=%v stats=%+v", err, st)
	}
	if el := time.Since(start); el > 130*time.Millisecond {
		t.Fatalf("se pasó del deadline: %s", el)
	}
}

// Cada intento tiene su propio timeout; un intento lento se corta y se reintenta.
func TestAttemptTimeout(t *testing.T) {
	p := policy()
	p.AttemptTimeout = 30 * time.Millisecond
	r, _ := New(p, nil)
	var calls atomic.Int32
	slowThenFast := func(ctx context.Context, _ any) (any, error) {
		if calls.Add(1) == 1 {
			<-ctx.Done()
			return nil, kerrors.GatewayTimeout("DEADLINE_EXCEEDED", "intento lento")
		}
		return "ok", nil
	}
	start := time.Now()
	if _, err := r.Middleware()(slowThenFast)(opCtx("GetProduct"), nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("calls=%d en %s", calls.Load(), time.Since(start))
	}
}

func TestBackoffBounds(t *testing.T) {
	p := Policy{BaseBackoff: 10 * time.Millisecond, MaxBackoff: 40 * time.Millisecond}
	for n := 1; n <= 10; n++ {
		ceil := min(p.BaseBackoff<<(n-1), p.MaxBackoff)
		for range 50 {
			if d := p.Backoff(n); d < 0 || d > ceil {
				t.Fatalf("backoff(%d)=%s fuera de [0,%s]", n, d, ceil)
			}
		}
	}
}

func TestValidate(t *testing.T) {
	bad := []Policy{{MaxAttempts: 0, BaseBackoff: 1, MaxBackoff: 1}, {MaxAttempts: 3}, {MaxAttempts: 3, BaseBackoff: 10, MaxBackoff: 5}}
	for i, p := range bad {
		if p.Validate() == nil {
			t.Errorf("caso %d debería ser inválido", i)
		}
	}
}

// La política se cambia en caliente sin recrear el middleware.
func TestSetPolicyHot(t *testing.T) {
	r, err := New(policy(), nil)
	if err != nil {
		t.Fatal(err)
	}
	mw := r.Middleware()
	var calls atomic.Int32
	h := mw(handler(10, unavailable, &calls))
	_, _ = h(opCtx("GetProduct"), nil)
	if calls.Load() != 3 {
		t.Fatalf("con 3 intentos: %d", calls.Load())
	}
	p := policy()
	p.MaxAttempts = 5
	if err := r.SetPolicy(p); err != nil {
		t.Fatal(err)
	}
	calls.Store(0)
	_, _ = h(opCtx("GetProduct"), nil)
	if calls.Load() != 5 {
		t.Fatalf("tras SetPolicy(5): %d", calls.Load())
	}
	p.MaxAttempts = 0
	if r.SetPolicy(p) == nil || r.Policy().MaxAttempts != 5 {
		t.Fatal("una política inválida se rechaza y se conserva la anterior")
	}
}
