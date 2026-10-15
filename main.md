# Proyecto: Go Microservices Lab — Arquitectura de microservicios con Go y Kratos

## 1. Objetivo general

Desarrollar una aplicación completa de prueba y aprendizaje de arquitecturas de microservicios utilizando **Go y Kratos v3**, incorporando los principales patrones y componentes habitualmente utilizados en Spring Boot y Spring Cloud.

El proyecto debe funcionar íntegramente en una máquina local mediante Docker Compose, sin depender de servicios cloud, cuentas externas ni infraestructura de pago.

El objetivo principal no es solamente desarrollar dos microservicios funcionales, sino disponer de un laboratorio donde sea posible observar, configurar y experimentar con:

- Service Discovery.
- Service Registration.
- Client-side Load Balancing.
- API Gateway.
- Circuit Breaker.
- Timeouts y retries controlados.
- Configuración centralizada.
- Distributed Tracing.
- Logging estructurado.
- Métricas y monitoreo.
- Health Checks.
- Escalado horizontal.
- Tolerancia a fallos.
- Comunicación REST y gRPC.

El código debe ser modular, estar documentado y seguir buenas prácticas de Go.

No utilizar Java, Spring Boot ni otros frameworks backend. Los dos microservicios deben implementarse exclusivamente en Go.

## 2. Tecnologías

### Backend

- Go, versión estable compatible con Kratos v3.
- Kratos v3, utilizando sus módulos oficiales y contrib.
- gRPC y Protocol Buffers para comunicación interna.
- HTTP REST para acceso desde el exterior.
- PostgreSQL para persistencia.
- Migraciones SQL versionadas.
- Inyección de dependencias mediante constructores explícitos, evitando depender de herramientas archivadas.

Consultar la documentación vigente de Kratos v3 antes de generar el proyecto. No mezclar APIs ni imports de Kratos v2 y v3.

### Infraestructura

- Docker.
- Docker Compose.
- Consul para registro, descubrimiento y configuración KV.
- Traefik como API Gateway y reverse proxy.
- OpenTelemetry para instrumentación.
- OpenTelemetry Collector.
- Jaeger para trazabilidad.
- Prometheus para métricas.
- Grafana para visualización.

### Frontend

Desarrollar una interfaz web con:

- Svelte y TypeScript.
- Vite.
- Diseño moderno, sencillo y responsive.
- Comunicación con el backend mediante HTTP REST.

El frontend debe servir tanto para gestionar pedidos e inventario como para ejecutar experimentos de resiliencia.

## 3. Arquitectura general

Implementar dos microservicios independientes:

### Microservicio 1: Orders Service

Responsable de administrar pedidos.

Funciones:

- Crear pedidos.
- Consultar pedidos.
- Listar pedidos.
- Consultar el estado de un pedido.
- Cancelar pedidos.
- Consultar disponibilidad de productos mediante Inventory Service.
- Reservar stock cuando se confirma un pedido.
- Gestionar errores cuando Inventory Service no está disponible.

Cada pedido contendrá:

- ID único.
- Fecha de creación.
- Estado.
- Lista de productos.
- Cantidad de cada producto.
- Precio unitario.
- Importe total.

Estados sugeridos:

- PENDING
- CONFIRMED
- REJECTED
- CANCELLED

El servicio utilizará PostgreSQL para almacenar sus datos.

### Microservicio 2: Inventory Service

Responsable de administrar productos y existencias.

Funciones:

- Crear productos.
- Modificar productos.
- Listar productos.
- Consultar stock.
- Incrementar stock.
- Reducir stock.
- Reservar unidades.
- Liberar unidades reservadas.
- Consultar disponibilidad.

Cada producto tendrá:

- ID.
- SKU.
- Nombre.
- Descripción.
- Precio.
- Stock disponible.
- Stock reservado.

Implementar reservas de stock con operaciones transaccionales que impidan cantidades negativas o sobreventa ante solicitudes concurrentes.

La operación de reserva debe admitir una clave de idempotencia para evitar duplicaciones cuando se reintentan solicitudes.

Para simplificar el laboratorio, una instancia PostgreSQL puede alojar dos bases de datos lógicas independientes, una por microservicio. Ningún servicio debe acceder directamente a las tablas del otro.

## 4. Comunicación entre microservicios

Utilizar gRPC para la comunicación interna.

