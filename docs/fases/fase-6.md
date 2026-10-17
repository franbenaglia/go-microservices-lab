# Fase 6: configuración centralizada (Consul KV)

## Qué se implementó

- **Proveedor de configuración de Kratos v3**: [pkg/dynconfig](../../pkg/dynconfig/dynconfig.go) usa
  la fuente oficial `contrib/config/consul/v3` con `config.New(WithSource(...))` y los observadores de
  Kratos (`config.Watch`).
- **Un documento por servicio**: `config/<servicio>/dynamic.yaml`. Si no existe, el primer servicio
  que arranca lo siembra con sus valores de arranque (CAS con índice 0, así que con varias réplicas
  solo una escribe).
- **Recarga sin reinicio, validada y atómica**: ante cada aviso se relee el documento entero, se
  valida y solo entonces se aplica. Si no es válido se rechaza completo y se conserva el anterior.
  La configuración vigente vive en `atomic.Pointer` y los componentes leen sin bloqueos.
- **`/admin/config`** en cada servicio (solo con `LAB_MODE=true`):
  - `GET` devuelve la configuración dinámica vigente, la estática sin secretos, la lista de
    parámetros que requieren reinicio, el estado de las recargas y el documento crudo de Consul (con
    aviso si está mal formado).
  - `PUT` recibe un JSON parcial, lo combina con la configuración vigente, lo valida y lo guarda en
    Consul (202). Todas las réplicas lo aplican al recibir el aviso del watcher.
- **Métrica** `config_reloads_total{result=applied|rejected}` y panel en Grafana.

```
PUT /admin/config ─► validar ─► Consul KV (config/<svc>/dynamic.yaml)
                                       │ watch (blocking query, keyprefix)
             ┌─────────────────────────┼──────────────────────────┐
         réplica 1                  réplica 2                 réplica N
   Scan → Validate → Apply     (mismo proceso en cada instancia: todo o nada)
```

## Parámetros dinámicos y de reinicio

| Servicio | Parámetro | Dinámico | Efecto al cambiar |
|---|---|---|---|
| Orders | `log.level` | ✅ | `slog.LevelVar` (inmediato) |
| Orders | `inventory.timeout` | ✅ | presupuesto total por llamada; lo aplica un middleware propio en lugar del `WithTimeout` fijo de Kratos |
| Orders | `resilience.retry.*` (enabled, intentos, backoff, attempt_timeout, operaciones) | ✅ | `Retrier.SetPolicy` (atómico; las llamadas en curso terminan con la política anterior) |
| Orders | `resilience.breaker.enabled` | ✅ | activa o desactiva el breaker (el estado se conserva) |
| Orders | `resilience.breaker.*` (umbrales, ventana, open_timeout, half_open) | ✅ | **se crea un breaker nuevo en CLOSED** (sin historial). Si no cambian, el breaker no se toca |
| Inventory | `log.level` | ✅ | inmediato |
| Inventory | `lab.faults` | ✅ | se aplica a **todas las réplicas**; solo si cambia en el documento |
| Ambos | direcciones, timeouts de servidor, `data.*` (DSN), `consul.*`, `telemetry.*` | ❌ | reinicio |
| Orders | `inventory.endpoint`, `inventory.balancer`, `compensation_timeout`, `resilience.breaker.kind` | ❌ | reinicio (`kind` no está en el documento dinámico) |
| Inventory | `INSTANCE_WEIGHT` | ❌ | reinicio (se publica al registrarse) |
| Ambos | `LAB_MODE` | ❌ | reinicio |

**Prioridad:** para los parámetros dinámicos manda el documento de Consul; el archivo y las
variables de entorno solo dan el valor con el que se siembra. Las credenciales **nunca** están en
Consul: el DSN llega por `APP_DB_DSN` y la vista estática no lo incluye.

## Validaciones

- Nivel de log: `debug|info|warn|error`.
- `inventory.timeout` ∈ (0, 30s]; con retry activo, `retry.attempt_timeout` < `inventory.timeout`.
- Breaker: `failure_ratio` ∈ [0,1], `window` ≥ 1s, `open_timeout` ≥ 100ms, `half_open_max_requests` > 0, y
  al menos un criterio de apertura. Con `kind=sre` no se puede cambiar `enabled` en caliente.
- Retry: `max_attempts` ∈ [1,10], 0 < `base_backoff` ≤ `max_backoff`.
- Fallos de laboratorio: latencias de 0 a 30000ms, `error_rate` ∈ [0,1], `error_code` 500 o 503.
- El `PUT` rechaza campos desconocidos (400), así que una errata no pasa desapercibida.

## Cómo probarlo

```bash
make lab-config        # 28 comprobaciones; restaura los documentos al terminar

curl -s localhost:8081/admin/config | jq '.dynamic, .status'
curl -s -X PUT localhost:8081/admin/config --data-binary @- <<<'{"log":{"level":"debug"}}'
curl -s -X PUT localhost:8081/admin/config --data-binary @- <<<'{"resilience":{"breaker":{"consecutive_failures":3}}}'
# Fallos para todas las réplicas de Inventory:
INV=http://$(docker compose port --index 1 inventory 8000)
curl -s -X PUT $INV/admin/config --data-binary @- <<<'{"lab":{"faults":{"latency_ms":300}}}'
# Ver o editar el documento directamente:
curl -s 'localhost:8500/v1/kv/config/orders-service/dynamic.yaml?raw'   # o en la UI de Consul, Key/Value
```

## Resultados (`make lab-config`, 28/28)

- `log.level=warn` deja de registrar las peticiones (INFO) y con `info` vuelven, sin reiniciar.
- `inventory.timeout` de 2s a 500ms con Inventory a 1s de latencia: las llamadas fallan a los 501ms;
  al volver a 2s terminan bien a los ~1001ms. El contenedor no se reinicia (mismo `StartedAt`).
- `consecutive_failures=3` y `open_timeout=2s` en caliente: abre tras 3 fallos y se recupera a los 2s.
- 3 `PUT` inválidos dan 400 y no se aplica nada. Una edición inválida directa en Consul se rechaza
  (`rejected`+1, con el error en `last_error`). El YAML mal formado se detecta en `GET`.
- `lab.faults.unavailable=true` en KV activa el fallo en las **dos** réplicas; al desactivarlo, 0 fallos.
- Regresión: smoke 17, discovery 10, lb 19, resilience 19 y observability 29, todo OK.

## Limitaciones y hallazgos

- **YAML mal formado editado a mano en Consul**: Kratos registra el error y no avisa a los
  observadores, así que el servicio sigue con la configuración anterior. El `GET /admin/config` lo
  detecta (`consul_problem`). Los `PUT` nunca producen YAML inválido.
- **Borrado de claves**: el watcher del contrib no propaga borrados y el merge de Kratos no elimina
  campos. Por eso el `PUT` escribe siempre el documento completo; un campo que falta toma el valor de
  arranque al releer.
- **El contrib hace `panic` si su *watch plan* termina con error** (no ocurre al perder Consul: el
  plan reintenta). Se acepta para el laboratorio.
- **Fallos centralizados frente a `/admin/faults`**: gana la última escritura. Un cambio en KV solo
  pisa los fallos de una réplica si cambia `lab.faults` (no al cambiar el nivel de log).
- **Sustituir el breaker** con peticiones en vuelo: sus resultados llegan al breaker nuevo, que los
  descarta. La imprecisión se limita al instante del cambio.
