#!/usr/bin/env bash
# Laboratorio de Load Balancing del lado cliente (main.md §6).
#
# Todo el tráfico va Orders -> Inventory por gRPC: el balanceo lo decide el
# cliente de Kratos con las instancias que publica Consul. No interviene
# ningún proxy (a diferencia del balanceo HTTP que hará Traefik en la Fase 7).
#
#   1. Tres réplicas: distribución con wrr (por defecto).
#   2. Caída de una réplica con tráfico continuo (MODE=stop|kill).
#   3. Balanceo ponderado: una instancia extra con weight=300.
#   4. Comparación de selectores: wrr, p2c y random (reinicia Orders).
set -euo pipefail
cd "$(dirname "$0")/../.."
source scripts/lab/lib.sh
MODE=${MODE:-stop}
SVC=inventory-service
tmp=$(mktemp -d)
heavy=lab-inventory-heavy
cleanup() {
  docker rm -f "$heavy" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

# Veces que la misma instancia atiende dos llamadas seguidas.
repeats() { jq '[.sequence as $s | range(1; $s|length) | select($s[.] == $s[. - 1])] | length' <<<"$1"; }

step "1. Tres réplicas de Inventory con wrr"
INVENTORY_BALANCER=wrr docker compose up -d --wait --scale inventory=3 inventory orders >/dev/null 2>&1
wait_for 60 3 consul_passing $SVC >/dev/null || true
check "instancias passing" "$(consul_passing $SVC)" 3
r=$(probe 90); print_probe "$r"
check "balancer activo" "$(jq -r .balancer <<<"$r")" wrr
check "las 3 instancias reciben tráfico" "$(jq '.by_instance|length' <<<"$r")" 3
check "reparto exacto 30/30/30" "$(jq '[.by_instance[].count] | unique' -c <<<"$r")" "[30]"
check "rotación estricta (sin repeticiones consecutivas)" "$(repeats "$r")" 0

step "2. Caída de una réplica con tráfico continuo ($MODE)"
victim=$(container inventory 3)
victim_id=$(instance_id_of "$victim")
info "300 llamadas cada 10ms; a los ~1s se detiene $victim [$victim_id]"
probe_bg() { curl -s -H 'Content-Type: application/json' -X POST "$ORDERS_URL/api/orders/lab/lb" -d '{"count":300,"interval_ms":10}' >"$tmp/during.json"; }
probe_bg &
pid=$!
sleep 1
if [[ $MODE == kill ]]; then docker kill "$victim" >/dev/null; else docker stop "$victim" >/dev/null; fi
wait $pid
r=$(cat "$tmp/during.json"); print_probe "$r"
served=$(jq --arg v "$victim_id" '[.sequence[] | select(. == $v)] | length' <<<"$r")
last=$(jq --arg v "$victim_id" '[.sequence | to_entries[] | select(.value == $v) | .key] | max // -1' <<<"$r")
info "la réplica caída atendió $served llamadas; la última fue la nº $((last + 1)) de 300"
check "sin errores durante la caída" "$(jq .failed <<<"$r")" 0
check_true "la réplica caída dejó de recibir tráfico antes del final" test "$last" -lt 290
wait_for 60 2 consul_passing $SVC >/dev/null || true
r=$(probe 60); print_probe "$r"
check "después: reparto entre las 2 restantes" "$(jq '.by_instance|length' <<<"$r")" 2
docker start "$victim" >/dev/null
wait_for 60 3 consul_passing $SVC >/dev/null || true
check "réplica reincorporada" "$(consul_passing $SVC)" 3

step "3. Balanceo ponderado: instancia extra con weight=300 (las demás 100)"
docker compose run -d --no-deps --name "$heavy" -e INSTANCE_WEIGHT=300 inventory >/dev/null
wait_for 60 4 consul_passing $SVC >/dev/null || true
heavy_id=$(instance_id_of "$heavy")
info "instancia pesada: $heavy_id"
curl -s "$CONSUL_URL/v1/health/service/$SVC?passing=true" | jq -r '.[] | "    \(.Service.ID) weight=\(.Service.Meta.weight // "100 (por defecto)")"'
r=$(probe 120); print_probe "$r"
check "la instancia pesada recibe 300/600 = 50% (60 de 120)" \
  "$(jq --arg h "$heavy_id" '[.by_instance[] | select(.instance_id == $h) | .count] | first' <<<"$r")" 60
docker rm -f "$heavy" >/dev/null
wait_for 60 3 consul_passing $SVC >/dev/null || true

for b in p2c random wrr; do
  step "4. Selector $b (Orders se reinicia: el selector es global al proceso)"
  INVENTORY_BALANCER=$b docker compose up -d --wait orders >/dev/null 2>&1
  r=$(probe 90); print_probe "$r"
  check "balancer activo" "$(jq -r .balancer <<<"$r")" "$b"
  check "usa las 3 instancias" "$(jq '.by_instance|length' <<<"$r")" 3
  check "sin fallos" "$(jq .failed <<<"$r")" 0
  info "repeticiones consecutivas: $(repeats "$r") (wrr: 0; random/p2c: >0 habitualmente)"
  info "secuencia: $(jq -r '[.sequence[0:12][] | .[0:6]] | join(" ")' <<<"$r") ..."
done

step "Limpieza: wrr y una instancia"
docker compose up -d --wait --scale inventory=1 inventory >/dev/null 2>&1
info "réplicas de inventory: $(docker compose ps inventory -q | wc -l)"

summary
