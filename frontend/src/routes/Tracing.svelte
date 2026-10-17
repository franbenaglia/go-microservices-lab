<script lang="ts">
  import { get, errorText } from '../lib/api';
  import { ops, clearOps } from '../lib/ops.svelte';
  import { ms, time } from '../lib/format';
  import { tools, traceUrl } from '../lib/links';
  import TraceLink from '../lib/components/TraceLink.svelte';

  let traceId = $state('');
  let error = $state('');

  async function sample() {
    error = '';
    try {
      await get('/api/products?limit=5');
      await get('/api/orders?limit=5');
    } catch (e) {
      error = errorText(e);
    }
  }

  const open = (e: SubmitEvent) => {
    e.preventDefault();
    if (/^[0-9a-f]{32}$/i.test(traceId.trim())) window.open(traceUrl(traceId.trim()), '_blank', 'noopener');
    else error = 'Un ID de traza tiene 32 caracteres hexadecimales';
  };
</script>

<h1>Tracing</h1>
<p class="lead">
  Cada respuesta de la API incluye la cabecera <code>X-Trace-Id</code>. La traza empieza en Traefik y continúa en
  Orders, Inventory y PostgreSQL (W3C Trace Context). Aquí se listan las últimas operaciones de este navegador con
  enlace a Jaeger.
</p>

{#if error}<p class="alert error">{error}</p>{/if}

<div class="grid cols-2">
  <form class="card row" onsubmit={open}>
    <label style="flex: 1">ID de traza <input bind:value={traceId} placeholder="6319d9b977d356435d943357ca2d0d78" /></label>
    <button class="primary" type="submit" style="align-self: end">Abrir en Jaeger</button>
  </form>
  <div class="card row">
    <button onclick={sample}>Generar trazas de ejemplo</button>
    <a href="{tools.jaeger}/search?service=traefik" target="_blank" rel="noopener">Buscar en Jaeger ↗</a>
    <span class="spacer"></span>
    <button onclick={clearOps}>Vaciar lista</button>
  </div>
</div>

<section class="card" style="margin-top: 1rem">
  <h2>Últimas operaciones</h2>
  <div class="table-wrap">
    <table>
      <thead><tr><th>Hora</th><th>Método</th><th>Ruta</th><th class="num">Estado</th><th class="num">Duración</th><th>Traza</th></tr></thead>
      <tbody>
        {#each ops.list as o, i (o.at + i)}
          <tr>
            <td>{time(o.at)}</td>
            <td class="mono">{o.method}</td>
            <td class="mono small">{o.path}</td>
            <td class="num">{o.status}</td>
            <td class="num">{ms(o.durationMs)}</td>
            <td><TraceLink id={o.traceId} /></td>
          </tr>
        {:else}
          <tr><td colspan="6" class="muted">Todavía no hay operaciones: usa las otras secciones o genera trazas de ejemplo.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</section>
