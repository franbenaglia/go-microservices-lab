// Herramientas del laboratorio, publicadas en el mismo host (127.0.0.1).
const host = typeof window !== 'undefined' ? window.location.hostname : 'localhost';

export const tools = {
  consul: `http://${host}:8500/ui`,
  jaeger: `http://${host}:16686`,
  prometheus: `http://${host}:9090`,
  grafana: `http://${host}:3000/d/lab-overview`,
  traefik: `http://${host}:8090/dashboard/`,
};

export const traceUrl = (id: string) => `${tools.jaeger}/trace/${id}`;
