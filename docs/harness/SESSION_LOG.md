# Bitácora de sesiones

Entrada por sesión, la más reciente arriba. Plantilla:

```
## Sesión N — AAAA-MM-DD — Fase X
**Hecho:** ...
**Verificación:** comandos ejecutados y resultado
**Decisiones:** ADR-xxx
**Problemas encontrados:** ...
**Siguiente:** ...
```

---

## Sesión 3 (continuación) — 2026-10-17 — Fase 7
**Hecho:** Traefik (traefik.yml, dynamic.yml, tags en consul.tags), sin puertos en los microservicios, lib.sh por el
gateway (incluster, wait_gateway por IP en la API de Traefik), endpoint overview (pkg/promquery + Consul health),
Telemetry.WrapHTTP para /admin/*, frontend Svelte 5 completo (7 secciones, BarList y gráfico del breaker en SVG),
Dockerfile node+nginx, fila de Traefik en Grafana, scripts/lab/gateway.sh, objetivos make labs, lab-gateway,
frontend-check y frontend-dev, docs/fases/fase-7.md.
**Verificación:**
- `make vet`; `make test-integration` 13 ok; `make frontend-check` (svelte-check 0 errores y 0 avisos, build ok).
- `make labs`: smoke 17, discovery 10, lb 19, resilience 19, observability 29, config 28, gateway 21.
- Playwright: Dashboard, pedido CONFIRMED con traza, LB 10/10/10 (Kratos 0,3ms; Traefik 3,0ms), ciclo del breaker,
  configuración aplicada sin reinicio (y bloqueada por validación nativa con un valor inválido), Tracing, móvil a 390px.
- Bajada de escala 3→1 con 300 peticiones por Traefik: 299×200 y 1×504.
**Decisiones:** ADR-024, ADR-025; ADR-005 y ADR-009 implementadas.
**Problemas encontrados:**
- npm del host roto → npm de node:24 en contenedor; TS 7 incompatible con svelte-check → TS 6.0.3.
- Carrera recreate ↔ Traefik (compose --wait no espera a Consul) → wait_gateway por IP; loadbalancing.sh recreaba Orders sin orders_with.
- Spans de Traefik llegan en otro lote → observability.sh espera a que aparezcan.
- IDs cortos de UUIDv7 (prefijo = marca de tiempo) → últimos 8 caracteres; etiquetas de estado solapadas en el SVG → por tramos.
- Playwright: cambiar solo el hash no recarga.
**Siguiente:** Fase 8 (validación final y README completo en español).

---

## Sesión 3 — 2026-10-17 — Fase 6
**Hecho:** pkg/dynconfig (Manager genérico sobre contrib/config/consul/v3, siembra con CAS, validación todo o nada,
/admin/config GET/PUT, detección de YAML mal formado, métrica), retry con política atómica (SetPolicy), resiliencia de
Orders reconfigurable (switchable breaker, deadline dinámico, Validate/Update), separación Breaker{Kind; BreakerParams},
Dynamic/Static/RestartRequired en las conf, integración en ambos main, lib.sh (orders_with por KV, get/set_config),
scripts/lab/config.sh, panel de recargas en Grafana, docs/fases/fase-6.md.
**Verificación:**
- Tests: dynconfig (integración con Consul: siembra, aplicación por watcher, rechazo, YAML mal formado, PUT inválido),
  resiliencia en caliente (swap del breaker solo si cambian los umbrales, activar/desactivar, deadline dinámico,
  validación, actualizaciones concurrentes con -race ×3), retry.SetPolicy; `make test-integration`: 13 paquetes ok.
- `make lab-config` 28/28; regresión: smoke 17, discovery 10, lb 19, resilience 19, observability 29.
**Decisiones:** ADR-022, ADR-023.
**Problemas encontrados:**
- Comillas en las claves del YAML generado → estilo solo en los valores.
- KV > entorno rompía `orders_with BREAKER_ENABLED=...` → ahora por /admin/config.
- breaker.kind en el documento dinámico habría invalidado el documento al cambiar el kind por entorno → fuera del documento.
- Kratos descarta en silencio el YAML mal formado → validación del valor crudo en GET.
**Siguiente:** Fase 7 (Traefik + frontend Svelte).

---

## Sesión 2 — 2026-10-16 — Fase 5
**Hecho:** pkg/telemetry (Setup, middlewares, X-Trace-Id, contador por instancia, service_ready), spans de negocio en
ambos biz, otelpgx (spans y pool), extractor trace_id en slog, métricas de breaker y retry (observables) y de negocio,
Collector, Jaeger, Prometheus y Grafana en compose, dashboard generado (build-dashboard.py), scripts/lab/observability.sh,
scripts/lab/trace-tree.jq, docs/fases/fase-5.md.
**Verificación:**
- `make vet`; `make test-integration`: 11 paquetes ok (nuevos tests de telemetry: X-Trace-Id, contador por instancia, traceparent W3C).
- `make lab-observability` 29/29: traza de 30+ spans Orders→Inventory→PostgreSQL, logs con trace_id en ambos servicios,
  11 métricas presentes, job/instance correctos, sin etiquetas de IDs, dashboard aprovisionado.
- Las 20 consultas del dashboard devuelven datos; captura con Playwright tras un episodio de fallo (estados del breaker y anotaciones).
- Regresión: smoke 17/17, discovery 10/10, lb 19/19, resilience 19/19.
**Decisiones:** ADR-019 (sustituye a ADR-004), ADR-020, ADR-021.
**Problemas encontrados:**
- Jaeger 2.22 sin API heredada → datasource de Grafana eliminado (ADR-020).
- resource.Merge con schema URL distinta → NewSchemaless.
- Panel state-timeline: los mapeos por valor no se aplicaban → mapeos por rango.
- Series de instancias recreadas → metric_expiration 1m.
- Timeouts de Docker Hub al descargar imágenes → reintentos.
- El curl de snap tampoco escribe en /tmp; Playwright solo escribe dentro del repo (.playwright-mcp/, en .gitignore).
**Siguiente:** Fase 6 (configuración centralizada en Consul KV).

---

## Sesión 1 (continuación 4) — 2026-10-15 — Fase 4
**Hecho:** pkg/resilience/breaker (adaptador gobreaker v2.4.0), pkg/resilience/retry, services/orders/internal/resilience,
config `resilience:` en Orders, inyección de fallos en Inventory (internal/lab, /admin/faults), LAB_MODE,
`GET /api/orders/lab/resilience`, utilidades en lib.sh, scripts/lab/resilience.sh + `make lab-resilience`, docs/fases/fase-4.md.
**Verificación:**
- Pruebas unitarias: breaker (7, incluida la integración con el middleware de Kratos), retry (11), fallos (6); `go test -race` del módulo completo en verde.
- `make lab-resilience`: 19/19; escenarios aleatorios A/B/C repetidos: 15/15 ×2; modo sre verificado.
- Regresión: smoke 17/17, lab-discovery 10/10, lab-lb 19/19.
**Decisiones:** ADR-018; ADR-002, 003 y 008 pasan a implementadas.
**Problemas encontrados:**
- Primera ejecución 10 OK / 7 FAIL por interacciones reales: (1) umbral consecutive=5 demasiado
  sensible con retries; (2) con ratio 0.5 el breaker compartido se abría con 1 de 2 réplicas rotas;
  (3) `docker compose up` no recreaba Orders y el estado del breaker se arrastraba → `--force-recreate`.
- Ciclo de importaciones server↔service → paquete internal/resilience.
- Un script python a mitad de camino reescribió server.go antes de fallar → se revisó el estado y se recreó el archivo (sin pérdida).
**Siguiente:** Fase 5 (Observabilidad).

---

## Sesión 1 (continuación 3) — 2026-10-15 — Fase 3
**Hecho:** selector configurable (`inventory.balancer`, `server.SetBalancer`), pesos por instancia
(`INSTANCE_WEIGHT` → metadato `weight`), campo `balancer` en ProbeInventory, `INVENTORY_BALANCER` e
`INVENTORY_WEIGHT` en compose, `scripts/lab/loadbalancing.sh` + `make lab-lb`, pruebas unitarias del
selector, docs/fases/fase-3.md.
**Verificación:**
- `make vet` OK; `make test-integration`: 7 paquetes ok.
- `make lab-lb` 19/19 con MODE=stop y MODE=kill: 30/30/30 con rotación estricta; caída con 300 llamadas
  continuas sin errores; weight=300 → 60/120; p2c 51/29/10; random 33/30/27.
- Regresión: `make smoke` 17/17, `make lab-discovery` 10/10.
**Decisiones:** ADR-017.
**Problemas encontrados:** orden de init() del balancer de Kratos (documentado); p2c sin carga no reparte de forma uniforme (esperado).
**Siguiente:** Fase 4 (Resiliencia: circuit breaker con gobreaker, timeouts, retries, inyección de fallos).

---

## Sesión 1 (continuación 2) — 2026-10-15 — Fase 2
**Hecho:** Consul 2.0.4 en compose; `pkg/discovery` (registry + check de readiness + Setup); registro en
ambos servicios; Orders con `discovery:///inventory-service`; `orders.v1.LabService` (ProbeInventory);
Inventory con puerto efímero; `scripts/lab/lib.sh` + `scripts/lab/discovery.sh`; pruebas contra Consul;
docs/fases/fase-2.md.
**Verificación:**
- `make vet` OK; `make test-integration`: 6 paquetes ok (incluye 5 tests de pkg/discovery).
- `make lab-discovery` con MODE=pause, kill y stop: 10 OK / 0 FAIL en cada uno.
- Manual: con postgres detenido, inventory sigue healthy pero /readyz=503 y Consul lo excluye en ~1s
  (vuelve en ~4s); con todas las instancias caídas, 503 inmediato y pedido REJECTED; recuperación en ~1s.
- `make smoke`: 17/17 a través de discovery.
**Decisiones:** ADR-015 (registro y descubrimiento), ADR-016 (LabService adelantado, puerto efímero); ADR-006 aceptada.
**Problemas encontrados:**
- El contrib no publica la lista vacía (documentado).
- Consul 2.0 deja `Output` vacío en los checks HTTP con conexión rechazada → la prueba negativa espera varios intervalos.
- El curl de snap no lee /tmp.
- Dependencia circular registry ↔ cliente gRPC ↔ servidor HTTP → se crea primero el servidor HTTP.
**Siguiente:** Fase 3 (Load Balancing).

---

## Sesión 1 (continuación) — 2026-10-15 — Fase 1
**Hecho:** module path `github.com/franbenaglia/go-microservices-lab` y remoto `origin`; gobreaker v2
aceptado (ADR-002). Fase 1 completa: contratos proto, pkg común, Inventory, Orders, migraciones,
pruebas, Dockerfiles distroless, compose, Makefile, scripts/smoke.sh y docs/fases/fase-1.md.
**Verificación:**
- `go vet ./...`, `go tool buf lint`: sin errores.
- `make test-integration`: 5 paquetes ok con `-race` (7 tests de repositorio, incluida la concurrencia 60→20; 2 de gRPC; 1 de traducción de errores).
- `make up`: postgres, inventory y orders healthy. `make smoke`: 17 OK, 0 FAIL.
- Manual: el usuario `inventory` no puede conectarse a la base `orders` ("no CONNECT privilege"); los datos se
  conservan tras `docker compose restart`; con inventory detenido, el pedido queda REJECTED/INVENTORY_UNAVAILABLE
  y orders sigue ready; al volver inventory, el pedido se confirma; los logs muestran el apagado ordenado.
**Decisiones:** ADR-010 (protojson), ADR-011 (flujo y compensación, lápida), ADR-012 (céntimos),
ADR-013 (distroless + healthcheck), ADR-014 (schema aislado en tests).
**Problemas encontrados:**
- Kratos v3 serializa los proto con encoding/json → se resolvió con pkg/httpcodec.
- `curl -d` envía form-urlencoded → hay que documentar el Content-Type.
- El mensaje de rechazo filtraba la IP interna → ahora solo muestra el código y la razón.
- Carrera en un fake de test → se corrigió con atomic.
**Siguiente:** Fase 2 (Consul).

---

## Sesión 1 — 2026-10-15 — Fase 0
**Hecho:** lectura de main.md; verificación de dependencias en proxy.golang.org y en las fuentes
de Kratos v3.0.0 y contrib v3; análisis de viabilidad; propuesta de ADR-001..009; creación del harness
(CLAUDE.md, docs/harness/*, comandos `/retomar` y `/cerrar-sesion`, hook SessionStart).
**Verificación:** `curl proxy.golang.org/.../@latest` para cada módulo; `go mod download` de
kratos/v3 y de los contrib consul/registry, consul/config y otel; inspección del código
(`middleware/`, `internal/circuitbreaker/sre.go`, `transport/grpc/client.go`, `selector/`).
**Hallazgos:** el breaker SRE no tiene HALF-OPEN y sus opciones son internas; no hay
middleware de retry; tracing y metrics se movieron a `contrib/otel/v3`; los contrib no tienen tags.
**Siguiente:** validación del usuario y arranque de la Fase 1.
