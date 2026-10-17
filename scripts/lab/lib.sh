# Funciones comunes de los scripts de laboratorio. Se incluye con `source`.
# Requiere: curl, jq, docker compose.

# Todo entra por el API Gateway (Traefik). Los microservicios no publican puertos.
GATEWAY_URL=${GATEWAY_URL:-http://127.0.0.1:8080}
ORDERS_URL=${ORDERS_URL:-$GATEWAY_URL}
CONSUL_URL=${CONSUL_URL:-http://127.0.0.1:8500}
PROJECT=${COMPOSE_PROJECT_NAME:-go-microservices-lab}
pass=0 fail=0

step() { printf '\n\e[1m== %s\e[0m\n' "$*"; }
info() { printf '  %s\n' "$*"; }

check() { # check "descripción" obtenido esperado
  if [[ "$2" == "$3" ]]; then
    printf '  \e[32mOK\e[0m   %s (%s)\n' "$1" "$2"; pass=$((pass + 1))
  else
    printf '  \e[31mFAIL\e[0m %s: obtenido=[%s] esperado=[%s]\n' "$1" "$2" "$3"; fail=$((fail + 1))
  fi
}

check_true() { # check_true "descripción" comando...
  local desc=$1; shift
  if "$@"; then printf '  \e[32mOK\e[0m   %s\n' "$desc"; pass=$((pass + 1))
  else printf '  \e[31mFAIL\e[0m %s\n' "$desc"; fail=$((fail + 1)); fi
}

summary() {
  printf '\n\e[1mResultado: %d OK, %d FAIL\e[0m\n' "$pass" "$fail"
  [[ $fail -eq 0 ]]
}

# Número de instancias con todas las comprobaciones en passing.
consul_passing() { # consul_passing servicio
  curl -s "$CONSUL_URL/v1/health/service/$1?passing=true" | jq 'length'
}

# Lista "id estado" de todas las instancias registradas.
consul_instances() { # consul_instances servicio
  curl -s "$CONSUL_URL/v1/health/service/$1" |
    jq -r '.[] | "\(.Service.ID) \(.Service.Address) \([.Checks[] | select(.ServiceID != "") | .Status] | if all(. == "passing") then "passing" else "critical" end)"'
}

# Espera hasta que `cmd` devuelva `want` o se agote el tiempo. Imprime los segundos.
wait_for() { # wait_for timeout_s want cmd...
  local timeout=$1 want=$2; shift 2
  local start=$SECONDS
  while (( SECONDS - start < timeout )); do
    [[ "$("$@" 2>/dev/null)" == "$want" ]] && { echo $((SECONDS - start)); return 0; }
    sleep 1
  done
  echo "timeout"; return 1
}

# Sondeo de balanceo: N llamadas Orders -> Inventory.Diagnose (gRPC).
probe() { # probe N -> JSON
  curl -s -H 'Content-Type: application/json' -X POST "$ORDERS_URL/api/orders/lab/lb" -d "{\"count\":$1}"
}

print_probe() { # print_probe JSON
  jq -r '"  total=\(.total) ok=\(.succeeded) fallos=\(.failed) latencia media=\(.avg_latency_ms|floor)ms máx=\(.max_latency_ms|floor)ms",
         (.by_instance[]? | "    \(.instance_id): \(.count)"),
         (.errors[]? | "    error \(.reason): \(.count)")' <<<"$1"
}

container() { # container servicio índice
  echo "${PROJECT}-$1-$2"
}

instance_id_of() { # instance_id_of contenedor -> ID registrado en Consul
  local host
  host=$(docker inspect -f '{{.Config.Hostname}}' "$1")
  curl -s "$CONSUL_URL/v1/health/service/inventory-service" |
    jq -r --arg h "$host" '.[] | select(.Service.Meta.hostname == $h) | .Service.ID'
}

# --- Fase 4: inyección de fallos y resiliencia ---

# Acceso a una réplica concreta: los microservicios no publican puertos, así
# que la petición se hace desde dentro de la red Docker (contenedor de Consul,
# que incluye curl). Solo lo usan los scripts para fallos por réplica.
incluster() { docker compose exec -T consul curl -s "$@"; }

container_ip() { # container_ip servicio índice
  docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$(container "$1" "$2")"
}

inventory_url() { echo "http://$(container_ip inventory "$1"):8000"; }

inventory_urls() { # URL interna de cada réplica de inventory
  local n i
  n=$(docker compose ps inventory -q | wc -l)
  for ((i = 1; i <= n; i++)); do inventory_url "$i"; done
}

set_faults() { # set_faults JSON [índice]  (sin índice: todas las réplicas)
  local url
  if [[ -n ${2:-} ]]; then
    incluster -f -X PUT "$(inventory_url "$2")/admin/faults" --data-binary @- <<<"$1" >/dev/null
  else
    for url in $(inventory_urls); do incluster -f -X PUT "$url/admin/faults" --data-binary @- <<<"$1" >/dev/null; done
  fi
}

faults_of() { incluster "$(inventory_url "$1")/admin/faults"; } # faults_of índice

clear_faults() {
  local url
  for url in $(inventory_urls); do incluster -f -X DELETE "$url/admin/faults" >/dev/null; done
}

resilience() { curl -s "$ORDERS_URL/api/orders/lab/resilience"; }
breaker_state() { resilience | jq -r .breaker.state; }

# Recrea Orders (breaker y contadores desde cero) y aplica la configuración:
#   - INVENTORY_BALANCER, BREAKER_KIND: requieren reinicio → variables de entorno.
#   - BREAKER_ENABLED, RETRY_ENABLED: dinámicas → Consul KV vía /admin/config
#     (el documento de Consul tiene prioridad sobre el entorno).
# Ejemplo: orders_with BREAKER_ENABLED=false RETRY_ENABLED=true
orders_with() {
  local restart=() breaker="" retry="" kv
  for kv in "$@"; do
    case ${kv%%=*} in
      BREAKER_ENABLED) breaker=${kv#*=} ;;
      RETRY_ENABLED) retry=${kv#*=} ;;
      *) restart+=("$kv") ;;
    esac
  done
  env "${restart[@]}" docker compose up -d --wait --force-recreate orders >/dev/null 2>&1
  wait_gateway
  if [[ -n $breaker || -n $retry ]]; then
    local patch want
    patch=$(jq -n --arg b "$breaker" --arg r "$retry" '{resilience: (
      (if $b != "" then {breaker: {enabled: ($b == "true")}} else {} end) +
      (if $r != "" then {retry: {enabled: ($r == "true")}} else {} end))}')
    set_config orders "$patch" >/dev/null
    want="${breaker:-any}|${retry:-any}"
    wait_for 15 ok resilience_matches "$want" >/dev/null ||
      echo "  aviso: la configuración dinámica no se reflejó a tiempo" >&2
  fi
}

