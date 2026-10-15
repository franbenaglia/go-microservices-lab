# Estado actual

**Última actualización:** 2026-10-15 (sesión 1)
**Fase en curso:** Fase 2 terminada y verificada → siguiente: **Fase 3 (Load Balancing)**

## Próximo paso concreto
1. Probar `docker compose up -d --scale inventory=3` + `POST /api/orders/lab/lb {"count":30}` y
   documentar la distribución con wrr (selector por defecto de Kratos).
2. Selector configurable (`inventory.balancer: wrr|p2c|random`) con `selector.SetGlobalSelector`
   en el main de Orders. Es global al proceso y requiere reinicio; verificar en
   `$(go env GOMODCACHE)/github.com/go-kratos/kratos/v3@v3.0.0/selector/`.
3. `scripts/lab/loadbalancing.sh`: 3 réplicas, distribución, caída de una con tráfico continuo y
   comparación de selectores.
4. Documentar la diferencia entre balanceo interno (Kratos) y externo (Traefik, Fase 7).

## Estado de la plataforma
- `make up`: postgres, consul, inventory (escalable) y orders, todos healthy.
- Puertos en 127.0.0.1: orders 8081, consul 8500, postgres 55432, inventory efímero
  (`docker compose port --index 1 inventory 8000`).

## Bloqueos / preguntas abiertas
- Ninguno. Los cambios de la Fase 2 están **sin commit** (el usuario hizo commit y push de la Fase 1).

## Notas rápidas para retomar
- Fuentes de Kratos v3: `$(go env GOMODCACHE)/github.com/go-kratos/kratos/v3@v3.0.0/`
- Contrib v3 fijados a `v3.0.0-20260626125723-668db92c2c00` (registry/consul ya en go.mod).
- Peticiones REST con `Content-Type: application/json`; los int64 se devuelven como cadenas (ADR-010).
- El `curl` de snap no lee archivos de /tmp: usar `--data-binary @-` con stdin.
- Scripts de laboratorio: `scripts/lab/lib.sh` (check, wait_for, probe, consul_passing...).
