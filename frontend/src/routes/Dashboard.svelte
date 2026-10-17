<script lang="ts">
  import { onMount } from 'svelte';
  import { get, errorText, type Overview } from '../lib/api';
  import { fixed, ms, shortId, time } from '../lib/format';
  import { tools } from '../lib/links';
  import Status from '../lib/components/Status.svelte';

  let data = $state<Overview | null>(null);
  let error = $state('');

  async function load() {
    try {
      data = (await get<Overview>('/api/orders/lab/overview')).data;
      error = '';
    } catch (e) {
      error = errorText(e);
    }
  }

  onMount(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  const metricsOf = (name: string) => data?.metrics?.find((m) => m.service === name);
</script>

<h1>Dashboard</h1>
<p class="lead">
  Estado de la plataforma según el endpoint de diagnóstico de Orders, que consulta Consul (instancias y
  health checks) y Prometheus (métricas) desde el servidor. Se actualiza cada 5 s.
</p>

{#if error}<p class="alert error">{error}</p>{/if}

{#if data}
  <div class="grid cols-2">
    {#each data.services ?? [] as svc (svc.name)}
      {@const m = metricsOf(svc.name)}
      <section class="card">
        <div class="row">
          <h2>{svc.name}</h2>
          <span class="spacer"></span>
          <span class="muted small">{svc.passing ?? 0}/{svc.instances?.length ?? 0} instancias sanas</span>
        </div>
        <div class="grid cols-4 stats">
          <div class="stat"><div class="value">{fixed(m?.rps)}</div><div class="label">peticiones/s</div></div>
          <div class="stat"><div class="value">{fixed(m?.errors_per_second)}</div><div class="label">errores 5xx/s</div></div>
          <div class="stat"><div class="value">{ms(m?.p95_ms)}</div><div class="label">latencia p95</div></div>
          <div class="stat"><div class="value">{Math.round(m?.requests_last_15m ?? 0)}</div><div class="label">peticiones (15 min)</div></div>
        </div>
        <ul class="instances">
          {#each svc.instances ?? [] as inst (inst.id)}
            <li>
              <div class="row">
                <Status status={inst.status} />
                <span class="mono">{inst.id}</span>
                <span class="spacer"></span>
                <span class="mono muted small">{inst.address}{#if inst.weight} · peso {inst.weight}{/if}</span>
              </div>
              <div class="checks">
                {#each inst.checks ?? [] as c}
                  <span title="{c.name}: {c.status}"><Status status={c.type || c.name} kind={c.status} /></span>
                {/each}
              </div>
            </li>
          {:else}
            <li class="muted">Sin instancias registradas</li>
          {/each}
        </ul>
      </section>
    {/each}
  </div>

  {#if data.metrics_error}<p class="alert error">Métricas no disponibles: {data.metrics_error}</p>{/if}

  <div class="grid cols-2" style="margin-top: 1rem">
    <section class="card">
      <h2>Circuit breaker (Orders → Inventory)</h2>
      <p><Status status={data.breaker_state || '—'} /></p>
      <p class="muted small">Detalle y experimentos en <a href="#/breaker">Circuit Breaker Lab</a>.</p>
      <h3>Herramientas</h3>
      <div class="row">
        <a href={tools.grafana} target="_blank" rel="noopener">Grafana ↗</a>
        <a href={tools.jaeger} target="_blank" rel="noopener">Jaeger ↗</a>
        <a href={tools.prometheus} target="_blank" rel="noopener">Prometheus ↗</a>
        <a href={tools.consul} target="_blank" rel="noopener">Consul ↗</a>
        <a href={tools.traefik} target="_blank" rel="noopener">Traefik ↗</a>
      </div>
    </section>
    <section class="card">
      <h2>Errores recientes (pedidos rechazados)</h2>
      <div class="table-wrap">
        <table>
          <thead><tr><th>Hora</th><th>Pedido</th><th>Código</th></tr></thead>
          <tbody>
            {#each data.recent_rejections ?? [] as r (r.order_id)}
              <tr title={r.reason}><td>{time(r.at)}</td><td class="mono">{shortId(r.order_id)}</td><td class="mono small">{r.rejection_code}</td></tr>
            {:else}
              <tr><td colspan="3" class="muted">Ningún pedido rechazado</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    </section>
  </div>
  <p class="muted small">Generado: {time(data.generated_at)}</p>
{:else if !error}
  <p class="muted">Cargando…</p>
{/if}

<style>
  .stats { margin: 0.25rem 0 0.75rem; grid-template-columns: repeat(auto-fit, minmax(110px, 1fr)); }
  .instances { list-style: none; padding: 0; margin: 0; display: grid; gap: 0.5rem; }
  .instances li { border-top: 1px solid var(--border); padding-top: 0.5rem; }
  .checks { display: flex; flex-wrap: wrap; gap: 0.25rem; margin-top: 0.3rem; }
</style>
