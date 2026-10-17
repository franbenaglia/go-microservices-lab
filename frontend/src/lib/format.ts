// La API serializa los int64 como cadenas (mapeo JSON de proto3): se convierten aquí.
export const num = (v: string | number | undefined | null): number => Number(v ?? 0);

export const money = (cents: string | number | undefined) =>
  (num(cents) / 100).toLocaleString('es-ES', { style: 'currency', currency: 'EUR' });

export const ms = (v: number | undefined) =>
  v === undefined || Number.isNaN(v) ? '—' : v < 10 ? `${v.toFixed(1)} ms` : `${Math.round(v)} ms`;

export const fixed = (v: number | undefined, digits = 2) =>
  v === undefined || Number.isNaN(v) ? '—' : v.toFixed(digits);

export const time = (iso: string | undefined) =>
  iso ? new Date(iso).toLocaleTimeString('es-ES') : '—';

// Los UUIDv7 empiezan por la marca de tiempo: los últimos caracteres distinguen mejor.
export const shortId = (id: string | undefined) => (id ? id.slice(-8) : '—');

export const statusName = (s: string) => s.replace('ORDER_STATUS_', '');
