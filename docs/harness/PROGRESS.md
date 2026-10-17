# Estado actual

**Última actualización:** 2026-10-17 (sesión 3)
**Fase en curso:** Fase 6 terminada y verificada → siguiente: **Fase 7 (API Gateway Traefik + frontend Svelte)**

## Próximo paso concreto
1. Traefik (versión estable actual; verificar el tag en Docker Hub) con el provider **consulCatalog** (ADR-005):
   los servicios se registran con tags `traefik.*` (añadirlos con `consul.WithTags` en pkg/discovery).
2. Rutas: `/api/orders`, `/api/products`, `/api/inventory` → los servicios (balanceo HTTP de Traefik);
   logs de acceso, cabeceras y CORS solo si hace falta (el frontend estará en el mismo origen).
3. `/admin/*` y `/api/orders/lab/*` restringidos con ipAllowList; el frontend los usará para los labs y Configuration.
4. Tracing de Traefik (OTLP al Collector) para la traza Cliente → Gateway → Orders → Inventory.
5. Quitar los puertos temporales de los servicios (8081, puerto efímero de inventory) y adaptar
   scripts/lab/lib.sh para entrar por Traefik.
6. Frontend Svelte 5 + Vite + TS (ADR-009): Dashboard, Products, Orders, LB Lab, CB Lab, Tracing, Configuration.

## Estado de la plataforma
- `make up`: postgres, consul, inventory (escalable), orders, otel-collector, jaeger, prometheus y grafana.
- Puertos en 127.0.0.1: orders 8081, consul 8500, postgres 55432, jaeger 16686, prometheus 9090,
  grafana 3000, inventory efímero.
- Configuración dinámica en Consul KV: `config/orders-service/dynamic.yaml`, `config/inventory-service/dynamic.yaml`.

## Bloqueos / preguntas abiertas
- Ninguno. Los cambios de la Fase 6 están **sin commit**.

## Notas rápidas para retomar
- Fuentes de Kratos v3: `$(go env GOMODCACHE)/github.com/go-kratos/kratos/v3@v3.0.0/`
- Contrib v3 fijados a `v3.0.0-20260626125723-668db92c2c00` (registry/consul, config/consul y otel en go.mod).
- Peticiones REST con `Content-Type: application/json`; los int64 se devuelven como cadenas (ADR-010).
- **Lo dinámico manda en Consul KV**: cambiar con `PUT /admin/config` (o `set_config` en lib.sh), no con variables de entorno.
- El `curl` de snap no lee ni escribe en /tmp: usar stdin/stdout (`--data-binary @-`, `-D -`).
- Jaeger 2.22: solo `/api/v3/...` (`scripts/lab/trace-tree.jq`).
- Laboratorios: `make smoke`, `lab-discovery`, `lab-lb`, `lab-resilience`, `lab-observability`, `lab-config`.
- Dashboard: editar `deployments/grafana/build-dashboard.py` y `make dashboard`.
- Capturas de Playwright: solo dentro del repo (`.playwright-mcp/`, ignorado por git).
