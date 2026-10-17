# Plan por fases

Leyenda: `[ ]` pendiente · `[~]` en curso · `[x]` hecho y verificado (con evidencia en SESSION_LOG).
Cada fase termina con: compilar → pruebas → verificación real → resumen → cómo probar → pendientes.

## Fase 0 — Análisis y harness
- [x] Leer la especificación y verificar las dependencias (FEASIBILITY.md)
- [x] Proponer arquitectura y decisiones iniciales (DECISIONS.md)
- [x] Crear el harness de documentación entre sesiones
- [x] Validar con el usuario: module path `github.com/franbenaglia/go-microservices-lab`, gobreaker v2 aceptado

## Fase 1 — Base funcional ✅ (2026-10-15)
- [x] Monorepo: `go.mod`, `Makefile`, `buf.yaml`/`buf.gen.yaml`, herramientas con `go tool`
- [x] Contratos `api/proto/inventory/v1` (CRUD, AdjustStock, GetProduct, CheckAvailability, ReserveStock, ReleaseStock, Diagnose) + errores
- [x] Contratos `api/proto/orders/v1` (Create, Get, GetStatus, List, Cancel) + errores
- [x] `pkg/`: configuración (YAML + `APP_*`), slog, `/livez` `/readyz`, apagado ordenado, códec protojson, pool pgx y goose
- [x] Inventory: biz, data (reserva transaccional, idempotencia, lápida), service, servidores HTTP y gRPC
- [x] Orders: biz (máquina de estados, compensación), data, cliente gRPC con endpoint fijo **temporal**
- [x] Migraciones goose para ambas bases
- [x] Pruebas unitarias y de integración (PostgreSQL, gRPC, concurrencia)
- [x] `compose.yaml`: postgres (2 bases, 2 usuarios), inventory y orders; imágenes distroless
- **Aceptación:** cumplida. `make test-integration` en verde y `make smoke` 17/17 OK. Ver docs/fases/fase-1.md.

## Fase 2 — Service Discovery ✅ (2026-10-15)
- [x] Consul 2.0.4 en compose (modo servidor, volumen, UI en 127.0.0.1:8500)
- [x] Registro con `contrib/registry/consul/v3`: metadatos, TTL, TCP y check HTTP `/readyz`, deregister-after 1m
- [x] Orders descubre `discovery:///inventory-service` (sin endpoint fijo en compose)
- [x] Endpoint de diagnóstico `POST /api/orders/lab/lb` (adelantado de la Fase 3)
- [x] Script `scripts/lab/discovery.sh` con los escenarios 1–6 del §5 (MODE=pause|kill|stop)
- [x] Pruebas de integración contra Consul (`pkg/discovery`)
- **Aceptación:** cumplida. `make lab-discovery` da 10/10 en los 3 modos; readiness con la base caída
  saca la instancia; `make test-integration` y `make smoke` en verde.

## Fase 3 — Load Balancing ✅ (2026-10-15)
- [x] Quitar el puerto fijo de inventory (efímero; hecho en la Fase 2)
- [x] `docker compose up --scale inventory=3`; el ID de instancia aparece en `Diagnose` y coincide con Consul
- [x] Endpoint `POST /api/orders/lab/lb {count, interval_ms}` → distribución, secuencia y balancer
- [x] Selector configurable (wrr por defecto, p2c, random) y pesos por instancia (`INSTANCE_WEIGHT`)
- [x] Script `scripts/lab/loadbalancing.sh` (`make lab-lb`): 3 réplicas, caída con tráfico continuo, pesos y comparación de selectores
- [x] Documentación del balanceo interno frente al externo (docs/fases/fase-3.md)
- **Aceptación:** cumplida. `make lab-lb` 19/19 (MODE=stop y kill); reparto 30/30/30; 0 errores
  durante la caída; peso 300 → 50%.
- Pendiente para la Fase 4: comparar p2c frente a wrr con latencia artificial en una réplica.

