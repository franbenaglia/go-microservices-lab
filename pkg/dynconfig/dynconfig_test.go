package dynconfig

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/franbenaglia/go-microservices-lab/pkg/platform"
)

type testCfg struct {
	Log struct {
		Level string `json:"level"`
	} `json:"log"`
	Call struct {
		Timeout platform.Duration `json:"timeout"`
		Limit   int               `json:"limit"`
	} `json:"call"`
}

func validate(_, next *testCfg) error {
	if next.Call.Timeout.Std() <= 0 {
		return errors.New("timeout debe ser positivo")
	}
	if next.Log.Level != "info" && next.Log.Level != "debug" {
		return errors.New("nivel inválido")
	}
	return nil
}

func defaults() testCfg {
	var c testCfg
	c.Log.Level = "info"
	c.Call.Timeout = platform.Duration(2 * time.Second)
	c.Call.Limit = 5
	return c
}

func TestToYAMLAndKeys(t *testing.T) {
	b, err := toYAML(defaults())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`level: "info"`, `timeout: "2s"`, "limit: 5"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("falta %q en:\n%s", want, b)
		}
	}
	keys, _ := topLevelKeys(defaults())
	if strings.Join(keys, ",") != "call,log" {
		t.Fatalf("claves %v", keys)
	}
}

func TestDisabledWithoutConsul(t *testing.T) {
	m, err := New(Options[testCfg]{Service: "x", Defaults: defaults(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil || m.Current().Call.Limit != 5 || m.Status().Enabled {
		t.Fatal("sin Consul se usan los valores de arranque")
	}
	if m.Write(context.Background(), m.Current()) == nil {
		t.Fatal("sin Consul no se puede escribir")
	}
}

// --- integración (LAB_TEST_CONSUL_ADDR) ---

func consulClient(t *testing.T) *api.Client {
	t.Helper()
	addr := os.Getenv("LAB_TEST_CONSUL_ADDR")
	if addr == "" {
		t.Skip("LAB_TEST_CONSUL_ADDR no definida: se omite la prueba de integración")
	}
	cfg := api.DefaultConfig()
	cfg.Address = addr
	c, err := api.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("no se cumplió a tiempo: %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestConsulLifecycle(t *testing.T) {
	client := consulClient(t)
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	service := "test-" + hex.EncodeToString(b)
	t.Cleanup(func() { _, _ = client.KV().DeleteTree("config/"+service, nil) })

	var applied atomic.Int32
	m, err := New(Options[testCfg]{
		Service: service, Client: client, Defaults: defaults(), Validate: validate,
		Apply:  func(_, _ *testCfg) { applied.Add(1) },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })

	// 1. Siembra: el documento existe con los valores de arranque.
	pair, _, err := client.KV().Get("config/"+service+"/dynamic.yaml", nil)
	if err != nil || pair == nil || !strings.Contains(string(pair.Value), `timeout: "2s"`) {
		t.Fatalf("siembra: %v %v", pair, err)
	}

	// 2. Escritura validada: se aplica a través del watcher, sin reinicio.
	next := *m.Current()
	next.Call.Timeout = platform.Duration(750 * time.Millisecond)
	next.Log.Level = "debug"
	if err := m.Write(ctx, &next); err != nil {
		t.Fatal(err)
	}
	eventually(t, "aplicar timeout=750ms", func() bool {
		return m.Current().Call.Timeout.Std() == 750*time.Millisecond && m.Current().Log.Level == "debug"
	})

	// 3. Edición manual inválida en Consul: se rechaza y se conserva la anterior.
	_, err = client.KV().Put(&api.KVPair{Key: "config/" + service + "/dynamic.yaml",
		Value: []byte("log:\n  level: \"trace\"\ncall:\n  timeout: \"1s\"\n  limit: 9\n")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "rechazo", func() bool { return m.Status().Rejected >= 1 })
	if m.Current().Call.Limit != 5 || m.Current().Log.Level != "debug" {
		t.Fatalf("no debe aplicarse nada de un documento inválido: %+v", m.Current())
	}

	// 4. YAML mal formado: Kratos lo descarta en silencio; GET lo detecta.
	_, _ = client.KV().Put(&api.KVPair{Key: "config/" + service + "/dynamic.yaml", Value: []byte("log: [sin cerrar")}, nil)
	time.Sleep(500 * time.Millisecond)
	if _, problem := m.rawCheck(ctx); !strings.Contains(problem, "mal formado") {
		t.Fatalf("problema no detectado: %q", problem)
	}

	// 5. El handler rechaza escrituras inválidas antes de llegar a Consul.
	rec := httptest.NewRecorder()
	m.Handler("i1", nil, nil)(rec, httptest.NewRequest(http.MethodPut, "/admin/config",
		strings.NewReader(`{"call":{"timeout":"0s"}}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT inválido: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	m.Handler("i1", nil, nil)(rec, httptest.NewRequest(http.MethodPut, "/admin/config",
		strings.NewReader(`{"call":{"limit":7}}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("PUT válido: %d %s", rec.Code, rec.Body)
	}
	eventually(t, "aplicar limit=7", func() bool { return m.Current().Call.Limit == 7 })
	if applied.Load() < 3 {
		t.Fatalf("Apply se llamó %d veces", applied.Load())
	}
}
