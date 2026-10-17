// Últimas operaciones hechas desde el frontend, con su ID de traza. Las usa la
// página Tracing para enlazar a Jaeger. Se conservan en memoria (y en
// sessionStorage, por si se recarga la página).

export type Operation = {
  at: string;
  method: string;
  path: string;
  status: number;
  durationMs: number;
  traceId?: string;
};

const KEY = 'lab.ops';
const MAX = 50;

function load(): Operation[] {
  try {
    return JSON.parse(sessionStorage.getItem(KEY) ?? '[]');
  } catch {
    return [];
  }
}

export const ops = $state<{ list: Operation[] }>({ list: load() });

export function record(op: Operation) {
  ops.list = [op, ...ops.list].slice(0, MAX);
  try {
    sessionStorage.setItem(KEY, JSON.stringify(ops.list));
  } catch {
    /* almacenamiento no disponible: solo memoria */
  }
}

export function clearOps() {
  ops.list = [];
  try {
    sessionStorage.removeItem(KEY);
  } catch {
    /* ignorar */
  }
}
