#!/usr/bin/env bash
# Laboratorio de observabilidad (main.md §10, §11 y §14).
#
#   1. Un pedido devuelve X-Trace-Id y su traza completa está en Jaeger:
#      Orders (HTTP) → spans de negocio → cliente gRPC → Inventory (gRPC) → PostgreSQL.
#   2. Los logs de ambos servicios llevan el mismo trace_id.
#   3. Prometheus tiene las métricas de servidor, cliente, breaker, retries, negocio y salud.
#   4. Grafana tiene el dashboard aprovisionado.
set -euo pipefail
cd "$(dirname "$0")/../.."
source scripts/lab/lib.sh
JAEGER_URL=${JAEGER_URL:-http://127.0.0.1:16686}
PROM_URL=${PROM_URL:-http://127.0.0.1:9090}
GRAFANA_URL=${GRAFANA_URL:-http://127.0.0.1:3000}
INV="http://$(docker compose port --index 1 inventory 8000)"

step "1. Traza distribuida de un pedido"
sku="OBS-$(date +%s)"
pid=$(curl -s -H 'Content-Type: application/json' -X POST "$INV/api/products" \
  -d "{\"sku\":\"$sku\",\"name\":\"Observabilidad\",\"price_cents\":100,\"initial_stock\":5}" | jq -r .id)
tid=$(curl -s -D - -o /dev/null -H 'Content-Type: application/json' -X POST "$ORDERS_URL/api/orders" \
  -d "{\"items\":[{\"product_id\":\"$pid\",\"quantity\":1}]}" | awk 'tolower($1)=="x-trace-id:" {print $2}' | tr -d '\r')
check_true "la respuesta incluye X-Trace-Id ($tid)" test ${#tid} -eq 32
info "esperando a que la traza llegue a Jaeger (batch del SDK y del Collector)..."
trace=""
for _ in $(seq 1 20); do
  trace=$(curl -s "$JAEGER_URL/api/v3/traces/$tid")
  [[ $(jq '[.result.resourceSpans[]?.scopeSpans[].spans[]] | length' <<<"$trace" 2>/dev/null || echo 0) -ge 20 ]] && break
  sleep 1
done
jq -r -f scripts/lab/trace-tree.jq <<<"$trace" | sed 's/^/    /'
names=$(jq -r '[.result.resourceSpans[].scopeSpans[].spans[].name] | unique | join(",")' <<<"$trace")
services=$(jq -r '[.result.resourceSpans[].resource.attributes[] | select(.key=="service.name") | .value.stringValue] | unique | join(",")' <<<"$trace")
check "servicios en la traza" "$services" "inventory-service,orders-service"
for n in /orders.v1.OrdersService/CreateOrder order.validate order.price_lookup order.check_availability \
         order.reserve_stock order.finalize /inventory.v1.InventoryService/ReserveStock inventory.reserve_stock UPDATE; do
  check_true "span $n" grep -q -- "$n" <<<"${names//,/$'\n'}"
done
check "sin parámetros SQL en los spans" \
  "$(jq '[.result.resourceSpans[].scopeSpans[].spans[].attributes[]? | select(.key | test("args|param"))] | length' <<<"$trace")" 0

step "2. Logs correlacionados con la traza"
n_orders=$(docker compose logs --no-log-prefix orders 2>&1 | grep -c "\"trace_id\":\"$tid\"" || true)
n_inv=$(docker compose logs --no-log-prefix inventory 2>&1 | grep -c "\"trace_id\":\"$tid\"" || true)
check_true "logs de orders con el trace_id ($n_orders)" test "$n_orders" -ge 1
check_true "logs de inventory con el trace_id ($n_inv)" test "$n_inv" -ge 1

step "3. Métricas en Prometheus"
sleep 12 # metric_interval (5s) + scrape (5s)
for m in server_requests_code_total server_requests_seconds_bucket client_requests_code_total \
         client_requests_by_instance_total circuit_breaker_state client_retry_events_total \
         orders_processed_total inventory_reservations_total service_ready go_memory_used_bytes \
         db_client_operation_duration_seconds_bucket; do
  n=$(curl -s --get "$PROM_URL/api/v1/query" --data-urlencode "query=count($m)" | jq -r '.data.result[0].value[1] // 0')
  check_true "$m ($n series)" test "$n" -gt 0
done
check "etiquetas job/instance desde el resource OTel" \
  "$(curl -s --get "$PROM_URL/api/v1/query" --data-urlencode 'query=count by (job) (service_ready)' | jq -r '[.data.result[].metric.job] | sort | join(",")')" \
  "inventory-service,orders-service"
check "sin IDs de pedido como etiqueta" \
  "$(curl -s "$PROM_URL/api/v1/labels" | jq '[.data[] | select(test("order_id|user"))] | length')" 0

step "4. Grafana"
check "dashboard aprovisionado" "$(curl -s "$GRAFANA_URL/api/dashboards/uid/lab-overview" | jq -r .dashboard.uid)" lab-overview
check "datasource Prometheus operativo" "$(curl -s "$GRAFANA_URL/api/datasources/uid/prometheus/health" -u "admin:${GRAFANA_ADMIN_PASSWORD:-change-me-grafana}" | jq -r .status)" OK

summary
