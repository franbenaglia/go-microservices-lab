# Fase 1: base funcional

## Qué se implementó

- **Contratos** en [api/proto](../../api/proto): `inventory.v1` y `orders.v1` con anotaciones HTTP y
  razones de error de Kratos. El código se genera con `make proto` (buf + plugins vía `go tool`).
- **Inventory Service**: CRUD de productos, ajuste de stock, consulta de disponibilidad, reserva
  atómica (todo o nada) e idempotente, liberación idempotente (con lápida) y `Diagnose`.
- **Orders Service**: crear, consultar, listar (con filtro por estado) y cancelar pedidos, más
  `GetOrderStatus`. Máquina de estados PENDING → CONFIRMED/REJECTED/CANCELLED y CONFIRMED → CANCELLED.
  Llama a Inventory por gRPC con un timeout por llamada.
- **Común** ([pkg/](../../pkg)): carga de configuración (YAML + `APP_*`), logger slog JSON con nivel
  dinámico y filtro de claves sensibles, `/livez` y `/readyz`, pool pgx con reintentos y migraciones goose.
- **Docker Compose**: PostgreSQL 18 (una base y un usuario por servicio), Inventory y Orders, con
  healthchecks y volumen persistente.

Las decisiones de diseño están en [DECISIONS.md](../harness/DECISIONS.md) (ADR-007 y ADR-010 a ADR-014).

## Cómo probarlo

```bash
cp .env.example .env          # y cambiar las contraseñas
make up                       # construye e inicia (espera a que todo esté healthy)
make smoke                    # recorre el flujo de pedidos y muestra OK/FAIL
make logs S=orders            # logs JSON de un servicio
make test                     # unitarias (sin Docker)
make test-integration         # unitarias + integración (usa el PostgreSQL de compose)
```

### Llamadas REST de ejemplo (Fase 1: puertos temporales)

```bash
# Crear producto (Inventory publicado en 127.0.0.1:8082)
curl -s -H 'Content-Type: application/json' -X POST localhost:8082/api/products \
  -d '{"sku":"KB-001","name":"Teclado","price_cents":4999,"initial_stock":10}'

# Crear pedido (Orders publicado en 127.0.0.1:8081)
curl -s -H 'Content-Type: application/json' -X POST localhost:8081/api/orders \
  -d '{"items":[{"product_id":"<id>","quantity":2}]}'

curl -s localhost:8081/api/orders?status=ORDER_STATUS_REJECTED
curl -s -H 'Content-Type: application/json' -X POST localhost:8081/api/orders/<id>/cancel -d '{}'
curl -s localhost:8082/api/inventory/diagnose
```

> El header `Content-Type: application/json` es obligatorio (ADR-010). Los campos `int64`, como
> `price_cents`, se devuelven como cadenas, según el mapeo JSON de proto3.

### Probar la indisponibilidad de Inventory

```bash
docker compose stop inventory
curl -s -H 'Content-Type: application/json' -X POST localhost:8081/api/orders \
  -d '{"items":[{"product_id":"<id>","quantity":1}]}'   # -> REJECTED / INVENTORY_UNAVAILABLE
curl -s localhost:8081/readyz                            # Orders sigue "ready"
docker compose start inventory
```

## Pruebas automatizadas

| Tipo | Ubicación | Qué cubre |
|---|---|---|
| Unitarias | `services/*/internal/biz/*_test.go` | validaciones, normalización de líneas, máquina de estados, totales, flujos de creación y cancelación con fakes |
| Integración (PostgreSQL) | `services/inventory/internal/data` | SKU duplicado, stock nunca negativo, reserva todo o nada, idempotencia, lápida, **60 reservas concurrentes sobre 20 unidades**, misma clave en paralelo |
| Integración (gRPC) | `services/inventory/internal/server` | contrato gRPC real sobre PostgreSQL, propagación de errores (razón y código) y de deadlines |
| Integración (Orders) | `services/orders/internal/data` | repositorio de pedidos y traducción de errores gRPC → dominio (NotFound, stock, 500, timeout, servidor caído) |

## Pendiente (fases siguientes)

- Orders usa una dirección fija (`inventory:9000`); en la Fase 2 se sustituye por Consul.
- Los puertos publicados en el host son temporales.
- Faltan trazas, métricas, circuit breaker y retries (Fases 4 y 5).
