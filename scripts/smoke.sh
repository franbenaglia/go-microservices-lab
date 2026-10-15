#!/usr/bin/env bash
# Prueba de humo de la Fase 1: recorre el flujo completo de un pedido contra la
# plataforma en marcha (make up) e informa de cada comprobación.
#
# Variables: ORDERS_URL (por defecto http://127.0.0.1:8081)
#            INVENTORY_URL (por defecto http://127.0.0.1:8082)
# Requiere: curl, jq.
set -euo pipefail

ORDERS_URL=${ORDERS_URL:-http://127.0.0.1:8081}
INVENTORY_URL=${INVENTORY_URL:-http://127.0.0.1:8082}
JSON=(-H 'Content-Type: application/json')
pass=0 fail=0

check() { # check "descripción" valor_obtenido valor_esperado
  if [[ "$2" == "$3" ]]; then
    printf '  \e[32mOK\e[0m   %s (%s)\n' "$1" "$2"; pass=$((pass + 1))
  else
    printf '  \e[31mFAIL\e[0m %s: obtenido=%q esperado=%q\n' "$1" "$2" "$3"; fail=$((fail + 1))
  fi
}
step() { printf '\n\e[1m== %s\e[0m\n' "$1"; }

step "Salud de los servicios"
check "inventory /readyz" "$(curl -s -o /dev/null -w '%{http_code}' "$INVENTORY_URL/readyz")" 200
check "orders /readyz" "$(curl -s -o /dev/null -w '%{http_code}' "$ORDERS_URL/readyz")" 200

step "Crear producto con 5 unidades"
sku="SMOKE-$(date +%s)"
product=$(curl -s "${JSON[@]}" -X POST "$INVENTORY_URL/api/products" \
  -d "{\"sku\":\"$sku\",\"name\":\"Producto de prueba\",\"price_cents\":1250,\"initial_stock\":5}")
pid=$(jq -r .id <<<"$product")
check "producto creado" "$(jq -r .sku <<<"$product")" "$sku"

step "Pedido de 3 unidades (Orders -> Inventory por gRPC)"
order=$(curl -s "${JSON[@]}" -X POST "$ORDERS_URL/api/orders" \
  -d "{\"items\":[{\"product_id\":\"$pid\",\"quantity\":3}]}")
oid=$(jq -r .id <<<"$order")
check "estado del pedido" "$(jq -r .status <<<"$order")" ORDER_STATUS_CONFIRMED
check "importe total (3 x 1250)" "$(jq -r .total_cents <<<"$order")" 3750
p=$(curl -s "$INVENTORY_URL/api/products/$pid")
check "stock disponible tras reservar" "$(jq -r .available <<<"$p")" 2
check "stock reservado" "$(jq -r .reserved <<<"$p")" 3

step "Pedido sin stock suficiente (3 unidades, quedan 2)"
rej=$(curl -s "${JSON[@]}" -X POST "$ORDERS_URL/api/orders" \
  -d "{\"items\":[{\"product_id\":\"$pid\",\"quantity\":3}]}")
check "estado" "$(jq -r .status <<<"$rej")" ORDER_STATUS_REJECTED
check "código de rechazo" "$(jq -r .rejection_code <<<"$rej")" INSUFFICIENT_STOCK
check "el stock no cambia" "$(curl -s "$INVENTORY_URL/api/products/$pid" | jq -r .available)" 2

step "Cancelar el pedido confirmado"
c=$(curl -s "${JSON[@]}" -X POST "$ORDERS_URL/api/orders/$oid/cancel" -d '{}')
check "estado" "$(jq -r .status <<<"$c")" ORDER_STATUS_CANCELLED
p=$(curl -s "$INVENTORY_URL/api/products/$pid")
check "stock liberado (disponible)" "$(jq -r .available <<<"$p")" 5
check "stock liberado (reservado)" "$(jq -r .reserved <<<"$p")" 0
check "segunda cancelación rechazada (409)" \
  "$(curl -s -o /dev/null -w '%{http_code}' "${JSON[@]}" -X POST "$ORDERS_URL/api/orders/$oid/cancel" -d '{}')" 409

step "Consultas"
check "estado del pedido" "$(curl -s "$ORDERS_URL/api/orders/$oid/status" | jq -r .status)" ORDER_STATUS_CANCELLED
check "listado de rechazados incluye el pedido" \
  "$(curl -s "$ORDERS_URL/api/orders?status=ORDER_STATUS_REJECTED&limit=500" | jq --arg id "$(jq -r .id <<<"$rej")" '[.orders[] | select(.id == $id)] | length')" 1
check "validación de entrada (400)" \
  "$(curl -s -o /dev/null -w '%{http_code}' "${JSON[@]}" -X POST "$ORDERS_URL/api/orders" -d '{"items":[]}')" 400

printf '\n\e[1mResultado: %d OK, %d FAIL\e[0m\n' "$pass" "$fail"
[[ $fail -eq 0 ]]
