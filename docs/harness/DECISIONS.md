# Registro de decisiones (ADR)

Formato: contexto → decisión → alternativas descartadas → consecuencias.
Estado: `Propuesta` (pendiente de validar en la fase correspondiente), `Aceptada` o `Reemplazada por ADR-x`.

---

## ADR-001 — Versiones y estructura de módulos
**Estado:** Aceptada (2026-10-15)
- **Decisión:** Kratos `v3.0.0` y contrib fijados a `v3.0.0-20260626125723-668db92c2c00`, con un
  único `go.mod` en la raíz (`module github.com/franbenaglia/go-microservices-lab`, repo remoto https://github.com/franbenaglia/go-microservices-lab). Los servicios
  viven en `services/<svc>`, los contratos en `api/proto` y el código generado en `api/gen/go`.
- **Alternativas:** un `go.mod` por servicio con `go.work`. Da más independencia pero también
  más fricción para un laboratorio.
- **Consecuencias:** los servicios comparten las versiones de las dependencias. El código común
  va en `pkg/` (observabilidad, health, config), sin lógica de negocio compartida.

## ADR-002 — Circuit breaker clásico vía `WithBreakerFactory`
**Estado:** Aceptada e implementada (2026-10-15, Fase 4). Detalles en ADR-018
- **Contexto:** el breaker por defecto de Kratos v3 es SRE adaptativo, sin HALF-OPEN y con
  opciones internas que no se pueden configurar.
- **Decisión:** usar `circuitbreaker.Client(circuitbreaker.WithBreakerFactory(f))`, donde `f`
  devuelve un adaptador sobre `sony/gobreaker/v2` (TwoStepCircuitBreaker) que implementa
  `Allow() error / MarkSuccess() / MarkFailed()`. El adaptador expone el estado y emite métricas
  y logs en `OnStateChange`. La configuración `breaker.kind: classic|sre` permite comparar ambos.
- **Alternativas:** escribir un breaker propio (más código que mantener) o usar Aegis (también
  SRE, sin HALF-OPEN).

## ADR-003 — Retry como middleware propio
**Estado:** Aceptada e implementada (2026-10-15, Fase 4). Orden real: `metadata → logging → retry → breaker`; ReleaseStock se considera idempotente y ReserveStock solo con clave
- **Decisión:** el middleware cliente `pkg/resilience/retry` aplica backoff exponencial con full
  jitter y un máximo de intentos, nunca supera el deadline del `ctx` y solo reintenta códigos
  transitorios (Unavailable, DeadlineExceeded por intento). Usa una allowlist de operaciones:
  `GetProduct`, `CheckAvailability` y `Diagnose` siempre; `ReserveStock`/`ReleaseStock` solo si
  llevan clave de idempotencia.
- **Orden de middleware (cliente):** `tracing → metrics → retry → circuitbreaker → timeout por intento`.
  Así cada intento cuenta para el breaker y, si el breaker está abierto, el retry no insiste:
  `ErrNotAllowed` no es reintentable.
- **Alternativa:** la retryPolicy de la service config de gRPC. Se descarta porque no es
  observable ni pasa por el breaker de Kratos.

## ADR-004 — Métricas: OTel → Collector → Prometheus
**Estado:** Reemplazada por ADR-019 (implementada en la Fase 5)
- **Decisión:** usar el middleware `contrib/otel/v3/metrics` (Server y Client) más métricas
  propias (breaker, retries, instancia) con el SDK de OTel. Se exportan por OTLP al Collector,
  que las publica con el exportador `prometheus` (con `resource_to_telemetry_conversion` para
  conservar `service.instance.id`). Prometheus hace scrape del Collector.
- **Consecuencias:** un único pipeline de telemetría. Nada de etiquetas con IDs de pedido o usuario.

## ADR-005 — Traefik descubre servicios vía Consul Catalog
**Estado:** Aceptada e implementada (Fase 7, ver ADR-024)
- **Decisión:** usar el provider `consulCatalog` de Traefik. Los servicios se registran con tags
  `traefik.*` y Traefik no necesita el socket de Docker.
- **Alternativa:** el provider Docker, que requiere montar `/var/run/docker.sock`.

## ADR-006 — Health checks y readiness
**Estado:** Aceptada e implementada (Fases 1–2). Consul registra los checks TTL, TCP y HTTP `/readyz`; Docker usa `/livez`
- **Decisión:** endpoints HTTP `/livez` (proceso vivo) y `/readyz` (ping a la base de datos y
  dependencias críticas), además de `grpc.health.v1` (lo trae Kratos). En Consul, el check HTTP
  sobre `/readyz` se registra con `WithServiceCheck` y `WithDeregisterCriticalServiceAfter`.
  El healthcheck de Docker usa `/livez`.

## ADR-007 — Persistencia
**Estado:** Aceptada e implementada (2026-10-15, Fase 1)
- **Decisión:** `pgx/v5` (pool), migraciones con `goose` embebidas (`embed.FS`) y ejecutadas al
  arrancar (opción desactivable), y una sola instancia de PostgreSQL con dos bases (`orders`,
  `inventory`) y usuarios distintos. La reserva de stock se hace con
  `UPDATE ... SET available = available - $n, reserved = reserved + $n WHERE id=$id AND available >= $n`
  dentro de una transacción, más una tabla `reservations` con `idempotency_key UNIQUE`.

## ADR-008 — Inyección de fallos aislada
**Estado:** Aceptada e implementada (2026-10-15, Fase 4). Configuración por réplica en `/admin/faults`; se centralizará en Consul KV en la Fase 6
- **Decisión:** un middleware de servidor en Inventory (`internal/lab/faults`) que añade latencia,
  errores con cierta probabilidad o indisponibilidad (que también hace fallar `/readyz`). Solo se
  registra si `LAB_MODE=true`. Los parámetros se toman de Consul KV y de un endpoint admin. Los
  endpoints `/admin/*` quedan restringidos por un `ipAllowList` de Traefik a redes locales.

## ADR-009 — Frontend
**Estado:** Aceptada e implementada (Fase 7, ver ADR-025)
- **Decisión:** Svelte 5 + TypeScript + Vite, servido como estático por un contenedor ligero
  detrás de Traefik en `/`. Solo consume `/api/*`.

## ADR-010 — JSON de la API REST con protojson
**Estado:** Aceptada (2026-10-15, Fase 1)
- **Contexto:** en Kratos v3 el códec `json` es `encoding/json` de la stdlib. Con mensajes proto,
  un `Timestamp` sale como `{"seconds":..}`, los enums como números y los `oneof` no funcionan.
- **Decisión:** `pkg/httpcodec` registra en cada servidor HTTP un `RequestDecoder` y un
  `ResponseEncoder` que usan `protojson` (con `UseProtoNames` y `EmitUnpopulated`) cuando el payload
  es un `proto.Message` y el cuerpo es JSON. El resto delega en los códecs por defecto de Kratos.
  Los errores siguen con `DefaultErrorEncoder` (`{"code","reason","message","metadata"}`).
- **Alternativas:** `contrib/encoding/json/v3` (códec de compatibilidad con v2, que sustituye el
  `json` global). Se descarta porque la guía de migración recomienda elegir el códec de forma explícita.
- **Consecuencias:** según el mapeo JSON de proto3, los `int64` (por ejemplo `price_cents`) se
  serializan como **cadenas** (`"4999"`) y los enums por nombre (`ORDER_STATUS_CONFIRMED`). El
  frontend debe convertir esos valores. Las peticiones deben enviar `Content-Type: application/json`
  (`curl -d` envía `x-www-form-urlencoded` por defecto).

## ADR-011 — Flujo de creación de pedidos y compensación
**Estado:** Aceptada (2026-10-15, Fase 1)
- **Flujo:** validar → `INSERT` en PENDING → `GetProduct` por línea (copia de precio, SKU y nombre)
  → `CheckAvailability` → `ReserveStock(key="order:<id>")` → `Finalize` (un `UPDATE` condicional
  `WHERE status='PENDING'`) a CONFIRMED o REJECTED.
- Los rechazos de negocio **no son errores HTTP**: se devuelve el pedido con `status=REJECTED` y un
  `rejection_code` estable (`INSUFFICIENT_STOCK`, `PRODUCT_NOT_FOUND`, `INVENTORY_UNAVAILABLE`,
  `TOTAL_OUT_OF_RANGE`). Solo la entrada inválida (400) y los fallos de persistencia son errores.
- **Reserva incierta** (Unavailable o timeout en `ReserveStock`): Orders llama a `ReleaseStock` por
  clave con un contexto propio (`context.WithoutCancel` + timeout). Si la reserva aún no existe,
  Inventory guarda una **lápida** RELEASED con esa clave, así que una reserva que llegue tarde con la
  misma clave no descuenta stock.
- **Cancelación:** si el pedido está CONFIRMED, primero se libera el stock. Si Inventory no responde,
  se devuelve 503 y el pedido no cambia. Si el pedido se cancela mientras se crea, `Finalize` falla y
  se libera la reserva.
- **Límite conocido:** si también falla la liberación compensatoria, queda una reserva activa sin
  pedido confirmado. Mitigación futura: un reconciliador periódico (no está en el alcance de la v1).
- La readiness de Orders depende **solo** de su base de datos. Si Inventory cae, Orders sigue en el
  balanceo y rechaza pedidos de forma controlada.

## ADR-012 — Importes en céntimos
**Estado:** Aceptada (2026-10-15, Fase 1)
- Precios e importes como `int64` en céntimos (`price_cents`, `total_cents`), sin coma flotante. El
  total se calcula con detección de desbordamiento.

## ADR-013 — Imágenes distroless con healthcheck integrado
**Estado:** Aceptada (2026-10-15, Fase 1)
- **Decisión:** imagen final `gcr.io/distroless/static-debian12:nonroot`, sin shell, como usuario no root.
  El healthcheck de Docker ejecuta el propio binario: `/app/<svc> -healthcheck http://127.0.0.1:8000/livez`.
  El build con contexto en la raíz usa cachés de BuildKit para los módulos y la compilación.

## ADR-014 — Pruebas de integración con schema aislado
**Estado:** Aceptada (2026-10-15, Fase 1)
- `pkg/postgres/pgtest` crea un schema aleatorio por test (`search_path`), aplica las migraciones y
  lo borra al terminar. Las pruebas se omiten si no existe `LAB_TEST_PG_DSN`, así que `make test`
  funciona sin Docker y `make test-integration` usa el PostgreSQL de compose (base `labtest`).
  No se usa testcontainers, para no añadir dependencias.

## ADR-015 — Registro y descubrimiento en Consul
**Estado:** Aceptada (2026-10-15, Fase 2)
- **Decisión:** `pkg/discovery.Setup` crea el registry de `contrib/registry/consul/v3` con heartbeat
  TTL, checks TCP (los añade el contrib) y un check HTTP `readiness` sobre `/readyz` de la IP real
  de la instancia. `health_interval=5s` y `deregister_after=1m` (mínimo de Consul). El mismo registry
  sirve como Registrar (registro propio) y Discovery (cliente gRPC de Orders).
- **Orden de construcción:** el servidor HTTP se crea primero (solo con salud), porque el registry
  necesita su endpoint y el cliente gRPC necesita el registry. Las APIs se registran después.
- **Consul 2.0.4** en modo servidor con volumen (no `-dev`). Sin ACLs ni TLS (laboratorio local).
- **Alternativas:** un check gRPC de Consul (`grpc.health.v1`). Se descarta porque Kratos marca
  SERVING al arrancar y no refleja las dependencias; `/readyz` sí.
- **Limitación del contrib:** no publica la lista vacía cuando todas las instancias caen (ver docs/fases/fase-2.md).

## ADR-016 — Endpoint de diagnóstico de balanceo en Orders
**Estado:** Aceptada (2026-10-15, adelantada a la Fase 2)
- `orders.v1.LabService/ProbeInventory` (`POST /api/orders/lab/lb`) hace N llamadas `Diagnose`
  consecutivas con el **mismo** cliente gRPC de negocio y devuelve la distribución, los errores, las
  latencias y la secuencia. Se adelantó porque la prueba del §5 necesita ver qué instancia atiende.
- Vive en `internal/service/lab.go`, fuera del dominio. En la Fase 4 se restringirá a modo laboratorio.
- **Puerto de Inventory en el host:** efímero (`127.0.0.1::8000`) para poder escalar. Los scripts lo
  obtienen con `docker compose port --index N inventory 8000`.

## ADR-017 — Selector de balanceo configurable y pesos por instancia
**Estado:** Aceptada (2026-10-15, Fase 3)
- **Decisión:** `inventory.balancer` (`wrr` por defecto, `p2c`, `random`) se aplica con
  `selector.SetGlobalSelector` en el main de Orders, antes de crear el cliente. Es global al proceso,
  así que cambiarlo requiere reiniciar (documentado). Inventory publica el metadato `weight` desde
  `INSTANCE_WEIGHT`, y wrr lo usa como peso del nodo.
- **Hallazgo:** el balancer gRPC de Kratos captura el selector global en su `init()`. Funciona porque
  `transport/http` se inicializa antes y fija wrr (ver docs/fases/fase-3.md). Hay pruebas unitarias
  que fijan el comportamiento (`services/orders/internal/server/balancer_test.go`).
- **Alternativas:** filtros de nodos por llamada (`grpc.WithNodeFilter`), más potentes, pero no son
  una "estrategia" de balanceo; se reservan para posibles experimentos de versión o zona.

## ADR-018 — Detalles del breaker y umbrales por defecto
**Estado:** Aceptada (2026-10-15, Fase 4)
- **Adaptador:** pool de callbacks `done` de `gobreaker.TwoStepCircuitBreaker`. Los `MarkFailed`
  que Kratos emite tras un rechazo se descuentan con un contador y no cuentan como fallos.
  Imprecisión acotada a las peticiones en vuelo durante una transición.
- **Un breaker por dependencia** (no por operación): la fábrica devuelve una instancia compartida,
  que es la que se observa en `/api/orders/lab/resilience`.
- **Umbrales:** `consecutive_failures=10`, `failure_ratio=0.6`, `min_requests=10`, `window=10s`,
  `open_timeout=5s`, `half_open_max_requests=3`. Se midió que con 5 / 0.5 un 30% de errores
  transitorios o 1 de 2 réplicas rotas abrían el circuito, porque cada intento cuenta (retry por
  fuera) (docs/fases/fase-4.md).
- **`kind: sre`** usa `circuitbreaker.Client()` sin fábrica. Su estado no se expone
  (`state_observable=false`), sin inventar una API.
- **Endpoints de laboratorio** (`/api/orders/lab/*`, `/admin/faults`) solo con `LAB_MODE=true`
  (cumple el "restringidos a desarrollo" de ADR-016). Traefik los filtrará en la Fase 7.

## ADR-019 — Telemetría: OTel SDK → Collector → Jaeger/Prometheus
**Estado:** Aceptada e implementada (2026-10-16, Fase 5). Sustituye a ADR-004
- **Decisión:** los servicios exportan trazas y métricas por OTLP/gRPC al Collector
  (`otel/opentelemetry-collector-contrib:0.162.0`). Las trazas van a Jaeger 2.22 (OTLP) y las
  métricas al exportador `prometheus` del Collector (:8889, `metric_expiration: 1m`), que Prometheus
  3.15 lee con `honor_labels`, de modo que `job=service.name` e `instance=service.instance.id`.
- Middlewares oficiales `contrib/otel/v3` (tracing, metrics) con un histograma propio (1ms–5s).
  Además: cabecera `X-Trace-Id`, contador por instancia de destino (`selector.Peer`), métricas
  observables de breaker y retry, métricas de negocio y `service_ready`.
- Spans de negocio con la API `otel.Tracer` en la capa biz (neutral, no-op sin proveedor) y spans de
  PostgreSQL con otelpgx (sin parámetros, sin spans de acquire).
- Logs: el extractor de slog añade `trace_id` y `span_id` solo si hay un span válido. Los logs
  siguen saliendo por stdout (`docker compose logs`) y no se envían por OTLP.
- **Alternativas:** el exportador Prometheus en cada servicio (pull directo). Se descarta para tener
  un único pipeline y no publicar más puertos.

## ADR-020 — Grafana sin datasource Jaeger
**Estado:** Aceptada (2026-10-16, Fase 5)
- **Contexto:** Jaeger 2.22 solo sirve `/api/v3` (OTLP JSON), y el datasource Jaeger de Grafana
  13.2 consulta `/api/services`, que da 404.
- **Decisión:** no se aprovisiona el datasource. El dashboard enlaza a la UI de Jaeger, y el frontend
  (Fase 7) usará `X-Trace-Id` para enlazar a `/trace/<id>`. La API v3 sirve también para los scripts
  (`scripts/lab/trace-tree.jq`).
- **Alternativa futura:** fijar una versión de Jaeger con la API heredada, o usar Tempo como backend
  de Grafana.

## ADR-021 — Dashboard de Grafana generado
**Estado:** Aceptada (2026-10-16, Fase 5)
- El JSON `deployments/grafana/dashboards/lab-overview.json` se genera con
  `deployments/grafana/build-dashboard.py` (`make dashboard`). Es más legible y revisable que editar
  el JSON a mano. Se aprovisiona en modo de solo lectura (`allowUiUpdates: false`).

## ADR-022 — Configuración dinámica con la fuente Consul de Kratos
**Estado:** Aceptada e implementada (2026-10-17, Fase 6)
- **Decisión:** `pkg/dynconfig.Manager[T]` usa `contrib/config/consul/v3` + `config.Watch` sobre un único
  documento YAML por servicio (`config/<svc>/dynamic.yaml`). Ante cada aviso relee el documento entero,
  lo valida (`Validate(prev, next)`) y lo aplica (`Apply(prev, next)`) bajo un mutex. Lo vigente se
  guarda en `atomic.Pointer`. Si no es válido se rechaza completo.
- **Siembra** con CAS (índice 0) desde los valores de arranque. **Escritura** por `PUT /admin/config`
  (JSON parcial + `DisallowUnknownFields` + validación) que guarda el documento completo.
- **Prioridad:** KV > archivo/entorno para los parámetros dinámicos; el entorno solo siembra.
- **Alternativas:** una clave por parámetro (más avisos, sin atomicidad entre parámetros
  relacionados, como timeout y attempt_timeout); `consul watch` propio (se descarta porque el
  requisito es usar el proveedor de Kratos).

## ADR-023 — Qué es dinámico y cómo se aplica
**Estado:** Aceptada (2026-10-17, Fase 6)
- Orders: nivel de log, `inventory.timeout` (middleware de deadline propio en lugar del
  `grpc.WithTimeout` fijo de Kratos, ahora con valor 0), retry (`Retrier.SetPolicy` atómico) y breaker
  (envoltorio `switchable`: enabled atómico; con umbrales nuevos se crea un breaker nuevo en CLOSED,
  solo si cambian). `breaker.kind` sale del documento dinámico (`Breaker{Kind; BreakerParams}`).
- Inventory: nivel de log y `lab.faults` para todas las réplicas (se aplica solo si cambian).
- Los scripts de laboratorio cambian lo dinámico por `/admin/config` (`orders_with` en lib.sh) y
  recrean el contenedor solo para lo que requiere reinicio.

## ADR-024 — API Gateway con Traefik (consulCatalog + file)
**Estado:** Aceptada e implementada (2026-10-17, Fase 7). Implementa ADR-005
- Traefik v3.7.15. Provider `consulCatalog` (`exposedByDefault: false`, `strictChecks: [passing]`):
  las rutas de los servicios son tags `traefik.*` declarados en `consul.tags` de su configuración.
  Provider `file` para el frontend y los middlewares compartidos (`local-only`, `api-headers`,
  `security-headers`).
- Rutas: `/api/orders` y `/api/orders/lab` (local-only) → orders; `/api/products` y `/api/inventory`
  → inventory; `/admin/<svc>/*` → `/admin/*` (replacepathregex, local-only). `/readyz` y
  `/admin/faults` no se exponen.
- Solo Traefik (8080 y dashboard 8090) y las herramientas publican puertos en 127.0.0.1; los
  microservicios no. Los scripts acceden a réplicas concretas con `docker compose exec consul curl`.
- Tracing y métricas OTLP de Traefik → Collector. El navegador inicia en Traefik la traza que
  continúan los servicios.

## ADR-025 — Frontend Svelte sin dependencias de ejecución
**Estado:** Aceptada e implementada (2026-10-17, Fase 7). Implementa ADR-009
- Svelte 5.57 + TypeScript 6.0.3 (svelte-check 4.7 no admite TS 7) + Vite 8.3, router por hash y
  gráficos en SVG propio (sin librerías). Se compila en `node:24.22.0-alpine` (`npm ci && check &&
  build`) y se sirve con `nginx:1.31.7-alpine` en :8080. Mismo origen que la API: sin CORS.
- El dashboard usa un endpoint de diagnóstico propio, `GET /api/orders/lab/overview`: Orders consulta
  Consul (instancias y checks) y Prometheus (`lab.prometheus_url`) desde el servidor. El navegador
  no accede a la infraestructura ni a sus credenciales.
- Los fallos del Circuit Breaker Lab se guardan en Consul KV (`/admin/inventory/config`) para que
  afecten a todas las réplicas.
