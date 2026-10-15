package discovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	consul "github.com/go-kratos/kratos/contrib/registry/consul/v3"
	"github.com/go-kratos/kratos/v3/registry"
	"github.com/hashicorp/consul/api"

	"github.com/franbenaglia/go-microservices-lab/pkg/platform"
)

func TestConfigValidate(t *testing.T) {
	ok := Config{Address: "consul:8500", HealthInterval: platform.Duration(5 * time.Second), DeregisterAfter: platform.Duration(time.Minute)}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Config{}).Validate(); err != nil || (Config{}).Enabled() {
		t.Fatal("sin dirección, el registro debe estar desactivado y ser válido")
	}
	bad := ok
	bad.DeregisterAfter = platform.Duration(30 * time.Second)
	if bad.Validate() == nil {
		t.Fatal("Consul exige deregister_after >= 1m")
	}
	bad = ok
	bad.HealthInterval = platform.Duration(100 * time.Millisecond)
	if bad.Validate() == nil {
		t.Fatal("health_interval < 1s debe rechazarse")
	}
}

func TestReadinessURL(t *testing.T) {
	u, _ := url.Parse("http://172.19.0.5:8000")
	if got := ReadinessURL(u); got != "http://172.19.0.5:8000/readyz" {
		t.Fatal(got)
	}
}

// --- integración (requiere LAB_TEST_CONSUL_ADDR, p. ej. 127.0.0.1:8500) ---

func consulAddr(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("LAB_TEST_CONSUL_ADDR")
	if addr == "" {
		t.Skip("LAB_TEST_CONSUL_ADDR no definida: se omite la prueba de integración")
	}
	return addr
}

func apiClient(t *testing.T, addr string) *api.Client {
	t.Helper()
	cfg := api.DefaultConfig()
	cfg.Address = addr
	c, err := api.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func uniqueName(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

func nextIDs(t *testing.T, w registry.Watcher) []string {
	t.Helper()
	type result struct {
		ins []*registry.ServiceInstance
		err error
	}
	ch := make(chan result, 1)
	go func() {
		ins, err := w.Next()
		ch <- result{ins, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatal(r.err)
		}
		ids := make([]string, len(r.ins))
		for i, in := range r.ins {
			ids[i] = in.ID
		}
		slices.Sort(ids)
		return ids
	case <-time.After(15 * time.Second):
		t.Fatal("el watcher no recibió cambios a tiempo")
		return nil
	}
}

// Registro con heartbeat TTL (sin checks TCP, porque Consul corre en otro
// contenedor y no alcanza 127.0.0.1 del host) y descubrimiento con Watch.
func TestRegisterWatchDeregister(t *testing.T) {
	addr := consulAddr(t)
	name := uniqueName("lab-test")
	registrar := consul.New(apiClient(t, addr),
		consul.WithHealthCheck(false), consul.WithHeartbeat(true), consul.WithHealthCheckInterval(1))
	discoverer := consul.New(apiClient(t, addr))
	ctx := context.Background()

	mk := func(id string) *registry.ServiceInstance {
		return &registry.ServiceInstance{
			ID: id, Name: name, Version: "test",
			Metadata:  map[string]string{"instance": id},
			Endpoints: []string{"http://10.0.0.1:8000", "grpc://10.0.0.1:9000"},
		}
	}
	a, b := mk(name+"-a"), mk(name+"-b")
	t.Cleanup(func() {
		_ = registrar.Deregister(ctx, a)
		_ = registrar.Deregister(ctx, b)
	})

	if err := registrar.Register(ctx, a); err != nil {
		t.Fatal(err)
	}
	w, err := discoverer.Watch(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	if got := nextIDs(t, w); !slices.Equal(got, []string{a.ID}) {
		t.Fatalf("tras registrar A: %v", got)
	}

	if err := registrar.Register(ctx, b); err != nil {
		t.Fatal(err)
	}
	if got := nextIDs(t, w); !slices.Equal(got, []string{a.ID, b.ID}) {
		t.Fatalf("tras registrar B: %v", got)
	}

	if err := registrar.Deregister(ctx, b); err != nil {
		t.Fatal(err)
	}
	if got := nextIDs(t, w); !slices.Equal(got, []string{a.ID}) {
		t.Fatalf("tras desregistrar B: %v", got)
	}

	// Metadatos y direcciones etiquetadas por protocolo.
	entries, _, err := apiClient(t, addr).Health().Service(name, "", true, nil)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%d err=%v", len(entries), err)
	}
	svc := entries[0].Service
	if svc.Meta["instance"] != a.ID || svc.TaggedAddresses["grpc"].Address != "grpc://10.0.0.1:9000" {
		t.Fatalf("registro inesperado: meta=%v tagged=%v", svc.Meta, svc.TaggedAddresses)
	}
}

// registerWithReadiness registra una instancia con NewRegistry. Los checks los
// ejecuta el agente Consul desde su contenedor, así que 127.0.0.1:8500 es la
// propia API de Consul (siempre accesible) y 127.0.0.1:1 nunca responde.
func registerWithReadiness(t *testing.T, addr, readyURL string) (string, *api.Client) {
	t.Helper()
	name := uniqueName("lab-ready")
	reg, err := NewRegistry(Config{
		Address:         addr,
		HealthInterval:  platform.Duration(time.Second),
		DeregisterAfter: platform.Duration(time.Minute),
	}, readyURL)
	if err != nil {
		t.Fatal(err)
	}
	ins := &registry.ServiceInstance{ID: name, Name: name, Endpoints: []string{"http://127.0.0.1:8500"}}
	ctx := context.Background()
	if err := reg.Register(ctx, ins); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Deregister(ctx, ins) })
	return name, apiClient(t, addr)
}

