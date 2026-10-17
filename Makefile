# Go Microservices Lab — tareas habituales. `make help` lista los objetivos.
SHELL := /bin/bash
COMPOSE ?= docker compose
-include .env
PG_HOST_PORT ?= 55432
TEST_DSN ?= postgres://postgres:$(POSTGRES_PASSWORD)@127.0.0.1:$(PG_HOST_PORT)/labtest?sslmode=disable

.DEFAULT_GOAL := help

.PHONY: help
help: ## Muestra esta ayuda
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: proto
proto: ## Genera el código Go desde api/proto (buf vía go tool)
	go tool buf lint
	go tool buf generate

.PHONY: build
build: ## Compila todos los paquetes
	go build ./...

.PHONY: vet
vet: ## go vet + comprobación de formato
	go vet ./...
	@test -z "$$(gofmt -l services pkg)" || (echo "gofmt pendiente:"; gofmt -l services pkg; exit 1)

.PHONY: test
test: ## Pruebas unitarias (las de integración se omiten sin base de datos)
	go test -race -count=1 ./...

.PHONY: test-integration
test-integration: ## Pruebas unitarias + integración contra PostgreSQL y Consul de compose
	$(COMPOSE) up -d --wait postgres consul
	LAB_TEST_PG_DSN='$(TEST_DSN)' LAB_TEST_CONSUL_ADDR=127.0.0.1:8500 go test -race -count=1 ./...

.PHONY: up
up: ## Construye e inicia la plataforma
	$(COMPOSE) up -d --build --wait

.PHONY: down
down: ## Detiene la plataforma (conserva los datos)
	$(COMPOSE) down

.PHONY: clean
clean: ## Detiene la plataforma y BORRA los volúmenes de datos
	$(COMPOSE) down -v

.PHONY: ps
ps: ## Estado de los contenedores
	$(COMPOSE) ps

.PHONY: logs
logs: ## Logs de todos los servicios (make logs S=orders para uno)
	$(COMPOSE) logs -f --tail=100 $(S)

.PHONY: lab-discovery
lab-discovery: ## Escenarios de Service Discovery (MODE=pause|kill|stop)
	./scripts/lab/discovery.sh

.PHONY: lab-lb
lab-lb: ## Escenarios de balanceo del lado cliente (MODE=stop|kill)
	./scripts/lab/loadbalancing.sh

.PHONY: lab-resilience
lab-resilience: ## Escenarios de resiliencia: retries, breaker, timeouts (args: A B C D E)
	./scripts/lab/resilience.sh $(ARGS)

.PHONY: lab-observability
lab-observability: ## Traza distribuida, logs correlacionados, métricas y dashboard
	./scripts/lab/observability.sh

.PHONY: lab-config
lab-config: ## Configuración dinámica en Consul KV: cambios en caliente y validación
	./scripts/lab/config.sh

.PHONY: lab-gateway
lab-gateway: ## API Gateway: rutas, cabeceras, ipAllowList, balanceo de Traefik, trazas
	./scripts/lab/gateway.sh

.PHONY: frontend-check
frontend-check: ## Type-check y build del frontend (en contenedor Node 24)
	docker run --rm --user "$$(id -u):$$(id -g)" -e HOME=/tmp -v "$$PWD/frontend":/app -w /app node:24.22.0-alpine \
	  sh -c 'npm ci --no-audit --no-fund && npm run check && npm run build'

.PHONY: frontend-dev
frontend-dev: ## Servidor de desarrollo de Vite (proxy de /api y /admin al gateway :8080)
	cd frontend && npm run dev

.PHONY: dashboard
dashboard: ## Regenera el dashboard de Grafana desde deployments/grafana/build-dashboard.py
	python3 deployments/grafana/build-dashboard.py

.PHONY: labs
labs: ## Ejecuta todos los laboratorios en secuencia
	./scripts/smoke.sh && ./scripts/lab/discovery.sh && ./scripts/lab/loadbalancing.sh && \
	./scripts/lab/resilience.sh && ./scripts/lab/observability.sh && ./scripts/lab/config.sh && ./scripts/lab/gateway.sh

.PHONY: smoke
smoke: ## Prueba de humo de la Fase 1 contra la plataforma en marcha
	./scripts/smoke.sh
