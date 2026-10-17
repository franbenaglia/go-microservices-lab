<script lang="ts">
  // Barras horizontales de una sola serie (conteo por categoría) con etiqueta
  // directa de valor y porcentaje. Hover: tooltip nativo (title) en toda la fila.
  let { items, unit = '' }: { items: { label: string; value: number }[]; unit?: string } = $props();
  const total = $derived(items.reduce((a, b) => a + b.value, 0));
  const max = $derived(Math.max(1, ...items.map((i) => i.value)));
</script>

<div class="bars" role="list">
  {#each items as it (it.label)}
    {@const pct = total ? (it.value * 100) / total : 0}
    <div class="bar-row" role="listitem" title="{it.label}: {it.value}{unit} ({pct.toFixed(1)} %)">
      <span class="label mono">{it.label}</span>
      <span class="track"><span class="fill" style="width: {(it.value * 100) / max}%"></span></span>
      <span class="value">{it.value}{unit} <span class="muted">({pct.toFixed(0)} %)</span></span>
    </div>
  {/each}
</div>

<style>
  .bars { display: grid; gap: 0.4rem; }
  .bar-row { display: grid; grid-template-columns: minmax(7rem, 12rem) 1fr auto; gap: 0.6rem; align-items: center; }
  .bar-row:hover .label { color: var(--text); }
  .label { color: var(--text-2); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .track { height: 14px; background: var(--surface-2); border-radius: 4px; overflow: hidden; }
  .fill { display: block; height: 100%; background: var(--series-1); border-radius: 0 4px 4px 0; min-width: 2px; }
  .value { font-variant-numeric: tabular-nums; white-space: nowrap; }
  @media (max-width: 520px) { .bar-row { grid-template-columns: 1fr auto; } .track { grid-column: 1 / -1; } }
</style>
