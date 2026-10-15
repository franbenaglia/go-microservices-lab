# Estado actual

**Última actualización:** 2026-10-15 (sesión 1)
**Fase en curso:** Fase 3 terminada y verificada → siguiente: **Fase 4 (Resiliencia)**

## Próximo paso concreto
1. Leer `$(go env GOMODCACHE)/github.com/go-kratos/kratos/v3@v3.0.0/middleware/circuitbreaker/` y
   `go get github.com/sony/gobreaker/v2@v2.4.0`; revisar la API de TwoStepCircuitBreaker
   (Allow() → done(success bool)).
2. `pkg/resilience/breaker`: adaptador gobreaker → `circuitbreaker.CircuitBreaker`
   (Allow/MarkSuccess/MarkFailed), con el estado observable y `OnStateChange` (ADR-002).
   Ojo: `MarkSuccess`/`MarkFailed` no reciben contexto; hay que mapear el `done` de gobreaker por llamada.
3. `pkg/resilience/retry`: middleware cliente (ADR-003) con backoff exponencial, jitter, máximo de intentos,
   deadline y allowlist de operaciones.
4. Inventory: middleware de inyección de fallos (ADR-008) solo con `LAB_MODE=true`.
5. Endpoints de laboratorio solo en modo laboratorio; `scripts/lab/resilience.sh`.

## Estado de la plataforma
- `make up`: postgres, consul, inventory (escalable) y orders, todos healthy.
- Puertos en 127.0.0.1: orders 8081, consul 8500, postgres 55432, inventory efímero.
- Variables de compose: `INVENTORY_BALANCER` (wrr|p2c|random) e `INVENTORY_WEIGHT`.

## Bloqueos / preguntas abiertas
- Ninguno. Los cambios de las Fases 2 y 3 están **sin commit**.

## Notas rápidas para retomar
- Fuentes de Kratos v3: `$(go env GOMODCACHE)/github.com/go-kratos/kratos/v3@v3.0.0/`
- Contrib v3 fijados a `v3.0.0-20260626125723-668db92c2c00`.
- Peticiones REST con `Content-Type: application/json`; los int64 se devuelven como cadenas (ADR-010).
- El `curl` de snap no lee archivos de /tmp: usar `--data-binary @-` con stdin.
- Laboratorios: `make lab-discovery`, `make lab-lb`, `make smoke` (scripts/lab/lib.sh tiene las utilidades).
