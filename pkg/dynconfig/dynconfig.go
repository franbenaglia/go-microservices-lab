// Package dynconfig implementa la configuración dinámica de los servicios
// sobre Consul KV, con la fuente oficial de Kratos v3
// (contrib/config/consul/v3) y su mecanismo de observadores.
//
// Cada servicio tiene un documento YAML en config/<servicio>/dynamic.yaml con
// los parámetros que pueden cambiar sin reiniciar. Al arrancar:
//
//  1. si el documento no existe, se siembra con los valores de arranque (CAS:
//     con varias réplicas, solo una lo escribe);
//  2. se carga con config.New(WithSource(consul)) y se registra un observador
//     por cada clave de primer nivel;
//  3. ante cada cambio se vuelve a leer el documento entero, se valida y solo
//     entonces se aplica. Si no es válido, se rechaza completo y se conserva la
//     configuración anterior: nunca se aplica a medias.
//
// Los secretos nunca van en este documento: las credenciales llegan por
// variables de entorno.
package dynconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	consulcfg "github.com/go-kratos/kratos/contrib/config/consul/v3"
	"github.com/go-kratos/kratos/v3/config"
	"github.com/hashicorp/consul/api"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"gopkg.in/yaml.v3"
)

// DocName es el nombre del documento dentro de config/<servicio>/.
const DocName = "dynamic.yaml"

// Options configura un Manager.
type Options[T any] struct {
	Service string
	// Client de Consul; nil desactiva la configuración dinámica (solo valores de arranque).
	Client *api.Client
	// Defaults son los valores de arranque (archivo + entorno).
	Defaults T
	// Validate comprueba el documento completo antes de aplicarlo.
	Validate func(prev, next *T) error
	// Apply aplica la configuración validada. Debe ser rápido y no fallar.
	Apply  func(prev, next *T)
	Logger *slog.Logger
	Meter  metric.Meter
}

// Status describe el resultado de la última recarga.
type Status struct {
	Enabled     bool      `json:"enabled"`
	Key         string    `json:"key"`
	Applied     uint64    `json:"applied"`
	Rejected    uint64    `json:"rejected"`
	LastApplied time.Time `json:"last_applied,omitzero"`
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitzero"`
}

// Manager mantiene la configuración dinámica vigente.
type Manager[T any] struct {
	opts    Options[T]
	key     string
	prefix  string
	cfg     config.Config
	current atomic.Pointer[T]

	mu     sync.Mutex // serializa las recargas
	status Status

	reloads metric.Int64Counter
}

// New crea el gestor. La configuración vigente empieza siendo Defaults.
func New[T any](o Options[T]) (*Manager[T], error) {
	m := &Manager[T]{
		opts:   o,
		prefix: "config/" + o.Service,
	}
	m.key = m.prefix + "/" + DocName
	d := o.Defaults
	m.current.Store(&d)
	m.status = Status{Enabled: o.Client != nil, Key: m.key}
	if o.Meter != nil {
		c, err := o.Meter.Int64Counter("config_reloads_total",
			metric.WithDescription("Recargas de la configuración dinámica por resultado"))
		if err != nil {
			return nil, err
		}
		m.reloads = c
	}
	return m, nil
}

// Current devuelve la configuración vigente (no debe modificarse).
func (m *Manager[T]) Current() *T { return m.current.Load() }

// Status devuelve el estado de la última recarga.
func (m *Manager[T]) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Start siembra el documento si falta, lo carga y empieza a vigilarlo.
func (m *Manager[T]) Start(ctx context.Context) error {
	if m.opts.Client == nil {
		return nil
	}
	if err := m.seed(ctx); err != nil {
		return fmt.Errorf("sembrar %s: %w", m.key, err)
	}
	src, err := consulcfg.New(m.opts.Client, consulcfg.WithPath(m.prefix), consulcfg.WithContext(ctx))
	if err != nil {
		return err
	}
	m.cfg = config.New(config.WithSource(src))
	if err := m.cfg.Load(); err != nil {
		return fmt.Errorf("cargar %s: %w", m.key, err)
	}
	m.reload("startup")

	// Un observador por clave de primer nivel: Kratos lo invoca cuando cambia su
	// valor. Cada aviso relee y valida el documento completo.
	keys, err := topLevelKeys(m.opts.Defaults)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := m.cfg.Watch(k, func(string, config.Value) { m.reload("watch") }); err != nil {
			return fmt.Errorf("observar %q: %w", k, err)
		}
	}
	return nil
}

