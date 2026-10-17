# Estado actual

**Última actualización:** 2026-10-17 (sesión 3)
**Fase en curso:** Fase 7 terminada y verificada → siguiente: **Fase 8 (Validación final y documentación)**

## Próximo paso concreto
1. README.md completo en español con los 15 puntos de main.md §19:
   objetivos, arquitectura (diagrama), componentes, requisitos, comandos, URLs, ejemplos REST, gRPC,
   escalado, discovery y balanceo, circuit breaker, trazas, Grafana, configuración centralizada y
   solución de problemas (usar docs/fases/*.md como fuente).
2. Tabla Spring Boot / Spring Cloud ↔ laboratorio, con las **diferencias conceptuales** (no solo equivalencias).
3. Validación final desde cero: `make clean && make up` (volúmenes nuevos) → `make test-integration`
   → `make labs` → recorrido del frontend con Playwright.
4. Revisar main.md §21 (requisitos de calidad) punto por punto y anotar en PLAN cómo se cumple cada uno.

## Estado de la plataforma
- `make up`: postgres, consul, inventory, orders, traefik, frontend, otel-collector, jaeger, prometheus y grafana.
- Entrada única: http://127.0.0.1:8080 (frontend + API). Herramientas en 127.0.0.1: Traefik 8090,
  Consul 8500, Jaeger 16686, Prometheus 9090, Grafana 3000, PostgreSQL 55432.
- Los microservicios no publican puertos; los scripts usan el gateway y `docker compose exec consul curl`.

## Bloqueos / preguntas abiertas
- Ninguno. Los cambios de la Fase 7 están **sin commit**.

## Notas rápidas para retomar
- Fuentes de Kratos v3: `$(go env GOMODCACHE)/github.com/go-kratos/kratos/v3@v3.0.0/`
- Contrib v3 fijados a `v3.0.0-20260626125723-668db92c2c00`.
- Peticiones REST con `Content-Type: application/json`; los int64 se devuelven como cadenas (ADR-010).
- Lo dinámico manda en Consul KV: `PUT /admin/<svc>/config` (o `set_config` en lib.sh).
- Tras recrear Orders, esperar a Traefik con `wait_gateway` (lib.sh); `orders_with` ya lo hace.
- Frontend: npm del host roto → `make frontend-check` (contenedor Node 24). TypeScript fijado a 6.0.3.
- Jaeger 2.22: solo `/api/v3/...`; Playwright: cambiar solo el hash no recarga (añadir `?v=N`).
- Laboratorios: `make labs` (o uno a uno: smoke, lab-discovery, lab-lb, lab-resilience, lab-observability, lab-config, lab-gateway).
