#!/usr/bin/env bash
# Laboratorio de configuración centralizada (main.md §9).
#
# Documentos en Consul KV: config/<servicio>/dynamic.yaml. Se cambian con
# PUT /admin/config (valida antes de guardar) o editando el KV directamente
# (los servicios validan de nuevo al recibir el aviso del watcher).
#
#   1. Documentos sembrados y estado de las recargas.
#   2. Nivel de log en caliente (warn → los logs "server request" desaparecen).
#   3. Timeout de las llamadas en caliente, sin reiniciar el contenedor.
#   4. Umbrales del breaker en caliente.
#   5. Validación: PUT inválido (400), edición inválida en Consul (rechazada),
#      YAML mal formado (detectado en GET).
#   6. Fallos de laboratorio centralizados: se aplican a TODAS las réplicas.
set -euo pipefail
cd "$(dirname "$0")/../.."
source scripts/lab/lib.sh
CONSUL=${CONSUL_URL:-http://127.0.0.1:8500}
SVC=inventory-service

started() { docker inspect -f '{{.State.StartedAt}}' "$(container "$1" 1)"; }
applied() { get_config "$1" | jq -r .status.applied; }
put_doc() { curl -s -X PUT "$CONSUL/v1/kv/config/$1/dynamic.yaml" --data-binary @- >/dev/null; } # stdin

# Estado inicial: se restaura al terminar.
orig_orders=$(get_config orders | jq -c .dynamic)
docker compose up -d --wait --scale inventory=1 inventory >/dev/null 2>&1
orig_inv=$(get_config inventory | jq -c .dynamic)
restore() {
  set_config orders "$orig_orders" >/dev/null || true
  set_config inventory "$orig_inv" >/dev/null || true
  clear_faults 2>/dev/null || true
}
trap restore EXIT

step "1. Documentos en Consul KV"
for s in orders-service inventory-service; do
  check_true "config/$s/dynamic.yaml existe" curl -sf "$CONSUL/v1/kv/config/$s/dynamic.yaml" -o /dev/null
done
info "$(get_config orders | jq -c '{status: .status | {applied, rejected}, restart_required: (.restart_required | length)}')"
check "sin secretos en la configuración estática" "$(get_config orders | jq -c '.static' | grep -ciE 'dsn|password' || true)" 0
orders_start=$(started orders)

step "2. Nivel de log en caliente"
a=$(applied orders)
check "PUT log.level=warn" "$(set_config orders '{"log":{"level":"warn"}}')" 202
wait_for 10 warn bash -c "curl -s $GATEWAY_URL/admin/orders/config | jq -r .dynamic.log.level" >/dev/null
check "aplicado por el watcher" "$(get_config orders | jq -r .dynamic.log.level)" warn
since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
probe 5 >/dev/null
n_warn=$(docker compose logs --since "$since" --no-log-prefix orders 2>&1 | grep -c '"msg":"server request"' || true)
set_config orders '{"log":{"level":"info"}}' >/dev/null
wait_for 10 info bash -c "curl -s $GATEWAY_URL/admin/orders/config | jq -r .dynamic.log.level" >/dev/null
since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
probe 5 >/dev/null
n_info=$(docker compose logs --since "$since" --no-log-prefix orders 2>&1 | grep -c '"msg":"server request"' || true)
check "con warn no se registran peticiones (INFO)" "$n_warn" 0
check_true "con info vuelven a registrarse ($n_info)" test "$n_info" -ge 1
check_true "recargas aplicadas: $(applied orders) (antes $a)" test "$(applied orders)" -ge $((a + 2))

step "3. Timeout de las llamadas en caliente (Inventory con 1s de latencia)"
set_faults '{"latency_ms":1000,"operations":["Diagnose"]}'
set_config orders '{"inventory":{"timeout":"500ms"},"resilience":{"breaker":{"enabled":false},"retry":{"attempt_timeout":"400ms"}}}' >/dev/null
wait_for 10 500ms bash -c "curl -s $ORDERS_URL/api/orders/lab/resilience | jq -r .call_timeout" >/dev/null
r=$(probe 3); print_probe "$r"
check "timeout=500ms: las llamadas fallan por timeout" "$(jq .failed <<<"$r")" 3
check_true "latencia ≈ 500ms ($(jq '.avg_latency_ms|floor' <<<"$r")ms)" test "$(jq '.avg_latency_ms|floor' <<<"$r")" -lt 700
set_config orders '{"inventory":{"timeout":"2s"},"resilience":{"retry":{"attempt_timeout":"1500ms"}}}' >/dev/null
wait_for 10 2s bash -c "curl -s $ORDERS_URL/api/orders/lab/resilience | jq -r .call_timeout" >/dev/null
r=$(probe 3); print_probe "$r"
check "timeout=2s: las llamadas terminan bien" "$(jq .failed <<<"$r")" 0
check "Orders no se reinició" "$(started orders)" "$orders_start"
clear_faults

step "4. Umbrales del breaker en caliente"
set_config orders '{"resilience":{"breaker":{"enabled":true,"consecutive_failures":3,"open_timeout":"2s"},"retry":{"enabled":false}}}' >/dev/null
wait_for 10 3 bash -c "curl -s $ORDERS_URL/api/orders/lab/resilience | jq -r .breaker.consecutive_failures_threshold" >/dev/null
check "consecutive_failures=3 aplicado" "$(resilience | jq -r .breaker.consecutive_failures_threshold)" 3
set_faults '{"unavailable":true}'
r=$(probe 5); clear_faults
check "abre tras 3 fallos (3 llegan, 2 rechazados)" "$(jq '[.errors[] | select(.reason=="CIRCUITBREAKER") | .count] | add // 0' <<<"$r")" 2
sleep 2.5
r=$(probe 5)
check "con open_timeout=2s se recupera" "$(breaker_state)" closed

step "5. Validación"
before=$(get_config orders | jq -c .dynamic)
check "PUT con failure_ratio=2 → 400" "$(set_config orders '{"resilience":{"breaker":{"failure_ratio":2}}}')" 400
check "PUT con campo desconocido → 400" "$(set_config orders '{"resilience":{"brekaer":{}}}')" 400
check "PUT con attempt_timeout >= timeout → 400" \
  "$(set_config orders '{"inventory":{"timeout":"1s"},"resilience":{"retry":{"enabled":true,"attempt_timeout":"1s"}}}')" 400
check "nada de lo anterior llegó a aplicarse" "$(get_config orders | jq -c .dynamic)" "$before"

rej=$(get_config orders | jq .status.rejected)
curl -s "$CONSUL/v1/kv/config/orders-service/dynamic.yaml?raw" | sed 's/level: "info"/level: "verbose"/' | put_doc orders-service
wait_for 10 $((rej + 1)) bash -c "curl -s $GATEWAY_URL/admin/orders/config | jq .status.rejected" >/dev/null
check "edición inválida en Consul: rechazada por el servicio" "$(get_config orders | jq .status.rejected)" $((rej + 1))
check "se mantiene la configuración anterior" "$(get_config orders | jq -c .dynamic)" "$before"
info "último error: $(get_config orders | jq -r .status.last_error)"

printf 'log: [sin cerrar\n' | put_doc orders-service
sleep 1
check_true "YAML mal formado: GET lo detecta" bash -c "curl -s $GATEWAY_URL/admin/orders/config | jq -r .consul_problem | grep -q 'mal formado'"
check "y la configuración vigente no cambia" "$(get_config orders | jq -c .dynamic)" "$before"
check "restaurar con PUT" "$(set_config orders "$before")" 202

step "6. Fallos de laboratorio centralizados (todas las réplicas)"
docker compose up -d --wait --scale inventory=2 inventory >/dev/null 2>&1
wait_for 60 2 consul_passing $SVC >/dev/null || true
check "PUT lab.faults.unavailable=true (Inventory)" "$(set_config inventory '{"lab":{"faults":{"unavailable":true}}}')" 202
active_all() { for i in 1 2; do faults_of "$i" | jq -r .active; done | xargs; }
wait_for 10 "true true" active_all >/dev/null || true
check "ambas réplicas con el fallo activo" "$(active_all)" "true true"
orders_with BREAKER_ENABLED=false RETRY_ENABLED=false
r=$(probe 10)
check "todas las llamadas fallan (FAULT_INJECTED)" "$(jq .failed <<<"$r")" 10
set_config inventory '{"lab":{"faults":{"unavailable":false}}}' >/dev/null
wait_for 10 "false false" active_all >/dev/null || true
r=$(probe 10)
check "desactivado en KV: ninguna falla" "$(jq .failed <<<"$r")" 0

step "Limpieza"
restore
trap - EXIT
orders_with BREAKER_ENABLED=true RETRY_ENABLED=true
docker compose up -d --wait --scale inventory=1 inventory >/dev/null 2>&1
info "documentos restaurados; 1 réplica de inventory"
summary
