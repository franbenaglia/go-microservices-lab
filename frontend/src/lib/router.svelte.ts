// Router mínimo por hash (#/products). Sin dependencias.
export const routes = [
  { path: 'dashboard', label: 'Dashboard' },
  { path: 'products', label: 'Productos' },
  { path: 'orders', label: 'Pedidos' },
  { path: 'lb', label: 'Load Balancing Lab' },
  { path: 'breaker', label: 'Circuit Breaker Lab' },
  { path: 'tracing', label: 'Tracing' },
  { path: 'config', label: 'Configuración' },
] as const;

export type RoutePath = (typeof routes)[number]['path'];

function current(): RoutePath {
  const h = window.location.hash.replace(/^#\/?/, '');
  return (routes.find((r) => r.path === h)?.path ?? 'dashboard') as RoutePath;
}

export const router = $state<{ path: RoutePath }>({ path: current() });

window.addEventListener('hashchange', () => (router.path = current()));
