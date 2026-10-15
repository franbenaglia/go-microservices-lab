#!/usr/bin/env bash
# Laboratorio de resiliencia (main.md §7 y §8): timeouts, retries y circuit breaker.
#
# Configuración por defecto de Orders (services/orders/configs/config.yaml):
#   inventory.timeout=2s (presupuesto total por llamada, retries incluidos)
#   retry: 3 intentos, backoff 50ms..400ms con full jitter, 600ms por intento
#   breaker classic: abre con 10 intentos fallidos seguidos o >=60% en 10s (mín. 10);
#                    5s en OPEN; 3 pruebas en HALF-OPEN
#
# Los fallos se inyectan en Inventory con /admin/faults (LAB_MODE=true).
# Escenarios: A transitorios | B réplica rota | C ciclo del breaker | D latencia | E p2c vs wrr
# Uso: ./scripts/lab/resilience.sh [A B C D E]
set -euo pipefail
cd "$(dirname "$0")/../.."
source scripts/lab/lib.sh
SCENARIOS=${*:-A B C D E}
SVC=inventory-service
DEFAULTS=(BREAKER_ENABLED=true RETRY_ENABLED=true INVENTORY_BALANCER=wrr)
trap 'clear_faults 2>/dev/null || true' EXIT

replicas() {
  docker compose up -d --wait --scale inventory="$1" inventory >/dev/null 2>&1
  wait_for 60 "$1" consul_passing $SVC >/dev/null || true
  clear_faults
}

scenario_A() {
  step "A. Errores transitorios (error_rate=0.3, 1 réplica): sin y con retries"
  replicas 1
  set_faults '{"error_rate":0.3}'
  orders_with "${DEFAULTS[@]}" RETRY_ENABLED=false
  r=$(probe 200); info "sin retries:"; print_probe "$r"; a=$(fail_pct "$r")
  orders_with "${DEFAULTS[@]}"
  r=$(probe 200); info "con retries (3 intentos):"; print_probe "$r"; b=$(fail_pct "$r")
  info "reintentos: $(resilience | jq -c '.retry | {calls, retries, recovered, exhausted}')"
  check_true "sin retries falla ~30% (obtenido ${a}%)" test "$a" -ge 20 -a "$a" -le 40
  check_true "con retries falla <8% (≈0.3³=2.7%; obtenido ${b}%)" test "$b" -lt 8
  check "un 30% de fallos no abre el breaker (umbral 60%)" "$(breaker_state)" closed
}

scenario_B() {
  step "B. Una de dos réplicas responde 503 (sigue registrada en Consul)"
  replicas 2
  set_faults '{"unavailable":true}' 2
  orders_with "${DEFAULTS[@]}" RETRY_ENABLED=false
  r=$(probe 100); info "sin retries:"; print_probe "$r"; a=$(fail_pct "$r")
  orders_with "${DEFAULTS[@]}"
  r=$(probe 100); info "con retries (el reintento va a la otra réplica):"; print_probe "$r"
  check_true "sin retries falla ~50% (obtenido ${a}%)" test "$a" -ge 40 -a "$a" -le 60
  check "con retries no falla ninguna" "$(jq .failed <<<"$r")" 0
  check "el breaker (compartido por la dependencia) sigue cerrado" "$(breaker_state)" closed
  info "intentos fallidos registrados por el breaker: $(resilience | jq -r .breaker.failures) de $(resilience | jq -r .breaker.allowed)"
  info "(~50% de los INTENTOS fallan: con failure_ratio=0.5 el breaker abriría y convertiría"
  info " un fallo parcial enmascarado por los retries en una caída total; por eso 0.6)"
}