## Fase 4 — Resiliencia ✅ (2026-10-15)
- [x] Timeouts: presupuesto total por llamada (2s, envuelve los reintentos) + timeout por intento (600ms)
- [x] Middleware de retry (ADR-003): backoff exponencial, full jitter, máximo de intentos, deadline, allowlist; pruebas unitarias
- [x] Adaptador del breaker gobreaker → middleware oficial de Kratos (ADR-002/018), estado observable; pruebas de transiciones
- [x] Modo `sre` (breaker por defecto de Kratos) para comparar; sin estado inventado
- [x] Inyección de fallos en Inventory (ADR-008), desactivada por defecto, solo con LAB_MODE
- [x] Endpoints de laboratorio restringidos a LAB_MODE; `GET /api/orders/lab/resilience`
- [x] `scripts/lab/resilience.sh` (`make lab-resilience`): sin/con retries, sin/con breaker, ciclo de estados, latencia, p2c vs wrr
- **Aceptación:** cumplida. 19/19 (A, B y C repetidos 2 veces más: 15/15); regresión smoke 17/17, discovery 10/10, lb 19/19.

## Fase 5 — Observabilidad ✅ (2026-10-16)
- [x] OTel SDK (trazas y métricas) con `contrib/otel/v3`; W3C tracecontext; `X-Trace-Id` en las respuestas
- [x] Spans de negocio (validación, consulta, reserva, finalización, compensación) y de PostgreSQL (otelpgx, sin parámetros)
- [x] Collector → Jaeger y exportador Prometheus; Prometheus; Grafana con datasource y dashboard aprovisionados (20 paneles, anotaciones del breaker)
- [x] Logs slog con trace_id/span_id, servicio, instancia y operación
- [x] Métricas del breaker, retries, distribución por instancia, negocio, readiness, runtime y pool de PostgreSQL
- [x] `scripts/lab/observability.sh` (`make lab-observability`)
- **Aceptación:** cumplida. `make lab-observability` 29/29; traza completa Orders→Inventory→PostgreSQL;
  dashboard verificado visualmente (Playwright); regresión smoke 17, discovery 10, lb 19, resilience 19.
- Pendiente para la Fase 7: el span de Traefik en la traza (Cliente → Gateway → Orders → Inventory).
- El nivel de log dinámico queda para la Fase 6 (Consul KV); el `LevelVar` ya existe.

## Fase 6 — Configuración centralizada ✅ (2026-10-17)
- [x] Fuente `contrib/config/consul/v3` + observadores de Kratos; documento `config/<svc>/dynamic.yaml` sembrado con CAS
- [x] Recarga dinámica con validación completa y `atomic.Pointer` (sin carreras; test con -race)
- [x] Orders: nivel de log, timeout de llamadas (deadline dinámico), retry y breaker en caliente
- [x] Inventory: nivel de log y fallos de laboratorio centralizados (todas las réplicas)
- [x] `/admin/config` (GET/PUT) y `config_reloads_total` (+ panel en Grafana)
- [x] Tabla de parámetros dinámicos frente a los de reinicio (docs/fases/fase-6.md)
- [x] `scripts/lab/config.sh` (`make lab-config`); lib.sh adaptado (lo dinámico por /admin/config)
- **Aceptación:** cumplida. `make lab-config` 28/28; regresión smoke 17, discovery 10, lb 19,
  resilience 19, observability 29; 13 paquetes de pruebas en verde.

## Fase 7 — API Gateway y frontend ✅ (2026-10-17)
- [x] Traefik v3.7 con consulCatalog (tags en Consul) + file; rutas /api/orders, /api/products, /api/inventory, /admin/<svc>/*
- [x] Logs de acceso JSON, cabeceras de seguridad, ipAllowList (local-only) en lab y admin (verificado 403 desde red externa)
- [x] Tracing y métricas de Traefik (OTLP); traza Cliente → Gateway → Orders → Inventory → PostgreSQL
- [x] Sin puertos publicados en los microservicios; scripts por el gateway y `docker compose exec consul curl`
- [x] Endpoint `GET /api/orders/lab/overview` (Consul + Prometheus desde el servidor)
- [x] Frontend Svelte 5: Dashboard, Productos, Pedidos, LB Lab, CB Lab, Tracing, Configuración (verificado con Playwright, incluida la vista móvil)
- [x] `/admin/*` instrumentado con otelhttp (X-Trace-Id); fila de Traefik en Grafana
- [x] `scripts/lab/gateway.sh` (`make lab-gateway`), `make labs`, `make frontend-check`
- **Aceptación:** cumplida. Gateway 21/21; todos los labs en verde; 13 paquetes de Go; svelte-check 0/0.

## Fase 8 — Validación final
- [ ] Batería completa: unit, integración y resiliencia
- [ ] Verificación de todos los contenedores y de la recuperación tras fallos
- [ ] README en español (los 15 puntos del §19 + tabla Spring Cloud + diferencias conceptuales)
