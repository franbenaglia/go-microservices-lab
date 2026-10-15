package lab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/transport"
)

type tr struct{ op string }

func (t tr) Kind() transport.Kind            { return transport.KindGRPC }
func (t tr) Endpoint() string                { return "" }
func (t tr) Operation() string               { return t.op }
func (t tr) RequestHeader() transport.Header { return nil }
func (t tr) ReplyHeader() transport.Header   { return nil }

func ctxOp(m string) context.Context {
	return transport.NewServerContext(context.Background(), tr{op: "/inventory.v1.InventoryService/" + m})
}

func ok(context.Context, any) (any, error) { return "ok", nil }

func TestDisabledByDefault(t *testing.T) {
	i := NewInjector("i1")
	if _, err := i.Middleware()(ok)(ctxOp("GetProduct"), nil); err != nil {
		t.Fatal(err)
	}
	if i.ReadinessCheck(context.Background()) != nil {
		t.Fatal("readiness no debe fallar por defecto")
	}
}

func TestUnavailableAndOperationsFilter(t *testing.T) {
	i := NewInjector("i1")
	if err := i.Set(Faults{Unavailable: true, Operations: []string{"Diagnose"}}); err != nil {
		t.Fatal(err)
	}
	_, err := i.Middleware()(ok)(ctxOp("Diagnose"), nil)
	if kerrors.Code(err) != 503 || kerrors.Reason(err) != "FAULT_INJECTED" {
		t.Fatalf("got %v", err)
	}
	if _, err := i.Middleware()(ok)(ctxOp("GetProduct"), nil); err != nil {
		t.Fatalf("GetProduct no está en operations: %v", err)
	}
}

func TestErrorRate(t *testing.T) {
	i := NewInjector("i1")
	_ = i.Set(Faults{ErrorRate: 0.3, ErrorCode: 500})
	failed := 0
	for range 2000 {
		if _, err := i.Middleware()(ok)(ctxOp("GetProduct"), nil); err != nil {
			if kerrors.Code(err) != 500 {
				t.Fatalf("código %d", kerrors.Code(err))
			}
			failed++
		}
	}
	if failed < 480 || failed > 720 {
		t.Fatalf("con error_rate=0.3 fallaron %d de 2000", failed)
	}
}

func TestLatencyRespectsCancellation(t *testing.T) {
	i := NewInjector("i1")
	_ = i.Set(Faults{LatencyMs: 2000})
	ctx, cancel := context.WithTimeout(ctxOp("GetProduct"), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := i.Middleware()(ok)(ctx, nil)
	if kerrors.Code(err) != 504 || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("err=%v en %s", err, time.Since(start))
	}
}

func TestReadinessAndValidation(t *testing.T) {
	i := NewInjector("i1")
	_ = i.Set(Faults{FailReadiness: true})
	if i.ReadinessCheck(context.Background()) == nil {
		t.Fatal("readiness debería fallar")
	}
	for _, f := range []Faults{{ErrorRate: 2}, {LatencyMs: -1}, {ErrorCode: 404}} {
		if i.Set(f) == nil {
			t.Errorf("%+v debería ser inválido", f)
		}
	}
}

func TestAdminHandler(t *testing.T) {
	i := NewInjector("i1")
	h := i.Handler()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPut, "/admin/faults", strings.NewReader(`{"latency_ms":100}`)))
	if rec.Code != 200 || i.Get().LatencyMs != 100 || i.Get().ErrorCode != 503 {
		t.Fatalf("PUT: %d %s %+v", rec.Code, rec.Body, i.Get())
	}
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPut, "/admin/faults", strings.NewReader(`{"latencia":1}`)))
	if rec.Code != 400 {
		t.Fatalf("campo desconocido: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodDelete, "/admin/faults", nil))
	if rec.Code != 200 || i.Get().Active() {
		t.Fatalf("DELETE: %d %+v", rec.Code, i.Get())
	}
}