// Close detiene la vigilancia.
func (m *Manager[T]) Close() error {
	if m.cfg == nil {
		return nil
	}
	return m.cfg.Close()
}

// reload lee el documento, lo valida y lo aplica (todo o nada).
func (m *Manager[T]) reload(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prev := m.current.Load()
	next := m.opts.Defaults // los campos ausentes toman el valor de arranque
	err := m.cfg.Scan(&next)
	if err == nil && m.opts.Validate != nil {
		err = m.opts.Validate(prev, &next)
	}
	if err != nil {
		m.status.Rejected++
		m.status.LastError = err.Error()
		m.status.LastErrorAt = time.Now()
		m.count("rejected")
		m.opts.Logger.Error("configuración dinámica rechazada; se mantiene la anterior",
			slog.String("key", m.key), slog.String("reason", reason), slog.Any("error", err))
		return
	}
	if m.opts.Apply != nil {
		m.opts.Apply(prev, &next)
	}
	m.current.Store(&next)
	m.status.Applied++
	m.status.LastApplied = time.Now()
	m.count("applied")
	m.opts.Logger.Info("configuración dinámica aplicada", slog.String("key", m.key), slog.String("reason", reason))
}

func (m *Manager[T]) count(result string) {
	if m.reloads != nil {
		m.reloads.Add(context.Background(), 1, metric.WithAttributes(attribute.String("result", result)))
	}
}

// seed escribe los valores de arranque si el documento no existe (CAS con
// índice 0: si otra réplica lo creó antes, no se sobrescribe).
func (m *Manager[T]) seed(ctx context.Context) error {
	kv := m.opts.Client.KV()
	pair, _, err := kv.Get(m.key, (&api.QueryOptions{}).WithContext(ctx))
	if err != nil {
		return err
	}
	if pair != nil {
		return nil
	}
	doc, err := toYAML(m.opts.Defaults)
	if err != nil {
		return err
	}
	ok, _, err := kv.CAS(&api.KVPair{Key: m.key, Value: doc, ModifyIndex: 0}, (&api.WriteOptions{}).WithContext(ctx))
	if err != nil {
		return err
	}
	if ok {
		m.opts.Logger.Info("documento de configuración dinámica sembrado", slog.String("key", m.key))
	}
	return nil
}

// Write valida next y lo guarda en Consul. Todas las instancias (incluida esta)
// lo aplicarán al recibir el aviso del watcher.
func (m *Manager[T]) Write(ctx context.Context, next *T) error {
	if m.opts.Client == nil {
		return errors.New("configuración dinámica desactivada (sin Consul)")
	}
	if m.opts.Validate != nil {
		if err := m.opts.Validate(m.current.Load(), next); err != nil {
			return &ValidationError{Err: err}
		}
	}
	doc, err := toYAML(*next)
	if err != nil {
		return err
	}
	_, err = m.opts.Client.KV().Put(&api.KVPair{Key: m.key, Value: doc}, (&api.WriteOptions{}).WithContext(ctx))
	return err
}

// ValidationError indica que el documento propuesto no es válido.
type ValidationError struct{ Err error }

func (e *ValidationError) Error() string { return "configuración inválida: " + e.Err.Error() }
func (e *ValidationError) Unwrap() error { return e.Err }

