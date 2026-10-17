# Fase 7: API Gateway (Traefik) y frontend (Svelte)

## Arquitectura de entrada

```
navegador ──► 127.0.0.1:8080 Traefik v3.7 ─┬─ /                                  → frontend (nginx, provider file)
                                          ├─ /api/orders, /api/orders/lab*      → orders-service     ┐
                                          ├─ /api/products, /api/inventory      → inventory-service  ├ provider consulCatalog
                                          └─ /admin/orders/*, /admin/inventory/* → /admin/* (rewrite) ┘ (tags traefik.* en Consul)
                     127.0.0.1:8090 dashboard de Traefik
```

- **Punto de entrada único**: los microservicios ya no publican puertos. Todo pasa por Traefik, que
  descubre las instancias en Consul (provider `consulCatalog`, `strictChecks: [passing]`) a partir
  de los tags que cada servicio declara en `consul.tags` de su configuración.
- **Frontend en el mismo origen**: no hace falta CORS.
- **Restricción de laboratorio y administración**: middleware `local-only` (ipAllowList: 127.0.0.1,
  10/8, 172.16/12 y 192.168/16) en `/api/orders/lab/*` y `/admin/*`. `/readyz` y `/admin/faults` no
  tienen ruta en el gateway.
- **Cabeceras**: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
  `Referrer-Policy: same-origin`, `Cache-Control: no-store` en la API, y `X-Trace-Id` de los servicios.
- **Observabilidad**: Traefik envía trazas (inicia la traza W3C) y métricas por OTLP al Collector. Los
  logs de acceso van en JSON, sin cabeceras. Hay una fila "API Gateway (Traefik)" en Grafana.

## Frontend

Svelte 5 (runes) + TypeScript 6 + Vite 8, sin dependencias en tiempo de ejecución: router por hash
y gráficos en SVG propio. Se compila con `node:24.22.0-alpine` (type-check incluido) y se sirve con
`nginx:1.31.7-alpine`.

| Sección | Qué hace | API que usa |
|---|---|---|
| Dashboard | servicios, instancias y checks, RPS, errores 5xx, p95, peticiones de 15 min, breaker, últimos rechazos, accesos a las herramientas (cada 5s) | `GET /api/orders/lab/overview` (Orders consulta Consul y Prometheus desde el servidor) |
| Productos | listar, crear, ajustar stock (+/−), consultar disponibilidad | `/api/products`, `/api/inventory/*` |
| Pedidos | crear (varias líneas), filtrar por estado, cancelar, ver el código de rechazo y la traza | `/api/orders` |
| Load Balancing Lab | balanceo interno (Kratos) frente a externo (Traefik): distribución por instancia, fallos y latencia | `POST /api/orders/lab/lb` y `GET /api/inventory/diagnose` |
| Circuit Breaker Lab | fallos centralizados (todas las réplicas), generador de tráfico, gráfico por segundo (correctas, fallidas, rechazadas) con banda de estado y transiciones | `PUT /admin/inventory/config`, `/api/orders/lab/*` |
| Tracing | últimas operaciones del navegador con enlace a Jaeger por `X-Trace-Id` | cabecera `X-Trace-Id` |
| Configuración | edición de los parámetros dinámicos, estado de recargas, lista de reinicio y configuración estática | `/admin/<svc>/config` |

**Accesibilidad**: los estados usan icono y texto (nunca solo color); los gráficos tienen leyenda,
tooltip (`<title>`) y vista de tabla; hay modo oscuro según el sistema; el diseño es responsive
(probado a 390px sin scroll horizontal).

## Cómo probarlo

```bash
make up
open http://127.0.0.1:8080          # frontend
open http://127.0.0.1:8090          # dashboard de Traefik
make lab-gateway                    # 21 comprobaciones
make labs                           # todos los laboratorios en secuencia
make frontend-check                 # type-check + build del frontend en contenedor
make frontend-dev                   # Vite en :5173 con proxy al gateway
```

## Resultados

- `make lab-gateway` 21/21:
  - routers descubiertos en Consul;
  - cabeceras de seguridad;
  - `/readyz` y `/admin/faults` sin ruta;
  - desde una red externa (100.64.10.0/24), la API pública responde 200 y laboratorio y administración 403;
  - Traefik reparte 10/10/10 entre 3 réplicas;
  - traza Traefik → Orders → Inventory → PostgreSQL;
  - métricas de Traefik en Prometheus.
- Interfaz verificada con Playwright:
  - Dashboard con datos reales;
  - pedido creado (CONFIRMED) con enlace a la traza;
  - balanceo 10/10/10 por Kratos (0,3ms) y por Traefik (3,0ms desde el navegador);
  - ciclo CLOSED → OPEN → CLOSED en el Circuit Breaker Lab;
  - cambio de configuración aplicado sin reiniciar;
  - vista móvil.
- Regresión (`make labs`): smoke 17, discovery 10, lb 19, resilience 19, observability 29, config 28, gateway 21.

## Balanceo interno frente a externo, medido

| | Kratos (Orders → Inventory) | Traefik (navegador → Inventory) |
|---|---|---|
| 30 peticiones, 3 réplicas | 10/10/10 | 10/10/10 |
| Latencia media | 0,3ms (gRPC, conexión persistente) | 3,0ms (incluye navegador y proxy) |
| Fuente de instancias | resolver de Kratos (Consul, passing) | provider consulCatalog (Consul, passing) |

## Hallazgos

- **Carrera tras recrear un servicio**: `docker compose --wait` solo espera al healthcheck de Docker
  (`/livez`); Traefik exige *passing* en Consul (readiness). Los scripts esperan a que el servicio de
  Traefik tenga exactamente la IP del contenedor nuevo (`wait_gateway` en lib.sh).
- **Reducción de réplicas con tráfico por Traefik**: 1 de 300 peticiones devolvió 504 (petición en
  vuelo hacia la réplica que se apagaba). Mitigación posible: middleware `retry` de Traefik para las
  lecturas.
- **Endpoints `HandleFunc`** (`/admin/*`) no pasan por los middlewares de Kratos: se envuelven con
  `otelhttp` (`Telemetry.WrapHTTP`) para continuar la traza y devolver `X-Trace-Id`.
- **npm del host (10.8)** falla con `Cannot read properties of null (reading 'edgesOut')`: las
  dependencias se instalan y compilan con el npm de `node:24.22.0-alpine`.
- **TypeScript 7** (latest) no es compatible con svelte-check 4.7 (peer `^5 || ^6`): se fija 6.0.3.
- Con Playwright, navegar a la misma URL cambiando solo el hash no recarga la página.