scenario_C() {
  step "C. Indisponibilidad total: protección de Inventory con y sin breaker"
  replicas 1
  set_faults '{"unavailable":true}'
  orders_with "${DEFAULTS[@]}" BREAKER_ENABLED=false
  before=$(injected_total); r=$(probe_interval 50 20); off=$(( $(injected_total) - before ))
  info "sin breaker: 50 llamadas → $off peticiones llegan a Inventory caído (3 intentos cada una)"
  orders_with "${DEFAULTS[@]}"
  before=$(injected_total); r=$(probe_interval 50 20); on=$(( $(injected_total) - before ))
  info "con breaker: 50 llamadas → $on peticiones llegan a Inventory; el resto se rechaza localmente"
  check_true "el breaker reduce la carga sobre la dependencia caída ($on vs $off)" test "$on" -lt $((off / 3))
  clear_faults

  step "C. Ciclo del breaker: CLOSED → OPEN → HALF-OPEN → OPEN → HALF-OPEN → CLOSED"
  orders_with "${DEFAULTS[@]}"
  check "estado inicial" "$(breaker_state)" closed
  set_faults '{"unavailable":true}'
  r=$(probe_interval 20 50); print_probe "$r"
  check "el breaker se abre" "$(breaker_state)" open
  check_true "las llamadas posteriores se rechazan localmente (CIRCUITBREAKER)" \
    test "$(jq '[.errors[] | select(.reason == "CIRCUITBREAKER") | .count] | add // 0' <<<"$r")" -gt 0
  order=$(curl -s -H 'Content-Type: application/json' -X POST "$ORDERS_URL/api/orders" \
    -d '{"items":[{"product_id":"0199aaaa-0000-7000-8000-000000000001","quantity":1}]}')
  check "un pedido con el circuito abierto se rechaza al instante" "$(jq -r .rejection_code <<<"$order")" INVENTORY_UNAVAILABLE

  info "esperando open_timeout (5s) con Inventory todavía caído..."
  sleep 5.5
  r=$(probe 1)
  check "en HALF-OPEN la prueba falla y vuelve a OPEN" "$(breaker_state)" open

  clear_faults
  info "Inventory recuperado; esperando open_timeout (5s)..."
  sleep 5.5
  r=$(probe 10); print_probe "$r"
  check "tras 3 pruebas correctas en HALF-OPEN se cierra" "$(breaker_state)" closed
  check "sin fallos tras la recuperación" "$(jq .failed <<<"$r")" 0
  info "transiciones:"
  resilience | jq -r '.breaker.transitions[] | "    \(.at[11:23])  \(.from) → \(.to)"'
  check "secuencia de estados" "$(resilience | jq -r '[.breaker.transitions[] | "\(.from)>\(.to)"] | join(",")')" \
    "closed>open,open>half-open,half-open>open,open>half-open,half-open>closed"
  info "rechazos locales acumulados: $(resilience | jq -r .breaker.rejected)"
}

scenario_D() {
  step "D. Latencia excesiva (3s en Inventory; presupuesto por llamada 2s)"
  replicas 1
  set_faults '{"latency_ms":3000}'
  orders_with "${DEFAULTS[@]}" BREAKER_ENABLED=false RETRY_ENABLED=false
  t0=$SECONDS; r=$(probe 5); t1=$((SECONDS - t0))
  info "sin breaker ni retries (5 llamadas, ${t1}s):"; print_probe "$r"
  avg_off=$(jq '.avg_latency_ms|floor' <<<"$r")
  orders_with "${DEFAULTS[@]}"
  t0=$SECONDS; r=$(probe 30); t2=$((SECONDS - t0))
  info "con retries (600ms por intento) y breaker (30 llamadas, ${t2}s):"; print_probe "$r"
  avg_on=$(jq '.avg_latency_ms|floor' <<<"$r")
  check_true "sin protección cada llamada agota el timeout (~2000ms; media ${avg_off}ms)" test "$avg_off" -ge 1900
  check "con breaker el circuito se abre" "$(breaker_state)" open
  check_true "con breaker la latencia media cae drásticamente (${avg_on}ms < 500ms)" test "$avg_on" -lt 500
  info "timeout por intento: los fallos son DEADLINE (504); no se reintenta más allá del presupuesto de 2s"
}

scenario_E() {
  step "E. Balanceo frente a una réplica lenta (200ms en la réplica 2): wrr vs p2c"
  replicas 2
  set_faults '{"latency_ms":200}' 2
  orders_with "${DEFAULTS[@]}" INVENTORY_BALANCER=wrr
  r=$(probe 40); info "wrr:"; print_probe "$r"; w=$(jq '.avg_latency_ms|floor' <<<"$r")
  orders_with "${DEFAULTS[@]}" INVENTORY_BALANCER=p2c
  r=$(probe 40); info "p2c:"; print_probe "$r"; p=$(jq '.avg_latency_ms|floor' <<<"$r")
  check_true "p2c evita la réplica lenta (media p2c ${p}ms < wrr ${w}ms / 2)" test "$p" -lt $((w / 2))
}

for s in $SCENARIOS; do "scenario_$s"; done

step "Limpieza"
clear_faults
orders_with "${DEFAULTS[@]}"
docker compose up -d --wait --scale inventory=1 inventory >/dev/null 2>&1
info "fallos desactivados, Orders con la configuración por defecto, 1 réplica"
summary