# Espera a que Traefik enrute a Orders. `docker compose --wait` solo espera al
# healthcheck de Docker (/livez); Traefik además exige que la instancia esté
# passing en Consul (readiness), lo que tarda hasta un intervalo de check.
# Un 200 aislado no basta: Traefik puede tener aún la instancia anterior en su
# lista. Se espera a que el servicio tenga exactamente la IP del contenedor nuevo.
TRAEFIK_API=${TRAEFIK_API:-http://127.0.0.1:8090}
gateway_orders_servers() {
  curl -s "$TRAEFIK_API/api/http/services/orders-service@consulcatalog" | jq -r '[.loadBalancer.servers[]?.url] | join(",")'
}
wait_gateway() {
  local want="http://$(container_ip orders 1):8000"
  wait_for 30 "$want" gateway_orders_servers >/dev/null ||
    echo "  aviso: Traefik no enruta solo a la nueva instancia de orders" >&2
}

resilience_matches() { # resilience_matches "true|false" (any = no se comprueba)
  local b r got
  got=$(resilience | jq -r '"\(.breaker.enabled)|\(.retry.enabled)"')
  b=${1%%|*} r=${1##*|}
  [[ ($b == any || $b == "${got%%|*}") && ($r == any || $r == "${got##*|}") ]] && echo ok
}

# --- Fase 6: configuración dinámica (Consul KV) ---

# Configuración dinámica a través del gateway: /admin/<servicio>/config.
get_config() { curl -s "$GATEWAY_URL/admin/$1/config"; }

set_config() { # set_config orders|inventory JSON  → código HTTP
  curl -s -o /dev/null -w '%{http_code}' -X PUT "$GATEWAY_URL/admin/$1/config" --data-binary @- <<<"$2"
}

injected_total() { # errores inyectados acumulados en todas las réplicas (peticiones que llegaron)
  local url t=0 n
  for url in $(inventory_urls); do n=$(incluster "$url/admin/faults" | jq .injected_errors); t=$((t + n)); done
  echo $t
}

probe_interval() { # probe_interval N ms
  curl -s -H 'Content-Type: application/json' -X POST "$ORDERS_URL/api/orders/lab/lb" \
    -d "{\"count\":$1,\"interval_ms\":$2}"
}

fail_pct() { jq '(.failed * 100 / .total) | floor' <<<"$1"; }