Orders Service debe comunicarse con Inventory Service mediante un cliente gRPC generado desde Protocol Buffers.

Definir contratos para:

- GetProduct
- CheckAvailability
- ReserveStock
- ReleaseStock

Evitar direcciones IP y puertos de instancias codificados en el cliente.

La resolución de servicios deberá realizarse mediante Consul y los mecanismos de descubrimiento de Kratos.

Implementar propagación de:

- Context.
- Deadlines.
- Trace IDs.
- Errores gRPC.
- Metadatos relevantes.

Utilizar contratos Protobuf versionados.

## 5. Service Discovery con Consul

Implementar registro automático de ambos microservicios en Consul.

Cada instancia deberá registrar:

- Nombre del servicio.
- ID de instancia.
- Dirección.
- Puerto gRPC.
- Puerto HTTP si corresponde.
- Estado de salud.
- Metadatos útiles.

Configurar health checks y desregistro de instancias que dejen de estar disponibles.

Orders Service debe descubrir Inventory Service utilizando su nombre lógico.

Ejemplo conceptual:

inventory-service

No utilizar una dirección fija correspondiente a un contenedor concreto.

Preparar pruebas para:

1. Iniciar una instancia de Inventory.
2. Iniciar una segunda instancia.
3. Comprobar que Consul registra ambas.
4. Detener una instancia.
5. Comprobar que deja de recibir tráfico después de actualizarse el estado de salud.
6. Reiniciarla y verificar su reincorporación.

Utilizar las integraciones compatibles con Kratos v3.

## 6. Load Balancing

Implementar balanceo de carga del lado cliente utilizando las capacidades de Kratos y el descubrimiento de Consul.

Permitir ejecutar tres réplicas de Inventory Service.

Cada instancia debe disponer de un identificador único visible en sus respuestas diagnósticas.

Crear un endpoint de diagnóstico que permita identificar qué instancia atendió cada solicitud.

La interfaz web debe incluir una herramienta para enviar múltiples solicitudes consecutivas y mostrar la distribución por instancia.

Implementar, cuando lo permita la API vigente, una estrategia de balanceo configurable. Priorizar primero la implementación predeterminada de Kratos.

El objetivo es demostrar que el cliente no depende de una única instancia.

Distinguir claramente este balanceo interno del balanceo HTTP que puede realizar Traefik.

## 7. Circuit Breaker

Implementar un circuit breaker en las llamadas desde Orders hacia Inventory utilizando el middleware de Kratos v3.

Permitir configurar:

- Umbral de fallos.
- Ventana de evaluación.
- Tiempo de recuperación.
- Número o proporción de errores para abrir el circuito, según la implementación seleccionada.

Demostrar los tres estados:

- CLOSED: las solicitudes se ejecutan normalmente.
- OPEN: las solicitudes se rechazan localmente.
- HALF-OPEN: se permiten solicitudes de prueba para comprobar la recuperación.

Agregar un modo experimental para simular fallos del Inventory Service.

Permitir:

- Introducir latencia artificial.
- Simular errores temporales.
- Simular indisponibilidad.
- Recuperar el funcionamiento normal.

Las simulaciones deben estar claramente separadas de la lógica de negocio, desactivadas por defecto y disponibles solamente en modo laboratorio.

El frontend debe mostrar métricas que permitan interpretar el comportamiento del circuit breaker.

No inventar APIs de inspección de estado si la implementación elegida no las expone. Cuando sea necesario, crear un adaptador instrumentado.

## 8. Timeouts y Retries

Configurar tiempos máximos de espera para las llamadas gRPC.

Agregar reintentos controlados únicamente en operaciones donde sean seguros.

Utilizar:

- Exponential backoff.
- Jitter.
- Número máximo de intentos.
- Respeto del deadline original.

No reintentar operaciones de reserva de stock sin garantías de idempotencia.

Permitir comparar los resultados cuando:

- No hay retries.
- Hay retries.
- El circuit breaker está habilitado.
- El circuit breaker está deshabilitado.

Documentar las interacciones entre timeouts, retries y circuit breaker.

## 9. Configuración centralizada

Utilizar Consul KV para administrar parámetros de configuración.

Ejemplos:

- Timeout de solicitudes.
- Parámetros del circuit breaker.
- Nivel de logging.
- Activación de modos de simulación.
- Parámetros de funcionalidades experimentales.

