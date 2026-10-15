package breaker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware/circuitbreaker"
	"github.com/go-kratos/kratos/v3/transport"
)

func settings() Settings {
	return Settings{
		Name: "test", ConsecutiveFailures: 3, FailureRatio: 0.5, MinRequests: 10,
		Window: 10 * time.Second, OpenTimeout: 200 * time.Millisecond, HalfOpenMaxRequests: 2,
	}
}

func newB(t *testing.T, s Settings) *Breaker {
	t.Helper()
	b, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// call simula lo que hace el middleware de Kratos con un resultado dado.
func call(b *Breaker, fail bool) error {
	if err := b.Allow(); err != nil {
		b.MarkFailed() // Kratos lo hace también para las rechazadas
		return err
	}
	if fail {
		b.MarkFailed()
	} else {
		b.MarkSuccess()
	}
	return nil
}

func TestValidate(t *testing.T) {
	bad := []Settings{
		{},
		func() Settings { s := settings(); s.FailureRatio = 1.5; return s }(),
		func() Settings { s := settings(); s.Window = 0; return s }(),
		func() Settings { s := settings(); s.HalfOpenMaxRequests = 0; return s }(),
		func() Settings { s := settings(); s.FailureRatio, s.ConsecutiveFailures = 0, 0; return s }(),
	}
	for i, s := range bad {
		if _, err := New(s); err == nil {
			t.Errorf("caso %d: debería ser inválido", i)
		}
	}
}

// CLOSED -> OPEN -> HALF-OPEN -> CLOSED.
func TestFullCycle(t *testing.T) {
	b := newB(t, settings())
	if b.State() != "closed" {
		t.Fatal(b.State())
	}
	for range 3 {
		_ = call(b, true)
	}
	if b.State() != "open" {
		t.Fatalf("tras 3 fallos seguidos: %s", b.State())
	}
	// En OPEN se rechaza localmente y los rechazos no cuentan como fallos.
	for range 5 {
		if err := call(b, false); err == nil {
			t.Fatal("en OPEN debería rechazar")
		}
	}
	if s := b.Snapshot(); s.Rejected != 5 || s.Failures != 3 {
		t.Fatalf("rechazos=%d fallos=%d", s.Rejected, s.Failures)
	}

	time.Sleep(250 * time.Millisecond)
	if b.State() != "half-open" {
		t.Fatalf("tras open_timeout: %s", b.State())
	}
	// HALF-OPEN: 2 peticiones de prueba con éxito cierran el circuito.
	if err := call(b, false); err != nil {
		t.Fatal(err)
	}
	if err := call(b, false); err != nil {
		t.Fatal(err)
	}
	if b.State() != "closed" {
		t.Fatalf("tras 2 pruebas correctas: %s", b.State())
	}
	got := b.Snapshot().Transitions
	want := []string{"closed>open", "open>half-open", "half-open>closed"}
	if len(got) != len(want) {
		t.Fatalf("transiciones %v", got)
	}
	for i, tr := range got {
		if tr.From+">"+tr.To != want[i] {
			t.Fatalf("transición %d = %s>%s", i, tr.From, tr.To)
		}
	}
}

// Un fallo en HALF-OPEN vuelve a abrir el circuito.
func TestHalfOpenFailureReopens(t *testing.T) {
	b := newB(t, settings())
	for range 3 {
		_ = call(b, true)
	}
	time.Sleep(250 * time.Millisecond)
	_ = call(b, true)
	if b.State() != "open" {
		t.Fatalf("got %s", b.State())
	}
}

// En HALF-OPEN solo pasan HalfOpenMaxRequests peticiones simultáneas.
func TestHalfOpenLimitsConcurrentProbes(t *testing.T) {
	b := newB(t, settings())
	for range 3 {
		_ = call(b, true)
	}
	time.Sleep(250 * time.Millisecond)
	if b.Allow() != nil || b.Allow() != nil {
		t.Fatal("las 2 primeras pruebas deben pasar")
	}
	if b.Allow() == nil {
		t.Fatal("la tercera prueba simultánea debe rechazarse")
	}
	b.MarkFailed() // el rechazo
	b.MarkSuccess()
	b.MarkSuccess()
	if b.State() != "closed" {
		t.Fatalf("got %s", b.State())
	}
}

// Umbral por proporción: 50% de fallos con al menos 10 peticiones.
func TestFailureRatio(t *testing.T) {
	s := settings()
	s.ConsecutiveFailures = 0
	b := newB(t, s)
	for i := range 9 {
		_ = call(b, i%2 == 0) // 5 fallos de 9: aún no llega a min_requests
	}
	if b.State() != "closed" {
		t.Fatalf("con 9 peticiones: %s", b.State())
	}
	_ = call(b, true) // 6/10 = 60%
	if b.State() != "open" {
		t.Fatalf("con 60%% de fallos: %s", b.State())
	}
}

// Uso concurrente (para el detector de carreras) y contabilidad coherente.
func TestConcurrentUse(t *testing.T) {
	s := settings()
	s.ConsecutiveFailures, s.FailureRatio, s.MinRequests = 0, 0.99, 1000
	b := newB(t, s)
	var wg sync.WaitGroup
	for i := range 200 {
		wg.Go(func() { _ = call(b, i%10 == 0) })
	}
	wg.Wait()
	snap := b.Snapshot()
	if snap.Allowed != 200 || snap.Successes+snap.Failures != 200 || len(b.pending) != 0 {
		t.Fatalf("contabilidad: %+v pendientes=%d", snap, len(b.pending))
	}
}

type tr struct{ op string }

func (t tr) Kind() transport.Kind            { return transport.KindGRPC }
func (t tr) Endpoint() string                { return "" }
func (t tr) Operation() string               { return t.op }
func (t tr) RequestHeader() transport.Header { return nil }
func (t tr) ReplyHeader() transport.Header   { return nil }

// Integración con el middleware oficial de Kratos: 503 y 504 abren el circuito;
// los errores de negocio (409) no.
func TestWithKratosMiddleware(t *testing.T) {
	b := newB(t, settings())
	mw := circuitbreaker.Client(circuitbreaker.WithBreakerFactory(func() circuitbreaker.CircuitBreaker { return b }))
	ctx := transport.NewClientContext(context.Background(), tr{op: "/inventory.v1.InventoryService/GetProduct"})

	conflict := mw(func(context.Context, any) (any, error) { return nil, kerrors.Conflict("INSUFFICIENT_STOCK", "x") })
	for range 5 {
		_, _ = conflict(ctx, nil)
	}
	if b.State() != "closed" {
		t.Fatal("los errores de negocio no deben abrir el circuito")
	}

	unavailable := mw(func(context.Context, any) (any, error) { return nil, kerrors.ServiceUnavailable("UNAVAILABLE", "x") })
	for range 3 {
		_, _ = unavailable(ctx, nil)
	}
	if b.State() != "open" {
		t.Fatalf("got %s", b.State())
	}
	_, err := unavailable(ctx, nil)
	if !errors.Is(err, circuitbreaker.ErrNotAllowed) {
		t.Fatalf("en OPEN el middleware debe devolver ErrNotAllowed: %v", err)
	}
}
