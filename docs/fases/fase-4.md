# Fase 4: resiliencia (timeouts, retries, circuit breaker e inyección de fallos)

## Qué se implementó

| Pieza | Dónde | Resumen |
|---|---|---|
| Circuit breaker clásico | [pkg/resilience/breaker](../../pkg/resilience/breaker/breaker.go) | adaptador de `sony/gobreaker/v2` (CLOSED/OPEN/HALF-OPEN) a la interfaz del middleware **oficial** `circuitbreaker.Client(WithBreakerFactory(...))` de Kratos; estado, contadores e historial de transiciones observables |
| Retry | [pkg/resilience/retry](../../pkg/resilience/retry/retry.go) | middleware cliente de Kratos: backoff exponencial con full jitter, máximo de intentos, timeout por intento, respeto del deadline original y lista de operaciones seguras |
| Configuración | [services/orders/internal/resilience](../../services/orders/internal/resilience/resilience.go) + `resilience:` en el config de Orders | `classic` (gobreaker) o `sre` (breaker por defecto de Kratos, sin estado visible) |
| Inyección de fallos | [services/inventory/internal/lab](../../services/inventory/internal/lab/faults.go) | latencia y jitter, tasa de errores (500/503), indisponibilidad, readiness fallida y filtro por operación; solo con `LAB_MODE=true` |
| Diagnóstico | `GET /api/orders/lab/resilience` | estado del breaker, transiciones, contadores de retry y configuración |
| Laboratorio | `make lab-resilience` ([scripts/lab/resilience.sh](../../scripts/lab/resilience.sh)) | 5 escenarios con comprobaciones automáticas |

`LAB_MODE` (por defecto `true` en compose y `false` en el código) controla `/admin/faults` en
Inventory y `/api/orders/lab/*` en Orders. Sin ese modo no se registran.

## Cadena del cliente Orders → Inventory

```
CreateOrder ─► [timeout total 2s ─ envuelve toda la cadena (WithTimeout de Kratos)]
                 metadata → logging → retry ──► circuit breaker ──► [timeout 600ms por intento] ──► gRPC
                                     ▲  │              │
                                     │  └ reintenta solo 503/504 de operaciones seguras
                                     └──── no reintenta CIRCUITBREAKER (circuito abierto)
```

- **Timeout total** (`inventory.timeout=2s`): lo aplica el interceptor de Kratos *alrededor* de
  todos los middlewares, así que incluye los reintentos y sus esperas. Nunca se supera.
- **Timeout por intento** (`retry.attempt_timeout=600ms`): un intento lento se corta y se reintenta
  mientras quede presupuesto. Debe ser menor que el total (se valida al arrancar).
- **Retry por fuera del breaker** (el orden de Resilience4j: `Retry(CircuitBreaker(call))`): cada
  intento cuenta para el breaker y, con el circuito abierto, el retry no insiste.
- **Un breaker por dependencia** (`inventory-service`), no uno por operación: la fábrica devuelve
  siempre la misma instancia.

## Operaciones que se reintentan

| Operación | ¿Se reintenta? | Por qué |
|---|---|---|
| GetProduct, CheckAvailability, Diagnose | sí | lecturas |
| ReleaseStock | sí | idempotente por diseño (liberar dos veces no tiene efecto; lápida por clave) |
| ReserveStock | **solo con `idempotency_key`** | la clave garantiza que el reintento devuelve la misma reserva (ADR-011) |
| CreateProduct, UpdateProduct, AdjustStock | no | no son idempotentes |

Solo se reintentan 503 (Unavailable) y 504 (timeout). Nunca los errores de negocio (404, 409) ni
el rechazo del breaker (`reason=CIRCUITBREAKER`).

## Resultados (`make lab-resilience`, 19/19)

| Escenario | Sin protección | Con protección |
|---|---|---|
| A. 30% de errores transitorios | 28% de fallos | **3%** con retries (≈0.3³); breaker cerrado |
| B. 1 de 2 réplicas responde 503 | 50% de fallos | **0%** con retries (el reintento va a la otra réplica); breaker cerrado |
| C. Inventory caído, 50 llamadas | **150** peticiones llegan al servicio caído (3 intentos × 50) | **10** llegan; el resto se rechaza localmente |
| C. Ciclo del breaker | — | `closed→open` → (5s) `open→half-open→open` (prueba fallida) → (5s, recuperado) `half-open→closed` |
| D. Latencia de 3s (presupuesto de 2s) | cada llamada tarda 2000ms (504) | media de **207ms**: tras unos intentos el circuito abre y falla en ~0ms |
| E. Una réplica con 200ms extra | wrr: media de 101ms (reparto 20/20) | p2c: media de **5ms** (39/1: evita la réplica lenta) |

