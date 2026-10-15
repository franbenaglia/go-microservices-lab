# Funciones comunes de los scripts de laboratorio. Se incluye con `source`.
# Requiere: curl, jq, docker compose.

ORDERS_URL=${ORDERS_URL:-http://127.0.0.1:8081}
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

inventory_urls() { # URL HTTP de cada réplica de inventory
  local n i
  n=$(docker compose ps inventory -q | wc -l)
  for ((i = 1; i <= n; i++)); do echo "http://$(docker compose port --index "$i" inventory 8000)"; done
}

set_faults() { # set_faults JSON [índice]  (sin índice: todas las réplicas)
  local url
  if [[ -n ${2:-} ]]; then
    url="http://$(docker compose port --index "$2" inventory 8000)"
    curl -sf -X PUT "$url/admin/faults" --data-binary @- <<<"$1" >/dev/null
  else
    for url in $(inventory_urls); do curl -sf -X PUT "$url/admin/faults" --data-binary @- <<<"$1" >/dev/null; done
  fi
}

clear_faults() {
  local url
  for url in $(inventory_urls); do curl -sf -X DELETE "$url/admin/faults" >/dev/null; done
}

resilience() { curl -s "$ORDERS_URL/api/orders/lab/resilience"; }
breaker_state() { resilience | jq -r .breaker.state; }

# Recrea Orders con otras variables (siempre: así el breaker y los contadores
# empiezan de cero), p. ej.: orders_with BREAKER_ENABLED=false RETRY_ENABLED=true
orders_with() {
  env "$@" docker compose up -d --wait --force-recreate orders >/dev/null 2>&1
}

injected_total() { # errores inyectados acumulados en todas las réplicas (peticiones que llegaron)
  local url t=0 n
  for url in $(inventory_urls); do n=$(curl -s "$url/admin/faults" | jq .injected_errors); t=$((t + n)); done
  echo $t
}

probe_interval() { # probe_interval N ms
  curl -s -H 'Content-Type: application/json' -X POST "$ORDERS_URL/api/orders/lab/lb" \
    -d "{\"count\":$1,\"interval_ms\":$2}"
}

fail_pct() { jq '(.failed * 100 / .total) | floor' <<<"$1"; }
