// Package conf define la configuración de arranque de Orders.
package conf

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/franbenaglia/go-microservices-lab/pkg/discovery"
	"github.com/franbenaglia/go-microservices-lab/pkg/platform"
	"github.com/franbenaglia/go-microservices-lab/pkg/telemetry"
)

// Config es la configuración completa del servicio.
type Config struct {
	Server     Server           `json:"server"`
	Data       Data             `json:"data"`
	Inventory  Inventory        `json:"inventory"`
	Log        Log              `json:"log"`
	Consul     discovery.Config `json:"consul"`
	Resilience Resilience       `json:"resilience"`
	Telemetry  telemetry.Config `json:"telemetry"`
}

// Resilience configura el circuit breaker y los reintentos hacia Inventory.
type Resilience struct {
	Breaker Breaker `json:"breaker"`
	Retry   Retry   `json:"retry"`
}

// Breaker configura el circuit breaker (ver pkg/resilience/breaker). Kind
// requiere reinicio; los parámetros (BreakerParams) son dinámicos.
type Breaker struct {
	Kind string `json:"kind"` // classic | sre
	BreakerParams
}

// BreakerParams son los parámetros del breaker que se pueden cambiar en caliente.
type BreakerParams struct {
	Enabled             bool              `json:"enabled"`
	FailureRatio        float64           `json:"failure_ratio"`
	MinRequests         uint32            `json:"min_requests"`
	ConsecutiveFailures uint32            `json:"consecutive_failures"`
	Window              platform.Duration `json:"window"`
	OpenTimeout         platform.Duration `json:"open_timeout"`
	HalfOpenMaxRequests uint32            `json:"half_open_max_requests"`
}

// Retry configura los reintentos (ver pkg/resilience/retry).
type Retry struct {
	Enabled        bool              `json:"enabled"`
	MaxAttempts    int               `json:"max_attempts"`
	BaseBackoff    platform.Duration `json:"base_backoff"`
	MaxBackoff     platform.Duration `json:"max_backoff"`
	AttemptTimeout platform.Duration `json:"attempt_timeout"`
	Idempotent     []string          `json:"idempotent_operations"`
	KeyRequired    []string          `json:"key_required_operations"`
}

// Server agrupa los servidores HTTP y gRPC.
type Server struct {
	HTTP Listener `json:"http"`
	GRPC Listener `json:"grpc"`
}

// Listener es la dirección y el timeout de un servidor.
type Listener struct {
	Addr    string            `json:"addr"`
	Timeout platform.Duration `json:"timeout"`
}

// Data es la configuración de PostgreSQL. El DSN llega por APP_DB_DSN.
type Data struct {
	DSN         string `json:"dsn"`
	MaxConns    int32  `json:"max_conns"`
	AutoMigrate bool   `json:"auto_migrate"`
}

// Inventory configura el cliente gRPC hacia Inventory Service.
type Inventory struct {
	// Endpoint: "discovery:///inventory-service" (resolución vía Consul) o
	// una dirección fija "host:puerto" (solo para pruebas locales sin Consul).
	Endpoint string `json:"endpoint"`
	// Timeout por llamada gRPC.
	Timeout platform.Duration `json:"timeout"`
	// CompensationTimeout limita la liberación compensatoria de reservas.
	CompensationTimeout platform.Duration `json:"compensation_timeout"`
	// Balancer es el selector del balanceo del lado cliente: wrr (por
	// defecto en Kratos), p2c o random. Es global al proceso: requiere reinicio.
	Balancer string `json:"balancer"`
}

// Log es la configuración de logging.
type Log struct {
	Level string `json:"level"`
}

