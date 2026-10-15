# Fase 3: balanceo de carga del lado cliente

## Qué se implementó

- **Réplicas de Inventory**: `docker compose up -d --scale inventory=3`. Cada réplica tiene un ID
  único (`<hostname>-<sufijo>`) que devuelve `Diagnose` y que coincide con su ID en Consul.
- **Selector configurable** en Orders: `APP_INVENTORY_BALANCER` = `wrr` (por defecto) | `p2c` |
  `random`, aplicado con `selector.SetGlobalSelector` antes de crear el cliente gRPC. En compose se
  controla con `INVENTORY_BALANCER=p2c docker compose up -d orders`. **Requiere reiniciar** Orders.
- **Pesos por instancia**: `INSTANCE_WEIGHT` (1..1000) se publica como metadato `weight` en Consul
  y wrr lo usa como peso del nodo (100 si no se define).
- **Diagnóstico**: `POST /api/orders/lab/lb {"count":N,"interval_ms":M}` devuelve la distribución,
  los errores, las latencias, la secuencia y el selector activo.

## Cómo probarlo

```bash
make up
make lab-lb                 # 4 escenarios (MODE=stop por defecto; MODE=kill caída abrupta)

docker compose up -d --scale inventory=3 --wait
curl -s -H 'Content-Type: application/json' -X POST localhost:8081/api/orders/lab/lb \
  -d '{"count":30}' | jq '{balancer, by_instance}'

INVENTORY_BALANCER=random docker compose up -d --wait orders   # cambiar el selector
```

## Resultados observados (`make lab-lb`)

| Escenario | Resultado |
|---|---|
| wrr, 3 réplicas, 90 llamadas | 30/30/30, rotación estricta (0 repeticiones consecutivas) |
| Caída de 1 réplica con 300 llamadas cada 10ms (`stop` y `kill`) | 0 errores; la réplica deja de recibir tráfico al instante (última llamada ~nº 92) y el resto se reparte entre las otras 2 |
| Instancia extra con `weight=300` (las demás 100) | recibe 60 de 120 (300/600 = 50%); las demás, 20 cada una |
| p2c, 90 llamadas | p. ej. 51/29/10: con latencias casi idénticas, el EWMA favorece a la instancia más rápida en cada momento |
| random, 90 llamadas | p. ej. 33/30/27, con repeticiones consecutivas |

> Con tráfico secuencial no hay llamadas en vuelo en el instante del fallo, por eso no aparecen
> errores ni con SIGKILL. Con tráfico concurrente, las llamadas en vuelo hacia la réplica caída
> fallarían con `Unavailable`. La Fase 4 añade reintentos seguros para cubrir ese caso.

## Cómo funciona el balanceo interno

```
Orders ──gRPC──► [ cliente Kratos ]
                   resolver discovery:///inventory-service ◄── Consul (solo instancias passing)
                   balancer "selector" (gRPC base balancer, solo subconexiones READY)
                   selector global: wrr | p2c | random  (peso = metadato "weight")
                 ──► inventory-1 / inventory-2 / inventory-3   (conexión directa, sin proxy)
```

- La decisión se toma **en el proceso cliente** para cada llamada, sobre conexiones HTTP/2
  persistentes a cada réplica. No hay salto intermedio.
- Hay dos fuentes de verdad: Consul decide qué instancias existen y están sanas, y gRPC descarta al
  instante las conexiones rotas.

## Balanceo interno (Kratos) frente a balanceo externo (Traefik, Fase 7)

| | Kratos + Consul (interno) | Traefik (externo) |
|---|---|---|
| Dónde decide | en el cliente (Orders), por llamada | en un proxy intermedio |
| Protocolo | gRPC sobre HTTP/2, conexiones persistentes a cada réplica | HTTP desde el navegador o un cliente externo |
| Descubrimiento | resolver de Kratos contra Consul | provider Consul Catalog de Traefik |
| Saltos de red | 1 (cliente → réplica) | 2 (cliente → Traefik → réplica) |
| Configuración del algoritmo | selector global del proceso (reinicio) | configuración del load balancer de Traefik |
| Equivalente en Spring Cloud | Spring Cloud LoadBalancer | Spring Cloud Gateway |

## Hallazgos

- **Orden de `init()` en Kratos v3**: el balancer gRPC (`transport/grpc/balancer.go`) captura
  `selector.GlobalSelector()` en su `init()`, pero el selector por defecto (wrr) lo fija el `init()`
  de `client.go`, que se ejecuta después. Funciona porque `transport/http` (que también importamos)
  se inicializa antes y fija wrr. Un binario que solo importara el transporte gRPC quedaría con un
  builder nil. `SetGlobalSelector` cambia el builder envuelto, así que basta con llamarlo antes de
  crear la conexión.
- **p2c sin carga real** no reparte de forma uniforme: optimiza la latencia, no la equidad. Con la
  latencia artificial de la Fase 4 se verá cómo evita una réplica lenta.