// checkStatuses devuelve el estado de cada check de la instancia por nombre
// ("readiness") o tipo ("ttl", "tcp").
func checkStatuses(t *testing.T, cli *api.Client, name string) map[string]string {
	t.Helper()
	all, _, err := cli.Health().Service(name, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	if len(all) == 1 {
		for _, c := range all[0].Checks {
			if c.ServiceID == "" {
				continue // check del nodo (serf)
			}
			key := c.Type
			if c.Name == "readiness" {
				key = "readiness"
			}
			out[key] = c.Status
		}
	}
	return out
}

func passingCount(t *testing.T, cli *api.Client, name string) int {
	t.Helper()
	p, _, err := cli.Health().Service(name, "", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	return len(p)
}

// Con /readyz respondiendo 200, la instancia llega a passing (y se descubre).
func TestReadinessCheckPassing(t *testing.T) {
	addr := consulAddr(t)
	name, cli := registerWithReadiness(t, addr, "http://127.0.0.1:8500/v1/status/leader")
	deadline := time.Now().Add(10 * time.Second)
	for passingCount(t, cli, name) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("la instancia preparada no llegó a passing: %v", checkStatuses(t, cli, name))
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// Con /readyz sin responder, la instancia nunca está passing aunque el
// proceso (TTL) y el puerto (TCP) estén bien: no recibe tráfico.
func TestReadinessCheckGatesTraffic(t *testing.T) {
	addr := consulAddr(t)
	name, cli := registerWithReadiness(t, addr, "http://127.0.0.1:1/readyz")
	// Varios intervalos de check (1s): en el caso positivo bastan ~1s para
	// llegar a passing, así que esto ya no es el estado inicial de Consul.
	time.Sleep(4 * time.Second)
	st := checkStatuses(t, cli, name)
	if st["ttl"] != api.HealthPassing || st["tcp"] != api.HealthPassing {
		t.Fatalf("el proceso y el puerto deberían estar sanos: %v", st)
	}
	if st["readiness"] != api.HealthCritical {
		t.Fatalf("readiness debería estar critical: %v", st)
	}
	if n := passingCount(t, cli, name); n != 0 {
		t.Fatalf("una instancia no preparada no debe estar passing (%d)", n)
	}
}
