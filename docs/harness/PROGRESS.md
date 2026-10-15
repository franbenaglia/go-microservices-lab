# Estado actual

**Última actualización:** 2026-10-15 (sesión 1)
**Fase en curso:** Fase 4 terminada y verificada → siguiente: **Fase 5 (Observabilidad)**

## Próximo paso concreto
1. Revisar el código de `$(go env GOMODCACHE)/github.com/go-kratos/kratos/contrib/otel/v3@v3.0.0-20260626125723-668db92c2c00/`
   (`tracing/tracing.go`: Server/Client middleware; `metrics/metrics.go`; `log/`) antes de usarlo.
2. `pkg/telemetry`: TracerProvider + MeterProvider con exportadores OTLP (gRPC) hacia el Collector;
   resource con service.name, service.instance.id y service.version; propagador W3C tracecontext.
3. Middleware `tracing.Server/Client` y `metrics.Server/Client` en ambos servicios; spans de negocio
   (validación, consulta, reserva) y de PostgreSQL (otelpgx).
4. Métricas propias: estado y transiciones del breaker, rechazos, retries, distribución por instancia.
5. compose: otel-collector, jaeger, prometheus y grafana (dashboard aprovisionado).
6. Logs: extractor de trace_id/span_id en slog (`klog.WithExtractor`).

## Estado de la plataforma
- `make up`: postgres, consul, inventory (escalable) y orders, todos healthy. LAB_MODE=true en compose.
- Puertos en 127.0.0.1: orders 8081, consul 8500, postgres 55432, inventory efímero.
- Variables de compose: INVENTORY_BALANCER, INVENTORY_WEIGHT, BREAKER_ENABLED, BREAKER_KIND, RETRY_ENABLED, LAB_MODE.

## Bloqueos / preguntas abiertas
- Ninguno. Los cambios de las Fases 2, 3 y 4 están **sin commit**.

## Notas rápidas para retomar
- Fuentes de Kratos v3: `$(go env GOMODCACHE)/github.com/go-kratos/kratos/v3@v3.0.0/`
- Contrib v3 fijados a `v3.0.0-20260626125723-668db92c2c00`.
- Peticiones REST con `Content-Type: application/json`; los int64 se devuelven como cadenas (ADR-010).
- El `curl` de snap no lee archivos de /tmp: usar `--data-binary @-` con stdin.
- Laboratorios: `make smoke`, `make lab-discovery`, `make lab-lb`, `make lab-resilience` (utilidades en scripts/lab/lib.sh).
- `orders_with VAR=valor ...` (lib.sh) recrea Orders con otra configuración (siempre `--force-recreate`).
- Al editar con scripts python que tocan varios archivos, crear antes los directorios: un fallo a mitad deja cambios parciales.