Implementar un proveedor de configuración compatible con Kratos v3.

Cuando el componente lo permita, incorporar recarga dinámica.

Evitar que las modificaciones de configuración provoquen condiciones de carrera.

Validar todos los valores antes de aplicarlos.

No almacenar contraseñas ni secretos en texto plano dentro de Consul KV. Utilizar variables de entorno o secretos montados para credenciales.

Documentar qué parámetros requieren reiniciar el proceso y cuáles pueden actualizarse dinámicamente.

## 10. Distributed Tracing

Integrar OpenTelemetry en ambos microservicios.

Enviar las trazas al OpenTelemetry Collector y visualizarlas mediante Jaeger.

Cada solicitud debe permitir seguir el recorrido completo:

Cliente → API Gateway → Orders Service → Inventory Service.

Generar spans para:

- Recepción de la solicitud.
- Validación de pedido.
- Consulta de inventario.
- Reserva de stock.
- Acceso a PostgreSQL.
- Respuesta final.

Configurar propagación W3C Trace Context cuando corresponda.

Implementar atributos útiles:

- Nombre del servicio.
- Operación.
- Identificador de instancia.
- Resultado.
- Duración.
- Tipo de error.

Evitar exponer datos sensibles dentro de las trazas.

El frontend debe mostrar el identificador de trazabilidad cuando sea apropiado y facilitar la consulta de la traza en Jaeger.

## 11. Métricas y monitoreo

Integrar Prometheus y Grafana.

Recopilar:

- Número total de solicitudes.
- Solicitudes por segundo.
- Latencia.
- Errores HTTP y gRPC.
- Cantidad de llamadas fallidas entre servicios.
- Rechazos del circuit breaker.
- Distribución de solicitudes entre instancias.
- Estado de salud.
- Recursos de los procesos, cuando estén disponibles.

Utilizar histogramas para medir latencias.

Crear al menos un dashboard de Grafana aprovisionado automáticamente mediante archivos de configuración.

El dashboard debe presentar métricas de Orders e Inventory.

Agregar gráficos para comparar el comportamiento antes, durante y después de un fallo simulado.

No utilizar identificadores de pedido o usuario como etiquetas de métricas Prometheus para evitar cardinalidad excesiva.

## 12. API Gateway

Configurar Traefik como punto de entrada HTTP.

Publicar rutas como:

- /api/orders
- /api/products
- /api/inventory

El frontend no debe comunicarse directamente con puertos internos de los microservicios.

Implementar:

- Enrutamiento HTTP.
- Middleware básico de logging.
- Cabeceras apropiadas.
- Health checks cuando corresponda.
- Configuración local de CORS si fuera necesaria.

Mantener separadas las responsabilidades de Traefik y los clientes gRPC internos.

## 13. Interfaz web de laboratorio

Desarrollar una aplicación Svelte con las siguientes secciones.

### Dashboard

Mostrar:

- Microservicios activos.
- Instancias detectadas.
- Estado de salud.
- Solicitudes procesadas.
- Errores recientes.
- Latencia aproximada.
- Accesos a Consul, Jaeger, Prometheus y Grafana.

El frontend debe consultar endpoints de diagnóstico propios. No conectarse directamente a los sockets de Docker ni acceder a credenciales de infraestructura.

### Products

Permitir:

- Crear productos.
- Listar productos.
- Modificar inventario.
- Consultar disponibilidad.

### Orders

Permitir:

- Crear pedidos.
- Seleccionar productos.
- Consultar estados.
- Ver pedidos rechazados.
- Identificar errores producidos por falta de stock o indisponibilidad.

### Load Balancing Lab

Permitir enviar lotes de solicitudes de prueba.

Mostrar:

- Cantidad de solicitudes.
- Respuestas satisfactorias.
- Respuestas fallidas.
- Distribución por instancia.
- Latencia promedio.

### Circuit Breaker Lab

Permitir activar simulaciones de:

- Errores.
- Retrasos.
- Indisponibilidad temporal.

Mostrar la evolución de los estados del circuito cuando esa información esté disponible mediante la instrumentación.

### Tracing

Mostrar las últimas operaciones relevantes y ofrecer enlaces a Jaeger usando identificadores de traza.

### Configuration

Visualizar parámetros no sensibles y modificar los que sean compatibles con recarga dinámica.

