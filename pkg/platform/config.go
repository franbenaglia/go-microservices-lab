// Package platform reúne la infraestructura común de arranque de los
// microservicios: configuración, logging, identidad de instancia y health checks.
// No contiene lógica de negocio.
package platform

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v3/config"
	"github.com/go-kratos/kratos/v3/config/env"
	"github.com/go-kratos/kratos/v3/config/file"
)

// EnvPrefix es el prefijo de las variables de entorno visibles para la
// configuración. APP_DB_DSN queda disponible como placeholder ${DB_DSN}.
const EnvPrefix = "APP_"

// LoadConfig carga el archivo YAML indicado, resuelve los placeholders
// ${CLAVE:default} contra las variables APP_* y vuelca el resultado en target.
// Devuelve el config.Config abierto para que el llamador pueda cerrarlo.
func LoadConfig(path string, target any) (config.Config, error) {
	c := config.New(
		config.WithSource(
			file.NewSource(path),
			env.NewSource(EnvPrefix),
		),
		config.WithResolveActualTypes(true),
	)
	if err := c.Load(); err != nil {
		return nil, fmt.Errorf("cargar configuración %q: %w", path, err)
	}
	if err := c.Scan(target); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("interpretar configuración: %w", err)
	}
	return c, nil
}

// Duration permite expresar duraciones como "2s" o "150ms" en la configuración.
type Duration time.Duration

// Std devuelve la duración como time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// UnmarshalJSON acepta una cadena con formato time.ParseDuration o un número
// de nanosegundos.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("duración inválida %q: %w", s, err)
		}
		*d = Duration(v)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("duración inválida %s", string(b))
	}
	*d = Duration(n)
	return nil
}

// MarshalJSON serializa la duración en formato legible.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}