// Validate comprueba que la configuración sea utilizable.
func (c *Config) Validate() error {
	var errs []error
	if c.Server.HTTP.Addr == "" || c.Server.GRPC.Addr == "" {
		errs = append(errs, errors.New("server.http.addr y server.grpc.addr son obligatorios"))
	}
	if c.Server.HTTP.Timeout.Std() <= 0 || c.Server.GRPC.Timeout.Std() <= 0 {
		errs = append(errs, errors.New("los timeouts de servidor deben ser positivos"))
	}
	if c.Data.DSN == "" {
		errs = append(errs, fmt.Errorf("data.dsn vacío: definir %sDB_DSN", platform.EnvPrefix))
	}
	if c.Inventory.Endpoint == "" {
		errs = append(errs, errors.New("inventory.endpoint es obligatorio"))
	}
	if strings.HasPrefix(c.Inventory.Endpoint, "discovery:///") && !c.Consul.Enabled() {
		errs = append(errs, errors.New("inventory.endpoint usa discovery:/// pero consul.address está vacío"))
	}
	switch c.Inventory.Balancer {
	case "wrr", "p2c", "random":
	default:
		errs = append(errs, fmt.Errorf("inventory.balancer %q inválido (wrr, p2c, random)", c.Inventory.Balancer))
	}
	if c.Inventory.Timeout.Std() <= 0 {
		errs = append(errs, errors.New("inventory.timeout debe ser positivo"))
	}
	if k := c.Resilience.Breaker.Kind; c.Resilience.Breaker.Enabled && k != "classic" && k != "sre" {
		errs = append(errs, fmt.Errorf("resilience.breaker.kind %q inválido (classic, sre)", k))
	}
	if r := c.Resilience.Retry; r.Enabled && r.AttemptTimeout.Std() >= c.Inventory.Timeout.Std() {
		errs = append(errs, errors.New("resilience.retry.attempt_timeout debe ser menor que inventory.timeout"))
	}
	if err := c.Consul.Validate(); err != nil {
		errs = append(errs, err)
	}
	if err := c.Telemetry.Validate(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Dynamic son los parámetros que se pueden cambiar en caliente desde Consul KV
// (config/orders-service/dynamic.yaml). El resto requiere reinicio.
type Dynamic struct {
	Log        Log               `json:"log"`
	Inventory  DynamicInventory  `json:"inventory"`
	Resilience DynamicResilience `json:"resilience"`
}

// DynamicResilience es la resiliencia sin breaker.kind (requiere reinicio).
type DynamicResilience struct {
	Breaker BreakerParams `json:"breaker"`
	Retry   Retry         `json:"retry"`
}

// DynamicInventory es la parte dinámica del cliente de Inventory.
type DynamicInventory struct {
	// Timeout es el presupuesto total por llamada (incluye reintentos).
	Timeout platform.Duration `json:"timeout"`
}

// Dynamic devuelve los valores de arranque de los parámetros dinámicos.
func (c *Config) Dynamic() Dynamic {
	return Dynamic{
		Log:        c.Log,
		Inventory:  DynamicInventory{Timeout: c.Inventory.Timeout},
		Resilience: DynamicResilience{Breaker: c.Resilience.Breaker.BreakerParams, Retry: c.Resilience.Retry},
	}
}

// Validate comprueba la parte que no depende de otros componentes.
func (d *Dynamic) Validate() error {
	var errs []error
	if err := platform.ValidateLogLevel(d.Log.Level); err != nil {
		errs = append(errs, err)
	}
	if t := d.Inventory.Timeout.Std(); t <= 0 || t > 30*time.Second {
		errs = append(errs, errors.New("inventory.timeout debe estar entre 0 y 30s"))
	}
	return errors.Join(errs...)
}

// RestartRequired enumera los parámetros que solo se aplican al reiniciar.
var RestartRequired = []string{
	"server.http.addr", "server.grpc.addr", "server.*.timeout", "data.*", "consul.*", "telemetry.*",
	"inventory.endpoint", "inventory.balancer", "inventory.compensation_timeout",
	"resilience.breaker.kind", "LAB_MODE",
}

// Static devuelve la configuración de arranque sin secretos (sin DSN).
func (c *Config) Static() map[string]any {
	return map[string]any{
		"server":    map[string]string{"http": c.Server.HTTP.Addr, "grpc": c.Server.GRPC.Addr},
		"inventory": map[string]string{"endpoint": c.Inventory.Endpoint, "balancer": c.Inventory.Balancer, "compensation_timeout": c.Inventory.CompensationTimeout.Std().String()},
		"breaker":   map[string]string{"kind": c.Resilience.Breaker.Kind},
		"consul":    c.Consul.Address,
		"telemetry": c.Telemetry.Endpoint,
	}
}
