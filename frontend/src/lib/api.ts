import { record } from './ops.svelte';

// Cliente HTTP del frontend. Todo va al mismo origen (Traefik): /api/... y
// /admin/... . Cada respuesta trae X-Trace-Id (el ID de la traza que inició
// Traefik y continuaron los servicios), que se guarda para la página Tracing.

export class ApiError extends Error {
  constructor(
    public status: number,
    public reason: string,
    message: string,
    public traceId?: string,
  ) {
    super(message);
  }
}

export type Result<T> = { data: T; traceId?: string; durationMs: number };

// Operaciones de sondeo periódico que no se registran en la lista de trazas.
const QUIET = [/\/api\/orders\/lab\/(overview|resilience)$/, /\/admin\/[a-z]+\/config$/];

export async function request<T>(method: string, path: string, body?: unknown, quiet = false): Promise<Result<T>> {
  const started = performance.now();
  const res = await fetch(path, {
    method,
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const durationMs = performance.now() - started;
  const traceId = res.headers.get('X-Trace-Id') ?? undefined;
  if (!quiet && !(method === 'GET' && QUIET.some((r) => r.test(path)))) {
    record({ at: new Date().toISOString(), method, path, status: res.status, durationMs, traceId });
  }
  const text = await res.text();
  let payload: any = undefined;
  try {
    payload = text ? JSON.parse(text) : undefined;
  } catch {
    payload = text;
  }
  if (!res.ok) {
    const reason = payload?.reason ?? `HTTP_${res.status}`;
    const message = payload?.message ?? payload?.error ?? res.statusText;
    throw new ApiError(res.status, reason, message, traceId);
  }
  return { data: payload as T, traceId, durationMs };
}

export const get = <T>(path: string) => request<T>('GET', path);
export const post = <T>(path: string, body: unknown = {}, quiet = false) => request<T>('POST', path, body, quiet);
export const put = <T>(path: string, body: unknown) => request<T>('PUT', path, body);

export const errorText = (e: unknown) =>
  e instanceof ApiError ? `${e.reason}: ${e.message}` : e instanceof Error ? e.message : String(e);

// --- Tipos de la API (nombres de campo del .proto, int64 como cadena) ---

export type Product = {
  id: string;
  sku: string;
  name: string;
  description: string;
  price_cents: string;
  available: number;
  reserved: number;
  updated_at: string;
};

export type OrderItem = {
  product_id: string;
  sku: string;
  name: string;
  quantity: number;
  unit_price_cents: string;
  subtotal_cents: string;
};

export type Order = {
  id: string;
  status: string;
  items: OrderItem[];
  total_cents: string;
  rejection_code: string;
  rejection_reason: string;
  reservation_id: string;
  created_at: string;
};

export type Probe = {
  total: number;
  succeeded: number;
  failed: number;
  by_instance: { instance_id: string; count: number }[];
  errors: { reason: string; count: number }[];
  avg_latency_ms: number;
  max_latency_ms: number;
  sequence: string[];
  balancer: string;
};

export type Transition = { from: string; to: string; at: string };

export type Resilience = {
  call_timeout: string;
  balancer: string;
  breaker: {
    enabled: boolean;
    kind: string;
    state: string;
    state_observable: boolean;
    requests: number;
    total_failures: number;
    consecutive_failures: number;
    allowed: string;
    rejected: string;
    successes: string;
    failures: string;
    transitions: Transition[];
    failure_ratio: number;
    min_requests: number;
    consecutive_failures_threshold: number;
    window: string;
    open_timeout: string;
    half_open_max_requests: number;
  };
  retry: {
    enabled: boolean;
    max_attempts: number;
    attempt_timeout: string;
    calls: string;
    retries: string;
    recovered: string;
    exhausted: string;
  };
};

export type Check = { name: string; type: string; status: string };
export type Instance = {
  id: string;
  address: string;
  status: string;
  version: string;
  hostname: string;
  weight: string;
  checks: Check[];
};
export type Overview = {
  services: { name: string; instances: Instance[]; passing: number }[];
  metrics: { service: string; rps: number; errors_per_second: number; p95_ms: number; requests_last_15m: number }[];
  metrics_error: string;
  recent_rejections: { order_id: string; rejection_code: string; reason: string; at: string }[];
  breaker_state: string;
  generated_at: string;
};

export type ConfigView<T> = {
  service: string;
  instance: string;
  dynamic: T;
  static: Record<string, unknown>;
  restart_required: string[];
  status: { enabled: boolean; key: string; applied: number; rejected: number; last_applied?: string; last_error?: string; last_error_at?: string };
  consul_document?: string;
  consul_problem?: string;
};