// rawCheck lee el documento tal como está en Consul y comprueba si es válido.
// Detecta ediciones manuales con YAML mal formado: Kratos las descarta sin
// avisar a los observadores.
func (m *Manager[T]) rawCheck(ctx context.Context) (raw string, problem string) {
	if m.opts.Client == nil {
		return "", ""
	}
	pair, _, err := m.opts.Client.KV().Get(m.key, (&api.QueryOptions{}).WithContext(ctx))
	if err != nil {
		return "", "no se pudo leer Consul: " + err.Error()
	}
	if pair == nil {
		return "", "el documento no existe en Consul"
	}
	var doc map[string]any
	if err := yaml.Unmarshal(pair.Value, &doc); err != nil {
		return string(pair.Value), "YAML mal formado (se mantiene la configuración anterior): " + err.Error()
	}
	next := m.opts.Defaults
	b, _ := json.Marshal(doc)
	if err := json.Unmarshal(b, &next); err != nil {
		return string(pair.Value), "tipos inválidos: " + err.Error()
	}
	if m.opts.Validate != nil {
		if err := m.opts.Validate(m.current.Load(), &next); err != nil {
			return string(pair.Value), "valores inválidos: " + err.Error()
		}
	}
	return string(pair.Value), ""
}

// View es la respuesta de GET /admin/config.
type View[T any] struct {
	Service         string   `json:"service"`
	Instance        string   `json:"instance"`
	Dynamic         *T       `json:"dynamic"`
	Static          any      `json:"static"`
	RestartRequired []string `json:"restart_required"`
	Status          Status   `json:"status"`
	ConsulDocument  string   `json:"consul_document,omitempty"`
	ConsulProblem   string   `json:"consul_problem,omitempty"`
}

// Handler expone /admin/config:
//
//	GET  devuelve la configuración dinámica vigente, la estática (sin secretos),
//	     los parámetros que requieren reinicio y el estado de las recargas.
//	PUT  recibe un JSON parcial, lo combina con la configuración vigente, lo
//	     valida y lo guarda en Consul (202). Todas las réplicas lo aplican.
func (m *Manager[T]) Handler(instance string, static any, restartRequired []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			raw, problem := m.rawCheck(r.Context())
			writeJSON(w, http.StatusOK, View[T]{
				Service: m.opts.Service, Instance: instance, Dynamic: m.Current(), Static: static,
				RestartRequired: slices.Clone(restartRequired), Status: m.Status(),
				ConsulDocument: raw, ConsulProblem: problem,
			})
		case http.MethodPut, http.MethodPatch:
			next := *m.Current()
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&next); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON inválido: " + err.Error()})
				return
			}
			if err := m.Write(r.Context(), &next); err != nil {
				code := http.StatusServiceUnavailable
				var ve *ValidationError
				if errors.As(err, &ve) {
					code = http.StatusBadRequest
				}
				writeJSON(w, code, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]any{
				"stored": true, "key": m.key,
				"note": "guardado en Consul KV; cada instancia lo aplicará al recibir el aviso del watcher",
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// toYAML convierte T a YAML usando los nombres JSON de sus campos (los mismos
// que usa Kratos al leer el documento).
func toYAML[T any](v T) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil { // JSON es YAML válido
		return nil, err
	}
	setBlockStyle(&doc)
	return yaml.Marshal(&doc)
}

// setBlockStyle formatea en bloque: claves sin comillas y valores de texto
// entre comillas (así "2s" o "info" se leen sin ambigüedad como cadenas).
func setBlockStyle(n *yaml.Node) {
	n.Style &^= yaml.FlowStyle
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			n.Content[i].Style = 0
			setBlockStyle(n.Content[i+1])
		}
		return
	case yaml.ScalarNode:
		if n.Tag == "!!str" {
			n.Style = yaml.DoubleQuotedStyle
		}
	}
	for _, c := range n.Content {
		setBlockStyle(c)
	}
}

func topLevelKeys[T any](v T) ([]string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("la configuración dinámica debe ser un objeto: %w", err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys, nil
}
