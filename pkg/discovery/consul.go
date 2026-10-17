// Package discovery integra los servicios con Consul mediante el registry
// oficial de Kratos v3 (contrib/registry/consul/v3).
//
// Cada instancia se registra con tres comprobaciones de salud:
//   - TCP sobre cada endpoint (la añade el contrib): el puerto acepta conexiones.
//   - TTL "heartbeat" que el propio proceso renueva: el proceso sigue vivo.
//   - HTTP sobre /readyz (añadida aquí): la instancia puede atender tráfico.
//
// Consul solo devuelve a los clientes las instancias con TODAS sus
// comprobaciones en estado passing, así que una instancia viva pero no
// preparada (sin base de datos, en apagado) deja de recibir tráfico.
package discovery

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"time"

	consul "github.com/go-kratos/kratos/contrib/registry/consul/v3"
	"github.com/go-kratos/kratos/v3"
	"github.com/go-kratos/kratos/v3/transport"
	"github.com/hashicorp/consul/api"

	"github.com/franbenaglia/go-microservices-lab/pkg/platform"
)

// minDeregisterAfter es el mínimo que acepta Consul para
// DeregisterCriticalServiceAfter.
const minDeregisterAfter = time.Minute

// Config configura la conexión y el registro en Consul.
type Config struct {
	// Address del agente Consul (host:puerto). Vacío desactiva el registro.
	Address string `json:"address"`
	// HealthInterval es la frecuencia de las comprobaciones y del heartbeat.
	HealthInterval platform.Duration `json:"health_interval"`
	// DeregisterAfter elimina del catálogo una instancia que lleva este
	// tiempo en estado crítico (mínimo 1m en Consul).
	DeregisterAfter platform.Duration `json:"deregister_after"`
}

// Enabled indica si hay que registrarse en Consul.
func (c Config) Enabled() bool { return c.Address != "" }

// Validate comprueba los valores.
func (c Config) Validate() error {
	if !c.Enabled() {
		return nil
	}
	var errs []error
	if c.HealthInterval.Std() < time.Second {
		errs = append(errs, errors.New("consul.health_interval debe ser >= 1s"))
	}
	if c.DeregisterAfter.Std() < minDeregisterAfter {
		errs = append(errs, fmt.Errorf("consul.deregister_after debe ser >= %s", minDeregisterAfter))
	}
	return errors.Join(errs...)
}

// NewRegistry crea el registry de Consul, que implementa tanto
// registry.Registrar (registro propio) como registry.Discovery (descubrir
// otros servicios). readinessURL es la URL de /readyz de esta instancia tal
// como la ve Consul.
func NewRegistry(c Config, readinessURL string) (*consul.Registry, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	client, err := NewClient(c)
	if err != nil {
		return nil, err
	}
	interval := seconds(c.HealthInterval.Std())
	deregister := seconds(c.DeregisterAfter.Std())

	opts := []consul.Option{
		consul.WithHealthCheck(true),
		consul.WithHeartbeat(true),
		consul.WithHealthCheckInterval(interval),
		consul.WithDeregisterCriticalServiceAfter(deregister),
		consul.WithTimeout(10 * time.Second),
	}
	if readinessURL != "" {
		opts = append(opts, consul.WithServiceCheck(&api.AgentServiceCheck{
			Name:                           "readiness",
			HTTP:                           readinessURL,
			Method:                         "GET",
			Interval:                       fmt.Sprintf("%ds", interval),
			Timeout:                        "3s",
			DeregisterCriticalServiceAfter: fmt.Sprintf("%ds", deregister),
		}))
	}
	return consul.New(client, opts...), nil
}

// ReadinessURL construye la URL de /readyz a partir del endpoint HTTP que
// anuncia el servidor de Kratos (IP real del contenedor).
func ReadinessURL(httpEndpoint *url.URL) string {
	return "http://" + httpEndpoint.Host + "/readyz"
}

func seconds(d time.Duration) int {
	return int(math.Ceil(d.Seconds()))
}

// Setup prepara el registro de una instancia: si Consul está configurado,
// crea el registry (con la URL de readiness del servidor HTTP indicado) y
// devuelve las opciones de kratos.App para registrarse al arrancar y
// desregistrarse al parar. Sin Consul devuelve (nil, nil, nil).
func Setup(c Config, httpSrv transport.Endpointer) ([]kratos.Option, *consul.Registry, error) {
	if !c.Enabled() {
		return nil, nil, nil
	}
	ep, err := httpSrv.Endpoint()
	if err != nil {
		return nil, nil, fmt.Errorf("endpoint HTTP: %w", err)
	}
	reg, err := NewRegistry(c, ReadinessURL(ep))
	if err != nil {
		return nil, nil, err
	}
	return []kratos.Option{
		kratos.Registrar(reg),
		kratos.RegistrarTimeout(5 * time.Second),
	}, reg, nil
}

// NewClient crea un cliente de la API de Consul (registro, KV...). Devuelve
// nil sin error si Consul no está configurado.
func NewClient(c Config) (*api.Client, error) {
	if !c.Enabled() {
		return nil, nil
	}
	cfg := api.DefaultConfig()
	cfg.Address = c.Address
	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("cliente consul: %w", err)
	}
	return client, nil
}
