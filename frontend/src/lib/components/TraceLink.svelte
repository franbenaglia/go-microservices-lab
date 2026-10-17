<script lang="ts">
  import { traceUrl } from '../links';
  let { id }: { id?: string } = $props();
</script>

{#if id}
  <a class="mono" href={traceUrl(id)} target="_blank" rel="noopener" title="Abrir la traza en Jaeger">{id.slice(0, 12)}… ↗</a>
{:else}
  <span class="muted">—</span>
{/if}
