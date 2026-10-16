# Estado actual

**Última actualización:** 2026-10-16 (sesión 2)
**Fase en curso:** Fase 5 terminada y verificada → siguiente: **Fase 6 (Configuración centralizada en Consul KV)**

## Próximo paso concreto
1. Revisar `$(go env GOMODCACHE)/github.com/go-kratos/kratos/contrib/config/consul/v3@v3.0.0-20260626125723-668db92c2c00/`
   (config.go: `New(client, WithPath)`; watcher.go: cómo vigila y en qué formato entrega los valores).
2. Diseñar las claves: `config/<servicio>/` (parámetros dinámicos) y `lab/inventory/faults` (fallos
   para todas las réplicas). Sin secretos en KV.
3. Configuración dinámica con validación y `atomic.Pointer`: nivel de log (LevelVar ya existe),
   timeout de las llamadas, parámetros del breaker (recrear el breaker o un breaker "swappable"),
   retry, fallos de laboratorio.
4. Script de seed del KV y tabla en la documentación: parámetros dinámicos frente a los que requieren reinicio.
5. `scripts/lab/config.sh`: cambiar valores en KV y verificar su aplicación sin reinicio; rechazar los inválidos.

## Estado de la plataforma
- `make up`: postgres, consul, inventory (escalable), orders, otel-collector, jaeger, prometheus y grafana.
- Puertos en 127.0.0.1: orders 8081, consul 8500, postgres 55432, jaeger 16686, prometheus 9090,
  grafana 3000, inventory efímero.
- Variables de compose: INVENTORY_BALANCER, INVENTORY_WEIGHT, BREAKER_ENABLED, BREAKER_KIND,
  RETRY_ENABLED, LAB_MODE; `.env` necesita GRAFANA_ADMIN_PASSWORD (añadido a .env.example).

## Bloqueos / preguntas abiertas
- Ninguno. Los cambios de la Fase 5 están **sin commit**.

## Notas rápidas para retomar
- Fuentes de Kratos v3: `$(go env GOMODCACHE)/github.com/go-kratos/kratos/v3@v3.0.0/`
- Contrib v3 fijados a `v3.0.0-20260626125723-668db92c2c00` (registry/consul y otel ya en go.mod).
- Peticiones REST con `Content-Type: application/json`; los int64 se devuelven como cadenas (ADR-010).
- El `curl` de snap no lee ni escribe en /tmp: usar stdin/stdout (`--data-binary @-`, `-D -`).
- Jaeger 2.22: solo `/api/v3/...` (`scripts/lab/trace-tree.jq` dibuja el árbol de una traza).
- Laboratorios: `make smoke`, `lab-discovery`, `lab-lb`, `lab-resilience`, `lab-observability`.
- Dashboard: editar `deployments/grafana/build-dashboard.py` y `make dashboard` (no editar el JSON a mano).
- Capturas de Playwright: solo dentro del repo (`.playwright-mcp/`, ignorado por git).
