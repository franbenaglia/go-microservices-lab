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
**Estado:** Aceptada por el usuario (2026-10-15); se implementa en la Fase 4
- **Contexto:** el breaker por defecto de Kratos v3 es SRE adaptativo, sin HALF-OPEN y con
  opciones internas que no se pueden configurar.
- **Decisión:** usar `circuitbreaker.Client(circuitbreaker.WithBreakerFactory(f))`, donde `f`
  devuelve un adaptador sobre `sony/gobreaker/v2` (TwoStepCircuitBreaker) que implementa
  `Allow() error / MarkSuccess() / MarkFailed()`. El adaptador expone el estado y emite métricas
  y logs en `OnStateChange`. La configuración `breaker.kind: classic|sre` permite comparar ambos.
- **Alternativas:** escribir un breaker propio (más código que mantener) o usar Aegis (también
  SRE, sin HALF-OPEN).

## ADR-003 — Retry como middleware propio
**Estado:** Propuesta (Fase 4)
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
**Estado:** Propuesta (Fase 5)
- **Decisión:** usar el middleware `contrib/otel/v3/metrics` (Server y Client) más métricas
  propias (breaker, retries, instancia) con el SDK de OTel. Se exportan por OTLP al Collector,
  que las publica con el exportador `prometheus` (con `resource_to_telemetry_conversion` para
  conservar `service.instance.id`). Prometheus hace scrape del Collector.
- **Consecuencias:** un único pipeline de telemetría. Nada de etiquetas con IDs de pedido o usuario.

## ADR-005 — Traefik descubre servicios vía Consul Catalog
**Estado:** Propuesta (Fase 7)
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
**Estado:** Propuesta (Fase 4)
- **Decisión:** un middleware de servidor en Inventory (`internal/lab/faults`) que añade latencia,
  errores con cierta probabilidad o indisponibilidad (que también hace fallar `/readyz`). Solo se
  registra si `LAB_MODE=true`. Los parámetros se toman de Consul KV y de un endpoint admin. Los
  endpoints `/admin/*` quedan restringidos por un `ipAllowList` de Traefik a redes locales.

## ADR-009 — Frontend
**Estado:** Propuesta (Fase 7)
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
