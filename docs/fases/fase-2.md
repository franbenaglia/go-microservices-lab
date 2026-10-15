# Fase 2: Service Discovery con Consul

## Qué se implementó

- **Consul 2.0.4** en compose: modo servidor de un nodo con `data_dir` persistente (volumen
  `consuldata`) y la UI en http://127.0.0.1:8500. Configuración en
  [deployments/consul/server.hcl](../../deployments/consul/server.hcl).
- **Registro automático** de `inventory-service` y `orders-service` con el registry oficial
  `contrib/registry/consul/v3`, envuelto en [pkg/discovery](../../pkg/discovery/consul.go). Cada
  instancia registra:
  - ID de instancia (`<hostname>-<sufijo aleatorio>`), nombre, versión (tag `version=`);
  - dirección y puertos: `TaggedAddresses.http` y `TaggedAddresses.grpc`;
  - metadatos `instance` y `hostname`;
  - tres checks: **TTL** (heartbeat del proceso), **TCP** (uno por puerto) y **HTTP `/readyz`** (readiness).
- **Desregistro**: inmediato en el apagado ordenado (Kratos llama a `Deregister` antes de parar los
  servidores). Si la instancia desaparece sin desregistrarse, queda *critical* y Consul la elimina
  pasado `deregister_after` (1m).
- **Descubrimiento**: Orders usa `discovery:///inventory-service`. El resolver de Kratos mantiene
  una consulta bloqueante (*long poll*) contra Consul y solo recibe las instancias **passing**.
- **Endpoint de diagnóstico** (adelantado de la Fase 3): `POST /api/orders/lab/lb {"count":N}` hace N
  llamadas `Inventory.Diagnose` por gRPC y devuelve qué instancia atendió cada una.

## Cómo probarlo

```bash
make up
open http://127.0.0.1:8500            # UI de Consul: servicios, instancias y checks
make lab-discovery                    # 6 escenarios del §5 con MODE=pause (por defecto)
MODE=kill make lab-discovery          # caída abrupta (SIGKILL)
MODE=stop make lab-discovery          # apagado ordenado

# A mano
docker compose up -d --scale inventory=3 --wait
curl -s -H 'Content-Type: application/json' -X POST localhost:8081/api/orders/lab/lb -d '{"count":30}' | jq '.by_instance'
curl -s localhost:8500/v1/health/service/inventory-service?passing=true | jq '.[].Service.ID'
```

## Tiempos de detección observados (health_interval = 5s)

| Fallo | Qué lo detecta | Errores observados | Exclusión |
|---|---|---|---|
| `docker stop` (apagado ordenado) | desregistro explícito | 0 | ~1s |
| `docker kill` (SIGKILL) | el cliente gRPC ve la conexión cerrada al instante; después, los checks TCP/TTL | 0 | Consul ~6s |
| `docker pause` (proceso congelado) | **solo** los checks de Consul: el puerto TCP sigue aceptando conexiones | timeouts (2s) hasta la exclusión | ~5–8s |
| PostgreSQL caído | `/readyz` → 503 (el contenedor sigue *healthy*) | — | ~1s; reincorporación ~4s |
| Todas las instancias caídas | el cliente falla rápido (503) y los pedidos quedan `REJECTED/INVENTORY_UNAVAILABLE` | — | recuperación ~1s |

**Dos capas de detección:** Consul decide qué instancias se *descubren*, y el cliente gRPC de
Kratos (balancer y subconexiones) descarta al instante las conexiones rotas. Un proceso congelado
solo lo detecta la primera capa, porque mantiene la conexión TCP abierta.

## Pruebas automatizadas

- `pkg/discovery` (integración, `LAB_TEST_CONSUL_ADDR`): registro, `Watch` con altas y bajas,
  metadatos y direcciones; readiness que llega a passing y readiness que impide estar passing aunque
  TTL y TCP estén sanos.
- `make test-integration` levanta PostgreSQL y Consul y ejecuta todo.

## Limitaciones conocidas

- El contrib **no publica una lista vacía** cuando todas las instancias quedan unhealthy
  (`registry.go`: `if len(tmpService) != 0 && ...`). El cliente conserva las direcciones viejas,
  que fallan rápido (connection refused), así que en la práctica el efecto es el mismo.
- Los IDs incluyen un sufijo aleatorio, así que un reinicio crea una instancia nueva y la anterior
  queda como *critical* hasta 1m (sin recibir tráfico). Así se distinguen los reinicios en el
  laboratorio de balanceo.
- Consul exige `deregister_after >= 1m`.
- Sin ACLs ni TLS (laboratorio local). La UI solo se publica en 127.0.0.1.
