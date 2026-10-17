<script lang="ts">
  // Estado con icono + texto (nunca solo color). `kind` permite indicar el
  // estado por separado del texto (p. ej. texto "ttl", estado "passing").
  let { status, kind: k }: { status: string; kind?: string } = $props();
  const s = $derived((k ?? status).toLowerCase());
  const kind = $derived(
    ['passing', 'closed', 'confirmed', 'ok'].includes(s)
      ? 'good'
      : ['warning', 'half-open', 'pending'].includes(s)
        ? 'warning'
        : ['critical', 'open', 'rejected', 'error'].includes(s)
          ? 'critical'
          : 'neutral',
  );
  const icon = $derived({ good: '●', warning: '◐', critical: '■', neutral: '○' }[kind]);
</script>

<span class="badge {kind}"><span aria-hidden="true">{icon}</span>{status}</span>

<style>
  .badge {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    padding: 0.05rem 0.5rem;
    border-radius: 999px;
    font-size: 0.8em;
    font-weight: 600;
    border: 1px solid var(--border);
    background: var(--surface-2);
    color: var(--text-2);
    white-space: nowrap;
  }
  .good { color: var(--good-ink); border-color: var(--good); }
  .warning { color: var(--warning-ink); border-color: var(--warning); }
  .critical { color: var(--critical-ink); border-color: var(--critical); }
  span[aria-hidden] { font-size: 0.85em; }
</style>
