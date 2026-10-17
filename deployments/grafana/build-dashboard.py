#!/usr/bin/env python3
"""Genera deployments/grafana/dashboards/lab-overview.json.

El dashboard se versiona como JSON generado: editar este script y volver a
ejecutarlo (python3 deployments/grafana/build-dashboard.py).
"""
import json
import pathlib

DS = {"type": "prometheus", "uid": "prometheus"}
panels, y = [], 0


def row(title):
    global y
    panels.append({"type": "row", "title": title, "collapsed": False,
                   "gridPos": {"h": 1, "w": 24, "x": 0, "y": y}, "id": len(panels) + 1})
    y += 1


def ts(title, targets, x, w=8, h=8, unit="short", desc="", stack=False, extra=None):
    p = {
        "type": "timeseries", "title": title, "description": desc, "datasource": DS,
        "id": len(panels) + 1, "gridPos": {"h": h, "w": w, "x": x, "y": y},
        "fieldConfig": {"defaults": {"unit": unit, "custom": {
            "drawStyle": "line", "lineWidth": 1, "fillOpacity": 10, "showPoints": "never",
            "stacking": {"mode": "normal" if stack else "none"}}}, "overrides": []},
        "options": {"legend": {"displayMode": "list", "placement": "bottom"},
                    "tooltip": {"mode": "multi", "sort": "desc"}},
        "targets": [{"datasource": DS, "expr": e, "legendFormat": l, "refId": chr(65 + i)}
                    for i, (e, l) in enumerate(targets)],
    }
    if extra:
        p.update(extra)
    panels.append(p)


def nl():
    global y
    y += 8


# --- Resumen ---------------------------------------------------------------
row("Resumen")
ts("Peticiones por segundo (servidor)",
   [('sum by (job) (rate(server_requests_code_total[1m]))', "{{job}}")], 0, unit="reqps",
   desc="Peticiones atendidas por servicio (HTTP + gRPC).")
ts("Errores por segundo (servidor)",
   [('sum by (job, code) (rate(server_requests_code_total{code!~"2.."}[1m]))', "{{job}} {{code}}")], 8, unit="reqps",
   desc="Respuestas con código distinto de 2xx. Los 409 de negocio (sin stock) también cuentan.")
ts("Latencia p95 (servidor)",
   [('histogram_quantile(0.95, sum by (le, job) (rate(server_requests_seconds_bucket[1m])))', "p95 {{job}}"),
    ('histogram_quantile(0.50, sum by (le, job) (rate(server_requests_seconds_bucket[1m])))', "p50 {{job}}")],
   16, unit="s", desc="Cuantiles calculados a partir del histograma de latencia.")
nl()

# --- API Gateway ----------------------------------------------------------------
row("API Gateway (Traefik)")
ts("Peticiones por servicio de Traefik",
   [('sum by (service) (rate(traefik_service_requests_total[1m]))', "{{service}}")], 0, w=8, unit="reqps",
   desc="Lo que entra por el gateway (navegador, curl): frontend, orders e inventory.")
ts("Respuestas no 2xx del gateway",
   [('sum by (service, code) (rate(traefik_service_requests_total{code!~"2.."}[1m]))', "{{service}} {{code}}")], 8, w=8,
   unit="reqps", desc="Incluye los 403 del ipAllowList (endpoints de laboratorio y admin).")
ts("Latencia p95 en el gateway",
   [('histogram_quantile(0.95, sum by (le, service) (rate(traefik_service_request_duration_seconds_bucket[1m])))', "{{service}}")],
   16, w=8, unit="s", desc="Medida en Traefik: incluye el salto del proxy.")
nl()

# --- Orders -> Inventory ----------------------------------------------------
row("Orders → Inventory (cliente gRPC)")
ts("Llamadas por resultado",
   [('sum by (code) (rate(client_requests_code_total{job="orders-service"}[1m]))', "{{code}}")], 0, unit="reqps",
   desc="Llamadas lógicas (tras reintentos). 503 incluye los rechazos del circuit breaker.")
ts("Latencia de las llamadas (p95 / p50)",
   [('histogram_quantile(0.95, sum by (le) (rate(client_requests_seconds_bucket{job="orders-service"}[1m])))', "p95"),
    ('histogram_quantile(0.50, sum by (le) (rate(client_requests_seconds_bucket{job="orders-service"}[1m])))', "p50")],
   8, unit="s", desc="Incluye los reintentos: el máximo lo marca el presupuesto total (2s).")
ts("Distribución por instancia (balanceo del cliente)",
   [('sum by (target_instance) (rate(client_requests_by_instance_total{job="orders-service", code="200"}[1m]))', "{{target_instance}}")],
   16, unit="reqps", stack=True, desc="Qué réplica de Inventory atendió cada llamada correcta.")
nl()

