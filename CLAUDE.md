# Go Microservices Lab — instrucciones para Claude

Laboratorio de microservicios en Go + Kratos v3 (Orders, Inventory) con Consul, Traefik,
OpenTelemetry, Jaeger, Prometheus, Grafana y frontend Svelte. La especificación completa
está en [main.md](main.md) y es la fuente de verdad de los requisitos.

## Harness de desarrollo (leer siempre)

El proceso se documenta en `docs/harness/` para poder retomarlo entre sesiones:

| Archivo | Propósito | Cuándo se actualiza |
|---|---|---|
| `docs/harness/PROGRESS.md` | Estado actual, fase en curso, **próximo paso concreto**, bloqueos | Al cerrar cada sesión y al terminar cada tarea relevante |
| `docs/harness/PLAN.md` | Plan por fases con checklist y criterios de aceptación | Al marcar tareas `[x]` (solo tras verificarlas) |
| `docs/harness/DECISIONS.md` | Decisiones de arquitectura (ADR) con motivo y alternativas | Cada vez que se toma o cambia una decisión |
| `docs/harness/SESSION_LOG.md` | Bitácora cronológica, una entrada por sesión | Al cerrar cada sesión |
| `docs/harness/FEASIBILITY.md` | Análisis de viabilidad y verificación de dependencias | Si cambian versiones o aparecen limitaciones |

### Protocolo de sesión

1. **Al empezar**: leer `PROGRESS.md` (el hook SessionStart lo inyecta) y la fase en curso de `PLAN.md`.
   Si hay dudas sobre por qué algo es así, consultar `DECISIONS.md` antes de cambiarlo.
2. **Durante**: trabajar una sola fase a la vez (main.md §20). No avanzar de fase sin cumplir
   sus criterios de aceptación.
3. **Al cerrar** (o con `/cerrar-sesion`): actualizar `PROGRESS.md`, marcar `PLAN.md`,
   añadir entrada a `SESSION_LOG.md` y registrar ADRs nuevos.

### Reglas

- Una tarea solo se marca `[x]` si se compiló, se probó y se verificó su funcionamiento real.
  Registrar en la bitácora el comando de verificación usado.
- **Kratos v3**: el conocimiento previo del modelo es mayormente de v2. Antes de usar una API de
  Kratos, verificarla en el código fuente del módulo:
  `$(go env GOMODCACHE)/github.com/go-kratos/kratos/v3@v3.0.0/` y los contrib en
  `.../kratos/contrib/<x>/v3@<pseudo-version>/`. Nunca importar rutas `/v2`.
- Logging con `log/slog` (Kratos v3 ya no tiene `log.Helper`).
- DI con constructores explícitos (no Wire: está archivado).
- Sin secretos en Consul KV ni en el repo; credenciales por variables de entorno.
- Comentarios y documentación en español; identificadores de código en inglés.

## Comandos (se completan a medida que existan)

```bash
make help            # lista de objetivos
make proto           # genera código desde api/proto
make test            # pruebas unitarias
make up / make down  # Docker Compose
```
