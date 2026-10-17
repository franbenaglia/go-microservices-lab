// Package conf define la configuración de arranque de Inventory.
package conf

import (
	"errors"
	"fmt"

	"github.com/franbenaglia/go-microservices-lab/pkg/discovery"
	"github.com/franbenaglia/go-microservices-lab/pkg/platform"
	"github.com/franbenaglia/go-microservices-lab/pkg/telemetry"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/internal/lab"
)

// Config es la configuración completa del servicio.
type Config struct {
	Server    Server           `json:"server"`
	Data      Data             `json:"data"`
	Log       Log              `json:"log"`
	Consul    discovery.Config `json:"consul"`
	Telemetry telemetry.Config `json:"telemetry"`
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

// Data es la configuración de PostgreSQL. El DSN llega por la variable APP_DB_DSN.
type Data struct {
	DSN         string `json:"dsn"`
	MaxConns    int32  `json:"max_conns"`
	AutoMigrate bool   `json:"auto_migrate"`
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
	if err := c.Consul.Validate(); err != nil {
		errs = append(errs, err)
	}
	if err := c.Telemetry.Validate(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Dynamic son los parámetros que se pueden cambiar en caliente desde Consul KV
// (config/inventory-service/dynamic.yaml). Todas las réplicas los aplican.
type Dynamic struct {
	Log Log        `json:"log"`
	Lab DynamicLab `json:"lab"`
}

// DynamicLab son los fallos simulados para todas las réplicas (solo con LAB_MODE=true).
type DynamicLab struct {
	Faults lab.Faults `json:"faults"`
}

// Dynamic devuelve los valores de arranque (sin fallos simulados).
func (c *Config) Dynamic() Dynamic { return Dynamic{Log: c.Log} }

// Validate comprueba los valores.
func (d *Dynamic) Validate() error {
	return errors.Join(platform.ValidateLogLevel(d.Log.Level), d.Lab.Faults.Validate())
}

// RestartRequired enumera los parámetros que solo se aplican al reiniciar.
var RestartRequired = []string{
	"server.http.addr", "server.grpc.addr", "server.*.timeout", "data.*", "consul.*", "telemetry.*",
	"INSTANCE_WEIGHT", "LAB_MODE",
}

// Static devuelve la configuración de arranque sin secretos (sin DSN).
func (c *Config) Static() map[string]any {
	return map[string]any{
		"server":    map[string]string{"http": c.Server.HTTP.Addr, "grpc": c.Server.GRPC.Addr},
		"consul":    c.Consul.Address,
		"telemetry": c.Telemetry.Endpoint,
	}
}
