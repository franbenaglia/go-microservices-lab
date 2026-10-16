# Fase 5: observabilidad (OpenTelemetry, Jaeger, Prometheus, Grafana)

## Arquitectura

```
orders / inventory ──OTLP/gRPC :4317──► OpenTelemetry Collector ──OTLP──► Jaeger 2.22   (trazas)  :16686
   (SDK OTel Go 1.47)                                         └──► :8889 ◄── Prometheus 3.15 (métricas) :9090
   logs JSON (slog) con trace_id ──► docker compose logs                     ▲
                                                                     Grafana 13.2 (dashboard) :3000
```

- **Un único pipeline**: los servicios solo hablan OTLP con el Collector (ADR-019). El Collector
  traduce `service.name → job` y `service.instance.id → instance` para Prometheus.
- **Propagación W3C Trace Context** (`traceparent`) entre HTTP, gRPC y, en la Fase 7, Traefik. El
  muestreo es `ParentBased(TraceIDRatio(1.0))` en el laboratorio.

## Qué se implementó

| Pieza | Dónde |
|---|---|
| Proveedores de trazas y métricas, resource y propagador | [pkg/telemetry/telemetry.go](../../pkg/telemetry/telemetry.go) |
| Middlewares de Kratos (`contrib/otel/v3`: tracing y metrics), cabecera `X-Trace-Id` y contador por instancia | [pkg/telemetry/middleware.go](../../pkg/telemetry/middleware.go) |
| Spans de negocio: `order.create`, `order.validate`, `order.persist_pending`, `order.price_lookup`, `order.check_availability`, `order.reserve_stock`, `order.finalize`, `order.compensate_reservation`; `inventory.check_availability`, `inventory.reserve_stock`, `inventory.release_stock` | `services/*/internal/biz` |
| Spans y métricas de PostgreSQL (`otelpgx`): consulta sin parámetros, pool | [pkg/postgres](../../pkg/postgres/postgres.go) |
| Métricas de breaker y retries (instrumentos observables) | [services/orders/internal/resilience/metrics.go](../../services/orders/internal/resilience/metrics.go) |
| Métricas de negocio y de salud (`service_ready`) | capa `service` y `pkg/telemetry` |
| Logs con `trace_id`/`span_id` (extractor de slog) | [pkg/platform/logger.go](../../pkg/platform/logger.go) |
| Collector, Prometheus y Grafana (datasource y dashboard aprovisionados) | [deployments/](../../deployments) |

### Cadenas de middlewares

```
servidor: recovery → tracing → X-Trace-Id → métricas → metadata → logging → [fallos de laboratorio]
cliente:  tracing → métricas → instancia → metadata → logging → retry → circuit breaker → gRPC
```

Tracing y métricas van antes que logging para que los logs lleven `trace_id`. En el cliente van
por fuera de retry y breaker: miden la **llamada lógica**, y los intentos se cuentan en las métricas
de resiliencia.

## Métricas principales

| Métrica | Etiquetas | Qué mide |
|---|---|---|
| `server_requests_code_total`, `server_requests_seconds` (histograma) | job, instance, kind, operation, code, reason | peticiones, RPS, errores HTTP/gRPC y latencia |
| `client_requests_code_total`, `client_requests_seconds` | job, operation, code, reason | llamadas Orders → Inventory (tras reintentos) |
| `client_requests_by_instance_total` | target_service, target_instance, code | distribución del balanceo por instancia |
| `circuit_breaker_state` (0/1/2), `circuit_breaker_rejected_total`, `circuit_breaker_calls_total`, `circuit_breaker_transitions_total` | breaker, result, from, to | breaker |
| `client_retry_events_total` | event | attempts, retries, recovered, exhausted, not_retryable |
| `orders_processed_total` | status, rejection_code | pedidos por resultado |
| `inventory_reservations_total`, `inventory_releases_total` | result | reservas y liberaciones |
| `service_ready` | job, instance | readiness (1/0) |
| `go_memory_used_bytes`, `go_goroutine_count`, `pgxpool_*`, `db_client_operation_duration_seconds` | job, instance | recursos del proceso y de la base de datos |

Ninguna etiqueta lleva IDs de pedido o de usuario. El ID de instancia sí, porque su cardinalidad es baja.
Las latencias usan histogramas con buckets de 1ms a 5s (más que el presupuesto de 2s y la latencia
simulada de 3s).

## Cómo probarlo

```bash
make up
make lab-observability          # 29 comprobaciones: traza, logs, métricas y dashboard
make lab-resilience ARGS=C      # genera un episodio de fallo para verlo en Grafana
```

- **Jaeger**: http://127.0.0.1:16686, servicio `orders-service`, operación `/orders.v1.OrdersService/CreateOrder`.
  Cada respuesta HTTP incluye `X-Trace-Id`, que se busca directamente en la UI.
- **Grafana**: http://127.0.0.1:3000 (lectura anónima; admin con `GRAFANA_ADMIN_PASSWORD`). El
  dashboard "Visión general" anota automáticamente cada cambio de estado del breaker (líneas rojas),
  para comparar el antes, el durante y el después de un fallo.
- **Prometheus**: http://127.0.0.1:9090.
- **Logs**: `docker compose logs --no-log-prefix orders | jq 'select(.trace_id=="<id>")'`.
- **Árbol de una traza en terminal**: `curl -s localhost:16686/api/v3/traces/<id> | jq -r -f scripts/lab/trace-tree.jq`.

## Atributos de las trazas y datos sensibles

- Spans RPC: `rpc.system`, `rpc.service`, `rpc.method`, `rpc.status_code`, `peer.service`, tamaños de
  mensaje (no su contenido). Spans de negocio: `order.id`, `order.items`, `order.status`,
  `order.rejection_code`, `inventory.lines`, `inventory.replayed`, `error.type`. Resource:
  `service.name`, `service.instance.id`, `service.version`.
- SQL: el texto de la consulta con placeholders (`$1`), **nunca los parámetros**.
- **Cuidado:** el middleware `logging` de Kratos escribe `args` con `String()` del mensaje proto, es
  decir, con todo el contenido de la petición. Hoy ningún contrato lleva datos sensibles. Si se añaden,
  el mensaje debe implementar `Redact() string` (interfaz `logging.Redacter`).

## Hallazgos

- **Jaeger 2.22 solo expone la API `/api/v3`.** La API heredada `/api/services`, que usa el datasource
  Jaeger de Grafana, devuelve 404, así que el datasource se quitó y el dashboard enlaza a la UI de
  Jaeger (ADR-020).
- **`resource.Merge` falla si los dos resources declaran schema URLs distintas.** El resource propio
  se crea sin schema (`NewSchemaless`).
- **El histograma por defecto del contrib** llega solo hasta 1s; se sustituye por uno de 1ms a 5s.
- **HALF-OPEN sin tráfico**: gobreaker calcula el estado al leerlo. Tras `open_timeout` aparece como
  HALF-OPEN aunque no llegue ninguna petición, y solo pasa a CLOSED con 3 pruebas correctas.
- **Instancias recreadas**: sus series siguen exportándose hasta `metric_expiration` (1m) del Collector.
