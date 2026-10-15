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
