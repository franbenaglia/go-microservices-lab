# Estado actual

**Última actualización:** 2026-10-15 (sesión 1)
**Fase en curso:** Fase 1 terminada y verificada → siguiente: **Fase 2 (Service Discovery)**

## Próximo paso concreto
1. Añadir Consul a `compose.yaml` (modo servidor de un nodo, volumen `consuldata`, UI en 127.0.0.1:8500).
2. Añadir `github.com/go-kratos/kratos/contrib/registry/consul/v3@v3.0.0-20260626125723-668db92c2c00`
   y revisar su código fuente (registry.go, client.go) antes de usarlo.
3. Registrar ambos servicios (`kratos.Registrar`) con el check HTTP `/readyz` (ADR-006) y
   `WithDeregisterCriticalServiceAfter`.
4. Orders: `grpc.WithDiscovery(reg)` + endpoint `discovery:///inventory-service`.

## Estado de la plataforma
- `make up` levanta postgres, inventory y orders (todos healthy). Puertos temporales:
  orders 127.0.0.1:8081, inventory 127.0.0.1:8082, postgres 127.0.0.1:55432.
- `.env` local creado a partir de `.env.example` (no se versiona).

## Bloqueos / preguntas abiertas
- Ninguno. Repositorio git inicializado con remoto `origin`, **sin commits todavía** (pendiente de la
  autorización del usuario).

## Notas rápidas para retomar
- Fuentes de Kratos v3: `$(go env GOMODCACHE)/github.com/go-kratos/kratos/v3@v3.0.0/`
- Contrib v3 fijados a `v3.0.0-20260626125723-668db92c2c00`.
- Peticiones REST: hace falta `Content-Type: application/json`; los int64 se devuelven como cadenas (ADR-010).
- No usar el `protoc` de Anaconda; `make proto` usa buf vía `go tool`.