Los endpoints administrativos y de simulación deberán estar protegidos o restringidos a un entorno local de desarrollo.

## 14. Logging

Utilizar logging estructurado con `log/slog` o el mecanismo compatible recomendado por Kratos v3.

Cada log relevante debe incluir:

- Timestamp.
- Nivel.
- Servicio.
- Instancia.
- Operación.
- Trace ID.
- Mensaje.
- Error, si corresponde.

Permitir cambiar el nivel de logging.

Evitar imprimir secretos.

Los logs de cada servicio deben visualizarse mediante Docker Compose.

## 15. Health Checks

Implementar:

- Liveness.
- Readiness.
- Health checks gRPC.

Readiness debe contemplar las dependencias necesarias para que la instancia pueda atender solicitudes.

Distinguir entre una instancia viva y una instancia preparada para recibir tráfico.

Integrar estos controles con Consul y Docker cuando corresponda.

## 16. Docker Compose

Preparar un archivo Compose para ejecutar la plataforma localmente.

Debe permitir:

- Construir las imágenes de los microservicios.
- Iniciar todos los componentes.
- Consultar logs.
- Ejecutar múltiples réplicas de Inventory.
- Reiniciar componentes individuales.
- Detener servicios para realizar pruebas.
- Conservar los datos de PostgreSQL.

Utilizar volúmenes persistentes para los componentes que lo necesiten.

Implementar health checks y dependencias de arranque razonables.

No utilizar `container_name` fijo en servicios que deban escalarse.

Exponer al host solamente los puertos necesarios para la interfaz web y las herramientas del laboratorio.

La solución debe funcionar en Linux con Docker Engine y Docker Compose.

## 17. Organización del repositorio

Utilizar un monorepo, con una estructura aproximada como:

```text
go-microservices-lab/
├── api/
│   └── proto/
├── services/
│   ├── orders/
│   │   ├── cmd/
│   │   ├── internal/
│   │   │   ├── biz/
│   │   │   ├── data/
│   │   │   ├── service/
│   │   │   ├── server/
│   │   │   └── conf/
│   │   ├── migrations/
│   │   └── Dockerfile
│   └── inventory/
│       ├── cmd/
│       ├── internal/
│       ├── migrations/
│       └── Dockerfile
├── frontend/
├── deployments/
│   ├── consul/
│   ├── traefik/
│   ├── otel/
│   ├── prometheus/
│   └── grafana/
├── scripts/
├── tests/
├── docs/
├── compose.yaml
├── Makefile
└── README.md
```

Adaptar los directorios internos si las convenciones de Kratos v3 lo requieren.

Evitar duplicar código común innecesariamente.

Mantener separados los contratos de comunicación y la implementación de cada servicio.

## 18. Pruebas

Crear pruebas automatizadas para:

### Pruebas unitarias

- Lógica de pedidos.
- Validaciones.
- Gestión de inventario.
- Transiciones de estado.
- Cálculos de stock.

### Pruebas de integración

- Comunicación gRPC.
- Persistencia PostgreSQL.
- Operaciones de reserva.
- Idempotencia.
- Service Discovery.

### Pruebas de resiliencia

- Caída de una instancia de Inventory.
- Caída de todas las instancias.
- Latencia excesiva.
- Errores repetidos.
- Apertura y recuperación del circuit breaker.
- Recuperación del servicio.
- Concurrencia sobre el mismo producto.

Crear scripts para ejecutar los escenarios de laboratorio de forma reproducible.

Los scripts deben mostrar claramente qué se está comprobando y cuáles fueron los resultados.

## 19. Documentación

Crear un README completo en español.

Debe incluir:

1. Objetivos del proyecto.
2. Arquitectura general.
3. Descripción de cada componente.
4. Requisitos de instalación.
5. Comandos para ejecutar Docker Compose.
6. URLs de las interfaces.
7. Ejemplos de llamadas REST.
8. Explicación de la comunicación gRPC.
9. Procedimiento para escalar Inventory.
10. Pruebas de discovery y balanceo.
11. Prueba del circuit breaker.
12. Visualización de trazas.
13. Uso de Grafana.
14. Modificación de la configuración centralizada.
15. Solución de problemas comunes.

Agregar una tabla que relacione cada componente con su equivalente en Spring Boot / Spring Cloud.

