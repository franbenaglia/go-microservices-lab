#!/usr/bin/env bash
# Laboratorio del API Gateway (main.md §12) y del frontend (§13).
#
#   1. Punto de entrada único: frontend y API en :8080; los microservicios no publican puertos.
#   2. Rutas descubiertas en Consul (tags traefik.*) y cabeceras de seguridad.
#   3. Endpoints internos no expuestos (/readyz, /admin/faults); /admin/<svc>/config sí (red local).
#   4. ipAllowList: laboratorio y administración solo desde redes locales (403 desde 100.64.0.0/10).
#   5. Balanceo externo de Traefik entre réplicas sanas.
#   6. Traza Cliente → Gateway → Orders → Inventory y métricas de Traefik en Prometheus.
set -euo pipefail
cd "$(dirname "$0")/../.."
source scripts/lab/lib.sh
GW=$GATEWAY_URL
code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }

step "1. Punto de entrada único"
check "frontend (/)" "$(code "$GW/")" 200
check "frontend es la SPA" "$(curl -s "$GW/" | grep -o '<title>[^<]*</title>')" "<title>Go Microservices Lab</title>"
for s in orders inventory; do
  check "$s no publica puertos en el host" "$(docker compose ps "$s" --format '{{.Publishers}}' | grep -c '127.0.0.1' || true)" 0
done

step "2. Rutas desde Consul y cabeceras"
routers=$(curl -s 127.0.0.1:8090/api/http/routers | jq -r '[.[] | select(.name | endswith("@consulcatalog")) | .name] | sort | join(",")')
check "routers del provider consulCatalog" "$routers" \
  "inventory-admin@consulcatalog,inventory-api@consulcatalog,orders-admin@consulcatalog,orders-api@consulcatalog,orders-lab@consulcatalog"
for p in /api/products /api/orders /api/inventory/diagnose; do check "GET $p" "$(code "$GW$p")" 200; done
h=$(curl -s -D - -o /dev/null "$GW/api/orders")
check "X-Content-Type-Options" "$(grep -i '^x-content-type-options' <<<"$h" | tr -d '\r' | awk '{print $2}')" nosniff
check "X-Frame-Options" "$(grep -i '^x-frame-options' <<<"$h" | tr -d '\r' | awk '{print $2}')" DENY
check_true "X-Trace-Id en la respuesta" grep -qi '^x-trace-id: [0-9a-f]\{32\}' <<<"$h"

step "3. Endpoints internos"
check "/readyz no se expone (lo atiende el frontend: SPA)" "$(curl -s "$GW/readyz" | grep -c '<title>' || true)" 1
check "/admin/faults no se expone" "$(curl -s "$GW/admin/faults" | grep -c '"faults"' || true)" 0
check "/admin/orders/config (red local)" "$(code "$GW/admin/orders/config")" 200
check "/admin/inventory/config (red local)" "$(code "$GW/admin/inventory/config")" 200

step "4. ipAllowList desde una red externa (100.64.10.0/24)"
docker network rm lab-external >/dev/null 2>&1 || true
docker network create --subnet 100.64.10.0/24 lab-external >/dev/null
tr_ctr=$(container traefik 1)
docker network connect lab-external "$tr_ctr"
tip=$(docker inspect -f '{{(index .NetworkSettings.Networks "lab-external").IPAddress}}' "$tr_ctr")
ext() { docker run --rm --network lab-external curlimages/curl:8.17.0 -s -o /dev/null -w '%{http_code}' "http://$tip:8080$1" 2>/dev/null; }
check "API pública desde fuera" "$(ext /api/products)" 200
check "laboratorio desde fuera → 403" "$(ext /api/orders/lab/resilience)" 403
check "administración desde fuera → 403" "$(ext /admin/orders/config)" 403
docker network disconnect lab-external "$tr_ctr"
docker network rm lab-external >/dev/null

step "5. Balanceo externo (Traefik) con 3 réplicas"
docker compose up -d --wait --scale inventory=3 inventory >/dev/null 2>&1
wait_for 60 3 consul_passing inventory-service >/dev/null || true
sleep 6 # Traefik refresca el catálogo de Consul
dist=$(for _ in $(seq 1 30); do curl -s "$GW/api/inventory/diagnose" | jq -r .instance_id; done | sort | uniq -c | awk '{print $1}' | xargs)
info "reparto de 30 peticiones: $dist"
check "Traefik reparte entre las 3 réplicas" "$(wc -w <<<"$dist" | xargs)" 3
docker compose up -d --wait --scale inventory=1 inventory >/dev/null 2>&1
wait_for 60 1 consul_passing inventory-service >/dev/null || true
sleep 6 # Traefik deja de enrutar a las réplicas retiradas al refrescar el catálogo

step "6. Traza completa y métricas del gateway"
pid=$(curl -s "$GW/api/products?limit=500" | jq -r '[.products[] | select(.available > 0)][0].id')
tid=$(curl -s -D - -o /dev/null -H 'Content-Type: application/json' -X POST "$GW/api/orders" \
  -d "{\"items\":[{\"product_id\":\"$pid\",\"quantity\":1}]}" | awk 'tolower($1)=="x-trace-id:" {print $2}' | tr -d '\r')
for _ in $(seq 1 15); do
  svcs=$(curl -s "127.0.0.1:16686/api/v3/traces/$tid" | jq -r '[.result.resourceSpans[]?.resource.attributes[] | select(.key=="service.name") | .value.stringValue] | unique | join(",")' 2>/dev/null || true)
  [[ $svcs == *traefik* && $svcs == *inventory* ]] && break
  sleep 1
done
check "servicios en la traza del pedido" "$svcs" "inventory-service,orders-service,traefik"
curl -s "127.0.0.1:16686/api/v3/traces/$tid" | jq -r -f scripts/lab/trace-tree.jq | head -5 | sed 's/^/    /'
n=$(curl -s --get 127.0.0.1:9090/api/v1/query --data-urlencode 'query=count(traefik_service_requests_total)' | jq -r '.data.result[0].value[1] // 0')
check_true "métricas de Traefik en Prometheus ($n series)" test "$n" -gt 0

summary
