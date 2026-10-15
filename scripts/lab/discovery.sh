#!/usr/bin/env bash
# Laboratorio de Service Discovery (main.md §5).
#
#   1. Iniciar una instancia de Inventory.
#   2. Iniciar una segunda instancia.
#   3. Comprobar que Consul registra ambas.
#   4. Detener (fallar) una instancia.
#   5. Comprobar que deja de recibir tráfico cuando Consul actualiza su salud.
#   6. Reiniciarla y verificar que se reincorpora.
#
# MODE controla cómo falla la instancia del paso 4:
#   pause (por defecto) congela el proceso: el puerto TCP sigue aceptando
#                       conexiones, pero /readyz no responde. Solo los health
#                       checks de Consul la sacan del balanceo.
#   kill                SIGKILL: el proceso desaparece sin desregistrarse.
#   stop                apagado ordenado: se desregistra de Consul al instante.
set -euo pipefail
cd "$(dirname "$0")/../.."
source scripts/lab/lib.sh
MODE=${MODE:-pause}
SVC=inventory-service

step "1. Una sola instancia de Inventory"
docker compose up -d --wait --scale inventory=1 inventory >/dev/null 2>&1
check "instancias passing en Consul" "$(wait_for 30 1 consul_passing $SVC >/dev/null; consul_passing $SVC)" 1
r=$(probe 10); print_probe "$r"
check "todas las respuestas desde una instancia" "$(jq '.by_instance|length' <<<"$r")" 1

step "2. Iniciar una segunda instancia"
docker compose up -d --wait --no-recreate --scale inventory=2 inventory >/dev/null 2>&1
info "contenedores: $(docker compose ps inventory --format '{{.Name}}' | tr '\n' ' ')"

step "3. Consul registra ambas"
t=$(wait_for 40 2 consul_passing $SVC) || true
check "instancias passing en Consul" "$(consul_passing $SVC)" 2
info "la segunda instancia pasó a passing en ${t}s"
consul_instances $SVC | sed 's/^/    /'
r=$(probe 20); print_probe "$r"
check "el tráfico se reparte entre 2 instancias" "$(jq '.by_instance|length' <<<"$r")" 2
check "sin fallos" "$(jq .failed <<<"$r")" 0

victim=$(container inventory 2)
victim_id=$(instance_id_of "$victim")
step "4. Fallo de una instancia ($MODE): $victim [$victim_id]"
failed_at=$SECONDS
case $MODE in
  pause) docker pause "$victim" >/dev/null ;;
  kill)  docker kill "$victim" >/dev/null ;;
  stop)  docker stop "$victim" >/dev/null ;;
  *) echo "MODE desconocido: $MODE"; exit 2 ;;
esac
r=$(probe 6); info "tráfico inmediatamente después del fallo:"; print_probe "$r"

step "5. Deja de recibir tráfico tras actualizarse su salud"
wait_for 60 1 consul_passing $SVC >/dev/null || true
check "instancias passing en Consul" "$(consul_passing $SVC)" 1
info "Consul la excluyó ~$((SECONDS - failed_at))s después del fallo (modo $MODE)"
consul_instances $SVC | sed 's/^/    /'
r=$(probe 20); print_probe "$r"
check "ninguna respuesta de la instancia caída" "$(jq --arg v "$victim_id" '[.sequence[] | select(. == $v)] | length' <<<"$r")" 0
check "sin fallos con la instancia restante" "$(jq .failed <<<"$r")" 0

step "6. Reiniciar la instancia y verificar su reincorporación"
case $MODE in
  pause) docker unpause "$victim" >/dev/null ;;
  *)     docker start "$victim" >/dev/null ;;
esac
t=$(wait_for 60 2 consul_passing $SVC) || true
check "instancias passing en Consul" "$(consul_passing $SVC)" 2
info "se reincorporó en ${t}s"
r=$(probe 20); print_probe "$r"
check "el tráfico vuelve a repartirse entre 2" "$(jq '.by_instance|length' <<<"$r")" 2

step "Limpieza: volver a una instancia"
docker compose up -d --wait --scale inventory=1 inventory >/dev/null 2>&1
info "réplicas de inventory: $(docker compose ps inventory -q | wc -l)"

summary
