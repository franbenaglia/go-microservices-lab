<script lang="ts">
  import { get, post, errorText, type Probe } from '../lib/api';
  import { ms } from '../lib/format';
  import BarList from '../lib/components/BarList.svelte';

  let count = $state(30);
  let interval = $state(0);
  let busy = $state(false);
  let error = $state('');
  let kratos = $state<Probe | null>(null);
  let traefik = $state<{ total: number; failed: number; avg: number; byInstance: Record<string, number> } | null>(null);

  // Balanceo interno: Orders llama N veces a Inventory.Diagnose por gRPC; el
  // cliente de Kratos elige la instancia (selector wrr/p2c/random).
  async function runKratos() {
    busy = true;
    error = '';
    try {
      kratos = (await post<Probe>('/api/orders/lab/lb', { count: Number(count), interval_ms: Number(interval) })).data;
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }

  // Balanceo externo: el navegador pide N veces /api/inventory/diagnose y
  // Traefik reparte entre las réplicas sanas que descubre en Consul.
  async function runTraefik() {
    busy = true;
    error = '';
    const byInstance: Record<string, number> = {};
    let failed = 0;
    let sum = 0;
    for (let i = 0; i < Number(count); i++) {
      const t0 = performance.now();
      try {
        const r = await get<{ instance_id: string }>('/api/inventory/diagnose');
        byInstance[r.data.instance_id] = (byInstance[r.data.instance_id] ?? 0) + 1;
      } catch {
        failed++;
      }
      sum += performance.now() - t0;
      if (interval > 0) await new Promise((r) => setTimeout(r, Number(interval)));
    }
    traefik = { total: Number(count), failed, avg: sum / Number(count), byInstance };
    busy = false;
  }

  const kratosItems = $derived((kratos?.by_instance ?? []).map((b) => ({ label: b.instance_id, value: b.count })));
  const traefikItems = $derived(
    Object.entries(traefik?.byInstance ?? {})
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([label, value]) => ({ label, value })),
  );
</script>

<h1>Load Balancing Lab</h1>
<p class="lead">
  Compara el <strong>balanceo interno</strong> (cliente gRPC de Kratos en Orders, con instancias de Consul) con el
  <strong>balanceo externo</strong> (Traefik, por HTTP). Escala Inventory con
  <code>docker compose up -d --scale inventory=3</code> para ver el reparto.
</p>

<form class="card row controls" onsubmit={(e) => { e.preventDefault(); runKratos(); }}>
  <label>Solicitudes <input type="number" min="1" max="1000" bind:value={count} /></label>
  <label>Pausa (ms) <input type="number" min="0" max="1000" bind:value={interval} /></label>
  <span class="spacer"></span>
  <button class="primary" type="submit" disabled={busy}>Interno (Kratos)</button>
  <button type="button" disabled={busy} onclick={runTraefik}>Externo (Traefik)</button>
</form>

{#if error}<p class="alert error">{error}</p>{/if}

<div class="grid cols-2" style="margin-top: 1rem">
  <section class="card">
    <h2>Interno: Orders → Inventory (gRPC)</h2>
    {#if kratos}
      <div class="grid cols-4">
        <div class="stat"><div class="value">{kratos.total}</div><div class="label">solicitudes</div></div>
        <div class="stat"><div class="value">{kratos.succeeded ?? 0}</div><div class="label">correctas</div></div>
        <div class="stat"><div class="value">{kratos.failed ?? 0}</div><div class="label">fallidas</div></div>
        <div class="stat"><div class="value">{ms(kratos.avg_latency_ms)}</div><div class="label">latencia media</div></div>
      </div>
      <p class="muted small">Selector: <strong>{kratos.balancer}</strong> · máx. {ms(kratos.max_latency_ms)}</p>
      <h3>Distribución por instancia</h3>
      <BarList items={kratosItems} />
      {#if kratos.errors?.length}
        <h3>Errores</h3>
        <ul>{#each kratos.errors as er}<li><code>{er.reason}</code>: {er.count}</li>{/each}</ul>
      {/if}
      <details>
        <summary class="small">Secuencia (instancia que atendió cada llamada)</summary>
        <p class="mono small seq">{(kratos.sequence ?? []).map((s) => (s ? s.slice(0, 6) : '✕')).join(' ')}</p>
      </details>
    {:else}
      <p class="muted">Pulsa «Interno (Kratos)».</p>
    {/if}
  </section>

  <section class="card">
    <h2>Externo: navegador → Traefik → Inventory (HTTP)</h2>
    {#if traefik}
      <div class="grid cols-4">
        <div class="stat"><div class="value">{traefik.total}</div><div class="label">solicitudes</div></div>
        <div class="stat"><div class="value">{traefik.total - traefik.failed}</div><div class="label">correctas</div></div>
        <div class="stat"><div class="value">{traefik.failed}</div><div class="label">fallidas</div></div>
        <div class="stat"><div class="value">{ms(traefik.avg)}</div><div class="label">latencia media</div></div>
      </div>
      <p class="muted small">Latencia medida desde el navegador (incluye el salto por Traefik).</p>
      <h3>Distribución por instancia</h3>
      <BarList items={traefikItems} />
    {:else}
      <p class="muted">Pulsa «Externo (Traefik)».</p>
    {/if}
  </section>
</div>

<section class="card" style="margin-top: 1rem">
  <h3>¿Qué diferencia hay?</h3>
  <ul class="small">
    <li><strong>Interno</strong>: la decisión la toma el cliente (Orders) en cada llamada, sobre conexiones HTTP/2 persistentes a cada réplica. Equivale a Spring Cloud LoadBalancer.</li>
    <li><strong>Externo</strong>: un proxy (Traefik) recibe la petición y la reenvía. Hay un salto más. Equivale a Spring Cloud Gateway.</li>
    <li>Ambos usan Consul como fuente de instancias sanas, pero cada uno con su propio algoritmo.</li>
  </ul>
</section>

<style>
  .controls { align-items: end; }
  .controls input { width: 7rem; }
  .seq { word-break: break-all; }
</style>
