# Análisis de viabilidad

Fecha: 2026-10-15. Verificado contra el proxy de módulos de Go y el código fuente descargado.

## Veredicto

**Factible.** Todas las piezas existen en versiones estables. Hay tres puntos donde Kratos v3
no cubre directamente lo que pide la especificación; cada uno se resuelve con un adaptador
pequeño (ver ADR-002, ADR-003 y ADR-006). El tamaño es grande: estimación de 10 a 15 sesiones
de trabajo siguiendo las 8 fases.

## Entorno local verificado

| Herramienta | Versión | Comentario |
|---|---|---|
| Go | 1.27.2 | Kratos v3 requiere ≥ 1.25 |
| Docker Engine | 29.8.0 | OK |
| Docker Compose | v5.5.1 | OK |
| Node | 20.20.2 | Suficiente para Vite; el build del frontend se hace en contenedor igualmente |
| protoc | /home/francisco/anaconda3/bin/protoc | Proviene de Anaconda: **no se usará**. Se usará `buf` como herramienta de Go (`go tool`) para tener builds reproducibles |

## Dependencias verificadas

| Módulo | Versión | Estado |
|---|---|---|
| `github.com/go-kratos/kratos/v3` | v3.0.0 (2026-06-26) | Estable, tag oficial |
| `.../contrib/registry/consul/v3` | `v3.0.0-20260626125723-668db92c2c00` | Sin tag, solo pseudo-versión del commit de v3.0.0 |
| `.../contrib/config/consul/v3` | misma pseudo-versión | Sin tag. Tiene `Watch()`, así que permite recarga dinámica |
| `.../contrib/otel/v3` (`tracing`, `metrics`, `log`) | misma pseudo-versión | Sin tag. Sustituye a `middleware/tracing` y `middleware/metrics` de v2 |
| `.../contrib/middleware/validate/v3` | misma pseudo-versión | Opcional |
| `.../cmd/protoc-gen-go-http/v3`, `.../cmd/protoc-gen-go-errors/v3` | misma pseudo-versión | Generadores |
| `github.com/hashicorp/consul/api` | v1.34.x | Dependencia del contrib |
| `github.com/sony/gobreaker/v2` | v2.4.0 | Breaker clásico de 3 estados (ADR-002) |
| `github.com/jackc/pgx/v5` | v5.11.0 | Driver PostgreSQL |
| `github.com/pressly/goose/v3` | v3.29.0 | Migraciones |
| `github.com/exaring/otelpgx` | v0.13.0 | Spans de PostgreSQL |
| imagen `golang:1.27-alpine` | existe | Build de servicios |

## Lo que aporta Kratos v3 (comprobado en el código fuente)

- `transport/grpc`: `WithDiscovery`, `WithTimeout`, `WithMiddleware`, `WithNodeFilter`, `WithHealthCheck`.
  El servidor gRPC registra `grpc.health.v1` automáticamente.
- `selector`: `wrr` (por defecto), `p2c`, `random`. Se elige con `selector.SetGlobalSelector`, que es
  **global al proceso**, así que cambiar la estrategia requiere reiniciar.
- `middleware`: `circuitbreaker`, `logging`, `metadata`, `ratelimit`, `recovery`, `selector`, `validate`.
  **No hay middleware de retry.**
- `log`: basado en `log/slog` (`log.NewLogger`, `log.SetDefault`, filtros de claves sensibles).
- `encoding`: `json` (stdlib) y `protojson` están separados; hay que importarlos explícitamente.

## Discrepancias entre la especificación y Kratos v3

### 1. Circuit breaker: no hay estado HALF-OPEN (§7)
El breaker por defecto (`internal/circuitbreaker/sre.go`) implementa *Google SRE adaptive
throttling*. Solo tiene los estados Open y Closed, rechaza de forma probabilística y no tiene
tiempo de recuperación. Además, sus opciones (`WithFailureRatio`, `WithWindow`, ...) están en
un paquete `internal`, así que no se pueden configurar desde fuera.
**Solución:** usar el middleware oficial `circuitbreaker.Client(circuitbreaker.WithBreakerFactory(...))`
con un adaptador sobre `sony/gobreaker/v2` (CLOSED/OPEN/HALF-OPEN, umbrales configurables y
`OnStateChange` para métricas). El SRE queda como modo comparativo. → ADR-002

### 2. Retries: no existe middleware (§8)
**Solución:** un middleware de cliente propio con backoff exponencial, *full jitter*, número
máximo de intentos, respeto del deadline del contexto y una lista de operaciones permitidas.
→ ADR-003

### 3. Contrib v3 sin tags
Solo existen pseudo-versiones. **Mitigación:** fijar la pseudo-versión exacta en `go.mod` y
no usar `@latest` sin revisarla.

### 4. Ejemplos escasos
Kratos v3 tiene menos de 4 meses y casi toda la documentación y los ejemplos públicos son de
v2. **Mitigación:** consultar siempre el código fuente del módulo (regla en CLAUDE.md).

## Observaciones sobre la especificación

- **Demostración del balanceo interno:** el lote de solicitudes del *Load Balancing Lab* debe
  pasar por Orders → Inventory (gRPC con Kratos). Si se enviara a `/api/inventory` a través de
  Traefik, lo que se mediría sería el balanceo de Traefik. Se propone un endpoint
  `POST /api/orders/lab/lb` que haga N llamadas `Diagnose` por gRPC y devuelva la distribución.
- **Detección de caídas en dos capas:** el TTL o el health check de Consul, y el health check
  del propio cliente gRPC. Hay que documentar los tiempos de cada una para que la prueba del
  §5.5 sea reproducible.
- **Estado del breaker por réplica:** si Orders se escala, cada réplica tiene su propio breaker.
  Se propone mantener Orders con una sola réplica en los experimentos.
- **Consul en modo `-dev`:** pierde el KV al reiniciarse. Se usará modo servidor con un volumen
  y un script de *seed* del KV.
- **Nombre del repositorio:** remoto `github.com/franbenaglia/go-microservices-lab`; el directorio
  local (`go-uservicios`) es la raíz.
- **Un solo `go.mod` en la raíz** para que los contratos y el código común sean simples (ADR-001).

## Actualización 2026-10-16 (Fase 5)

| Componente | Versión | Nota |
|---|---|---|
| go.opentelemetry.io/otel (+sdk, exporters OTLP gRPC) | v1.47.0 | contrib/otel/v3 declara 1.43; MVS usa 1.47 sin problemas |
| go.opentelemetry.io/contrib/instrumentation/runtime | v0.72.0 | métricas del runtime de Go |
| github.com/exaring/otelpgx | v0.13.0 | spans y métricas de pgx |
| otel/opentelemetry-collector-contrib | 0.162.0 | exportador prometheus incluido |
| jaegertracing/jaeger | 2.22.0 | **solo API /api/v3**; el datasource Jaeger de Grafana no funciona (ADR-020) |
| prom/prometheus | v3.15.0 | |
| grafana/grafana | 13.2.4 | |