# --- Resiliencia --------------------------------------------------------------
row("Resiliencia")
panels.append({
    "type": "state-timeline", "title": "Estado del circuit breaker", "datasource": DS,
    "id": len(panels) + 1, "gridPos": {"h": 8, "w": 8, "x": 0, "y": y},
    "description": "0 = closed, 1 = half-open, 2 = open (por instancia de Orders).",
    "fieldConfig": {"defaults": {
        "mappings": [
            {"type": "range", "options": {"from": -0.5, "to": 0.5, "result": {"text": "CLOSED", "color": "green", "index": 0}}},
            {"type": "range", "options": {"from": 0.5, "to": 1.5, "result": {"text": "HALF-OPEN", "color": "yellow", "index": 1}}},
            {"type": "range", "options": {"from": 1.5, "to": 2.5, "result": {"text": "OPEN", "color": "red", "index": 2}}}],
        "color": {"mode": "fixed", "fixedColor": "green"}},
        "overrides": []},
    "options": {"showValue": "auto", "mergeValues": True, "rowHeight": 0.9,
                "legend": {"showLegend": False}},
    "targets": [{"datasource": DS, "expr": 'max by (instance) (circuit_breaker_state)', "legendFormat": "{{instance}}", "refId": "A"}],
})
ts("Breaker: intentos y rechazos por segundo",
   [('sum(rate(circuit_breaker_calls_total{result="success"}[1m]))', "intentos correctos"),
    ('sum(rate(circuit_breaker_calls_total{result="failure"}[1m]))', "intentos fallidos"),
    ('sum(rate(circuit_breaker_rejected_total[1m]))', "rechazados localmente")], 8, unit="reqps",
   desc="Con el circuito abierto, las llamadas se rechazan sin llegar a Inventory.")
ts("Reintentos por segundo",
   [('sum by (event) (rate(client_retry_events_total{event!="attempts"}[1m]))', "{{event}}")], 16, unit="reqps",
   desc="retries = reintentos efectuados; recovered = llamadas salvadas por un reintento; exhausted = agotadas.")
nl()

# --- Negocio -------------------------------------------------------------------
row("Negocio")
ts("Pedidos por estado",
   [('sum by (status, rejection_code) (rate(orders_processed_total[1m]))', "{{status}} {{rejection_code}}")], 0,
   unit="reqps", stack=True, desc="CONFIRMED, REJECTED (con su código) y CANCELLED.")
ts("Reservas de stock por resultado",
   [('sum by (result) (rate(inventory_reservations_total[1m]))', "{{result}}")], 8, unit="reqps",
   desc="replayed = reintento detectado por la clave de idempotencia.")
ts("Latencia de PostgreSQL (p95)",
   [('histogram_quantile(0.95, sum by (le, job) (rate(db_client_operation_duration_seconds_bucket[1m])))', "{{job}}")],
   16, unit="s", desc="Duración de las consultas (spans de otelpgx).")
nl()

# --- Salud y recursos -------------------------------------------------------------
row("Salud y recursos")
ts("Readiness por instancia",
   [('max by (job, instance) (service_ready)', "{{job}} {{instance}}")], 0, unit="short",
   desc="1 = preparada para recibir tráfico (base de datos accesible y sin fallo simulado).",
   extra={"fieldConfig": {"defaults": {"min": 0, "max": 1, "unit": "short",
                                        "custom": {"drawStyle": "line", "lineWidth": 2, "fillOpacity": 0, "showPoints": "never"}},
                           "overrides": []}})
ts("Memoria en uso (Go runtime)",
   [('sum by (job, instance) (go_memory_used_bytes)', "{{job}} {{instance}}")], 8, unit="bytes")
ts("Goroutines y conexiones a PostgreSQL",
   [('sum by (job, instance) (go_goroutine_count)', "goroutines {{job}} {{instance}}"),
    ('sum by (job, instance) (pgxpool_acquired_connections)', "pg en uso {{job}} {{instance}}")], 16)
nl()

# --- Configuración ----------------------------------------------------------------
row("Configuración dinámica (Consul KV)")
ts("Recargas de configuración",
   [('sum by (job, result) (increase(config_reloads_total[1m]))', "{{job}} {{result}}")], 0, w=12,
   desc="applied = documento válido aplicado; rejected = documento inválido (se mantiene el anterior).")
ts("Latencia de las llamadas a Inventory (p95)",
   [('histogram_quantile(0.95, sum by (le) (rate(client_requests_seconds_bucket{job="orders-service"}[1m])))', "p95")],
   12, w=12, unit="s", desc="Refleja en caliente los cambios de inventory.timeout.")
nl()

dashboard = {
    "uid": "lab-overview",
    "title": "Go Microservices Lab — Visión general",
    "tags": ["lab", "kratos"],
    "timezone": "browser",
    "refresh": "5s",
    "time": {"from": "now-15m", "to": "now"},
    "schemaVersion": 39,
    "editable": False,
    "annotations": {"list": [{
        "name": "Cambios del circuit breaker", "datasource": DS, "enable": True,
        "iconColor": "red", "expr": 'sum(changes(circuit_breaker_state[20s])) > 0',
        "step": "5s", "titleFormat": "Circuit breaker", "textFormat": "cambio de estado",
    }]},
    "links": [
        {"title": "Jaeger (trazas)", "url": "http://localhost:16686/search", "type": "link", "icon": "external link", "targetBlank": True},
        {"title": "Consul", "url": "http://localhost:8500/ui", "type": "link", "icon": "external link", "targetBlank": True},
        {"title": "Prometheus", "url": "http://localhost:9090", "type": "link", "icon": "external link", "targetBlank": True},
        {"title": "Frontend", "url": "http://localhost:8080", "type": "link", "icon": "external link", "targetBlank": True},
    ],
    "panels": panels,
}
out = pathlib.Path(__file__).parent / "dashboards" / "lab-overview.json"
out.write_text(json.dumps(dashboard, indent=2, ensure_ascii=False) + "\n")
print(f"{out}: {len(panels)} paneles")
