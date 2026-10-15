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

## Fase 3 — Load Balancing
- [x] Quitar el puerto temporal 8082 de inventory (ahora efímero; hecho en la Fase 2)
- [ ] `docker compose up --scale inventory=3`; instance ID = hostname/UUID en `Diagnose`
- [x] Endpoint `POST /api/orders/lab/lb {count}` → distribución por instancia (hecho en la Fase 2)
- [ ] Selector configurable (wrr por defecto, p2c, random); requiere reinicio
- [ ] Script `scripts/lab/loadbalancing.sh` (incluye caída de una réplica)
- **Aceptación:** distribución visible en las 3 réplicas; al caer una, el tráfico se redistribuye sin errores sostenidos.

## Fase 4 — Resiliencia
- [ ] Timeouts por llamada y respeto del deadline
- [ ] Middleware de retry (ADR-003) con pruebas unitarias
- [ ] Adaptador del circuit breaker (ADR-002) con estado observable y pruebas de transiciones
- [ ] Inyección de fallos en Inventory (ADR-008), desactivada por defecto
- [ ] Script `scripts/lab/resilience.sh`: sin/con retries × sin/con breaker
- **Aceptación:** se observan CLOSED→OPEN→HALF-OPEN→CLOSED y la comparativa queda documentada.

## Fase 5 — Observabilidad
- [ ] OTel SDK (traces + metrics) con `contrib/otel/v3`; W3C tracecontext
- [ ] Spans de negocio (validación, consulta, reserva) y PostgreSQL (otelpgx)
- [ ] Collector → Jaeger y exportador Prometheus; Prometheus; Grafana con dashboard aprovisionado
- [ ] Logs slog con trace_id, servicio, instancia y operación; nivel dinámico
- **Aceptación:** traza completa Gateway→Orders→Inventory→DB en Jaeger; dashboard con datos antes, durante y después del fallo.

## Fase 6 — Configuración centralizada
- [ ] Source `contrib/config/consul/v3` + archivo local como base; seed del KV
- [ ] Recarga dinámica con validación y `atomic.Pointer` (sin carreras)
- [ ] Tabla de parámetros dinámicos y de los que requieren reinicio (docs)
- **Aceptación:** cambiar el timeout o el umbral del breaker en KV se refleja sin reinicio y los valores inválidos se rechazan.

## Fase 7 — API Gateway y frontend
- [ ] Traefik con el provider consulCatalog, rutas `/api/orders`, `/api/products`, `/api/inventory`, logs de acceso, cabeceras, ipAllowList para admin
- [ ] Frontend Svelte: Dashboard, Products, Orders, LB Lab, CB Lab, Tracing, Configuration
- **Aceptación:** todo el uso desde el navegador pasa por Traefik; los labs funcionan de punta a punta.

## Fase 8 — Validación final
- [ ] Batería completa: unit, integración y resiliencia
- [ ] Verificación de todos los contenedores y de la recuperación tras fallos
- [ ] README en español (los 15 puntos del §19 + tabla Spring Cloud + diferencias conceptuales)