Ejemplo:

| Spring Cloud | Implementación del laboratorio |
|---|---|
| Spring Boot | Kratos |
| Eureka Server | Consul |
| Spring Cloud Gateway | Traefik |
| Spring Cloud LoadBalancer | Kratos + Consul |
| Resilience4j | Kratos Circuit Breaker |
| Spring Cloud Config | Consul KV |
| Micrometer Tracing | OpenTelemetry |
| Zipkin/Jaeger | Jaeger |
| Actuator/Micrometer | OpenTelemetry + Prometheus |

Documentar también las diferencias conceptuales, no solamente las equivalencias.

## 20. Metodología de implementación

IMPORTANTE: No intentar implementar toda la arquitectura de una sola vez.

Trabajar por fases, verificando el resultado antes de continuar.

**Fase 1 — Base funcional**

- Crear el monorepo.
- Implementar Orders.
- Implementar Inventory.
- Configurar PostgreSQL.
- Implementar REST y gRPC.
- Crear pruebas básicas.
- Ejecutar con Docker Compose.

**Fase 2 — Service Discovery**

- Incorporar Consul.
- Registrar los microservicios.
- Implementar descubrimiento de instancias.
- Verificar la comunicación mediante nombres de servicio.

**Fase 3 — Load Balancing**

- Permitir varias instancias de Inventory.
- Comprobar distribución de solicitudes.
- Probar la caída de una instancia.

**Fase 4 — Resiliencia**

- Agregar circuit breaker.
- Configurar timeouts.
- Implementar retries seguros.
- Crear simulación de fallos.
- Verificar recuperación.

**Fase 5 — Observabilidad**

- Incorporar OpenTelemetry.
- Configurar Jaeger.
- Incorporar Prometheus.
- Crear dashboards Grafana.
- Verificar propagación de trazas.

**Fase 6 — Configuración centralizada**

- Incorporar Consul KV.
- Configurar parámetros dinámicos.
- Validar modificaciones en tiempo de ejecución.

**Fase 7 — API Gateway y frontend**

- Configurar Traefik.
- Desarrollar Svelte.
- Implementar el dashboard.
- Crear las interfaces de experimentación.

**Fase 8 — Validación final**

- Ejecutar pruebas unitarias y de integración.
- Verificar todos los contenedores.
- Probar fallos y recuperación.
- Completar la documentación.

## 21. Requisitos de calidad

- Utilizar versiones estables y compatibles.
- Revisar la documentación oficial de Kratos v3 antes de implementar.
- No utilizar paquetes obsoletos.
- No introducir dependencias innecesarias.
- Aplicar manejo explícito de errores.
- Implementar apagado ordenado de procesos.
- Respetar cancelaciones mediante context.
- Utilizar migraciones reproducibles.
- Configurar correctamente los timeouts.
- Evitar condiciones de carrera.
- Mantener separadas lógica de negocio e infraestructura.
- No compartir bases de datos directamente entre microservicios.
- No incorporar Kubernetes.
- No incorporar servicios cloud.
- No implementar mecanismos complejos de autenticación empresarial en esta primera versión.

Priorizar claridad del código y facilidad de aprendizaje sobre optimizaciones prematuras.

## 22. Forma de trabajar

Antes de comenzar a programar:

1. Analizar el proyecto y los requerimientos.
2. Verificar la disponibilidad y compatibilidad de las dependencias.
3. Proponer la arquitectura definitiva.
4. Identificar posibles incompatibilidades o limitaciones.
5. Preparar un plan detallado por fases.

Después, comenzar con la Fase 1.

Al finalizar cada fase:

- Compilar los componentes afectados.
- Ejecutar las pruebas correspondientes.
- Verificar el funcionamiento real.
- Explicar brevemente qué se implementó.
- Documentar cómo probarlo.
- Indicar qué queda pendiente.

No declarar una funcionalidad terminada sin verificarla.

No sustituir funcionalidades por simulaciones, salvo los mecanismos de inyección de fallos expresamente solicitados.

Si una integración no está soportada directamente por Kratos v3, implementar un adaptador modular o proponer una alternativa compatible, explicando la decisión.

**Resultado esperado:** un laboratorio real y extensible que permita aprender los principales patrones de microservicios utilizados en Spring Cloud, pero implementados con Go, Kratos y componentes independientes de infraestructura.