Con `BREAKER_KIND=sre` el breaker de Kratos también rechaza localmente, pero no expone su estado.
`/api/orders/lab/resilience` devuelve `state_observable=false` en lugar de inventar uno.

## Interacciones entre timeouts, retries y breaker (lo aprendido)

1. **Los retries amplifican lo que ve el breaker.** Como cada intento cuenta, con
   `consecutive_failures=5` un 30% de errores transitorios abría el circuito (en ~260 intentos una
   racha de 5 fallos es probable), y entonces los retries ya no podían recuperar nada. Con 10
   fallos seguidos la probabilidad es despreciable (0.3¹⁰ ≈ 6·10⁻⁶).
2. **Un breaker compartido puede convertir un fallo parcial en una caída total.** Con 1 de 2
   réplicas rota, wrr alterna también entre intentos y ~50% de los intentos fallan, aunque los
   retries hacen que el 100% de las llamadas tenga éxito. Con `failure_ratio=0.5` el circuito se
   abría y cortaba también el tráfico a la réplica sana. Con 0.6 no ocurre. Alternativas: breaker
   por instancia, o sacar las instancias rotas por readiness (Fase 2).
3. **Con el circuito abierto el retry no insiste**: `CIRCUITBREAKER` no es reintentable, así que una
   llamada rechazada cuesta ~0ms en lugar de 3 intentos.
4. **El breaker protege sobre todo a la dependencia**: con un fallo inmediato (503), sin breaker la
   latencia ya es baja, pero cada llamada genera 3 peticiones contra un servicio caído (150 frente a
   10). Con fallos lentos (timeout), el breaker además recorta la latencia (2000ms → ~200ms).
5. **HALF-OPEN**: con `half_open_max_requests=3` pasan 3 peticiones de prueba (el resto se rechaza);
   si las 3 van bien se cierra, y si una falla vuelve a OPEN otros 5s.

## Parámetros

| Parámetro | Defecto | Variable | Reinicio |
|---|---|---|---|
| `inventory.timeout` | 2s | `APP_INVENTORY_TIMEOUT` | sí (dinámico en la Fase 6) |
| `resilience.retry.enabled` / `max_attempts` | true / 3 | `RETRY_ENABLED` / `APP_RETRY_MAX_ATTEMPTS` | sí |
| `retry.base_backoff` / `max_backoff` / `attempt_timeout` | 50ms / 400ms / 600ms | `APP_RETRY_*` | sí |
| `resilience.breaker.enabled` / `kind` | true / classic | `BREAKER_ENABLED` / `BREAKER_KIND` | sí |
| `breaker.failure_ratio` / `min_requests` / `consecutive_failures` | 0.6 / 10 / 10 | `APP_BREAKER_*` | sí |
| `breaker.window` / `open_timeout` / `half_open_max_requests` | 10s / 5s / 3 | `APP_BREAKER_*` | sí |

## Cómo probarlo a mano

```bash
INV=http://$(docker compose port inventory 8000)
curl -s -X PUT $INV/admin/faults --data-binary @- <<<'{"unavailable":true}'
curl -s -H 'Content-Type: application/json' -X POST localhost:8081/api/orders/lab/lb -d '{"count":20}' | jq '.errors'
curl -s localhost:8081/api/orders/lab/resilience | jq '.breaker | {state, transitions}'
curl -s -X DELETE $INV/admin/faults
# otras simulaciones: {"latency_ms":3000} {"error_rate":0.3,"error_code":500}
#                     {"fail_readiness":true} {"latency_ms":200,"operations":["Diagnose"]}
```

## Limitaciones

- Los fallos se configuran **por réplica** (`/admin/faults` de cada instancia). En la Fase 6 se
  centralizarán en Consul KV para aplicarlos a todas a la vez.
- El adaptador del breaker comparte los callbacks de gobreaker entre peticiones en vuelo (la
  interfaz de Kratos no asocia resultado y petición). La imprecisión se limita a las peticiones en
  vuelo durante un cambio de estado (ver el comentario del paquete).
- Las métricas del breaker y de los retries van por ahora en el endpoint de diagnóstico. En la
  Fase 5 se exportan a Prometheus.
