// Package conf define la configuración de arranque de Orders.
package conf

import (
	"errors"
	"fmt"

	"github.com/franbenaglia/go-microservices-lab/pkg/platform"
)

// Config es la configuración completa del servicio.
type Config struct {
	Server    Server    `json:"server"`
	Data      Data      `json:"data"`
	Inventory Inventory `json:"inventory"`
	Log       Log       `json:"log"`
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
	// Endpoint, p. ej. "inventory:9000". En la Fase 2 se sustituye por
	// "discovery:///inventory-service" (resolución vía Consul).
	Endpoint string `json:"endpoint"`
	// Timeout por llamada gRPC.
	Timeout platform.Duration `json:"timeout"`
	// CompensationTimeout limita la liberación compensatoria de reservas.
	CompensationTimeout platform.Duration `json:"compensation_timeout"`
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
	if c.Inventory.Timeout.Std() <= 0 {
		errs = append(errs, errors.New("inventory.timeout debe ser positivo"))
	}
	return errors.Join(errs...)
}
