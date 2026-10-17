<script lang="ts">
  import { onMount } from 'svelte';
  import { get, put, errorText, type ConfigView } from '../lib/api';
  import { time } from '../lib/format';

  type Breaker = { enabled: boolean; failure_ratio: number; min_requests: number; consecutive_failures: number; window: string; open_timeout: string; half_open_max_requests: number };
  type Retry = { enabled: boolean; max_attempts: number; base_backoff: string; max_backoff: string; attempt_timeout: string; idempotent_operations: string[]; key_required_operations: string[] };
  type OrdersDyn = { log: { level: string }; inventory: { timeout: string }; resilience: { breaker: Breaker; retry: Retry } };
  type InvDyn = { log: { level: string }; lab: { faults: Record<string, unknown> } };

  const levels = ['debug', 'info', 'warn', 'error'];

  let orders = $state<ConfigView<OrdersDyn> | null>(null);
  let inventory = $state<ConfigView<InvDyn> | null>(null);
  let o = $state<OrdersDyn | null>(null);
  let invLevel = $state('info');
  let message = $state<{ ok: boolean; text: string } | null>(null);

  async function load() {
    try {
      orders = (await get<ConfigView<OrdersDyn>>('/admin/orders/config')).data;
      inventory = (await get<ConfigView<InvDyn>>('/admin/inventory/config')).data;
      o = structuredClone($state.snapshot(orders.dynamic));
      invLevel = inventory.dynamic.log.level;
    } catch (e) {
      message = { ok: false, text: errorText(e) };
    }
  }

  // Normaliza los números (los inputs devuelven cadenas) antes de enviar.
  function ordersPayload(d: OrdersDyn): OrdersDyn {
    const b = d.resilience.breaker;
    const r = d.resilience.retry;
    return {
      ...d,
      resilience: {
        breaker: { ...b, failure_ratio: Number(b.failure_ratio), min_requests: Number(b.min_requests), consecutive_failures: Number(b.consecutive_failures), half_open_max_requests: Number(b.half_open_max_requests) },
        retry: { ...r, max_attempts: Number(r.max_attempts) },
      },
    };
  }

  async function save(service: 'orders' | 'inventory', body: unknown) {
    message = null;
    const before = (service === 'orders' ? orders : inventory)?.status.applied ?? 0;
    try {
      await put(`/admin/${service}/config`, body);
      // La aplicación es asíncrona (watcher de Consul): esperar a que se refleje.
      for (let i = 0; i < 10; i++) {
        await new Promise((r) => setTimeout(r, 300));
        const v = (await get<ConfigView<unknown>>(`/admin/${service}/config`)).data;
        if (v.status.applied > before) break;
      }
      await load();
      message = { ok: true, text: `Guardado en Consul KV y aplicado en ${service} sin reiniciar.` };
    } catch (e) {
      message = { ok: false, text: errorText(e) };
    }
  }

  onMount(load);
</script>

<h1>Configuración</h1>
<p class="lead">
  Parámetros dinámicos en Consul KV (<code>config/&lt;servicio&gt;/dynamic.yaml</code>). Se validan antes de
  guardarse y todas las instancias los aplican sin reiniciar. Los secretos (credenciales) nunca están aquí.
</p>

{#if message}<p class="alert" class:ok={message.ok} class:error={!message.ok}>{message.text}</p>{/if}

{#if orders && o}
  <div class="grid cols-2">
    <form class="card form" onsubmit={(e) => { e.preventDefault(); save('orders', ordersPayload(o!)); }}>
      <h2>orders-service</h2>
      <div class="grid cols-2 fields">
        <label>Nivel de log
          <select bind:value={o.log.level}>{#each levels as l}<option>{l}</option>{/each}</select>
        </label>
        <label>Timeout por llamada a Inventory <input bind:value={o.inventory.timeout} /></label>
      </div>
      <fieldset>
        <legend>Circuit breaker</legend>
        <label class="check"><input type="checkbox" bind:checked={o.resilience.breaker.enabled} /> Activado</label>
        <div class="grid cols-3 fields">
          <label>Fallos seguidos <input type="number" min="0" bind:value={o.resilience.breaker.consecutive_failures} /></label>
          <label>Proporción de fallos <input type="number" min="0" max="1" step="0.05" bind:value={o.resilience.breaker.failure_ratio} /></label>
          <label>Mín. peticiones <input type="number" min="1" bind:value={o.resilience.breaker.min_requests} /></label>
          <label>Ventana <input bind:value={o.resilience.breaker.window} /></label>
          <label>Tiempo en OPEN <input bind:value={o.resilience.breaker.open_timeout} /></label>
          <label>Pruebas HALF-OPEN <input type="number" min="1" bind:value={o.resilience.breaker.half_open_max_requests} /></label>
        </div>
      </fieldset>
      <fieldset>
        <legend>Reintentos</legend>
        <label class="check"><input type="checkbox" bind:checked={o.resilience.retry.enabled} /> Activados</label>
        <div class="grid cols-3 fields">
          <label>Intentos <input type="number" min="1" max="10" bind:value={o.resilience.retry.max_attempts} /></label>
          <label>Backoff base <input bind:value={o.resilience.retry.base_backoff} /></label>
          <label>Backoff máx. <input bind:value={o.resilience.retry.max_backoff} /></label>
          <label>Timeout por intento <input bind:value={o.resilience.retry.attempt_timeout} /></label>
        </div>
      </fieldset>
      <div class="row"><button class="primary" type="submit">Guardar</button><button type="button" onclick={load}>Descartar</button></div>
      {@render status(orders)}
    </form>

    <div class="grid" style="align-content: start">
      {#if inventory}
        <form class="card form" onsubmit={(e) => { e.preventDefault(); save('inventory', { log: { level: invLevel } }); }}>
          <h2>inventory-service</h2>
          <label>Nivel de log <select bind:value={invLevel}>{#each levels as l}<option>{l}</option>{/each}</select></label>
          <p class="muted small">Los fallos simulados (<code>lab.faults</code>) se editan en <a href="#/breaker">Circuit Breaker Lab</a>.</p>
          <div class="row"><button class="primary" type="submit">Guardar</button></div>
          {@render status(inventory)}
        </form>
      {/if}
      <section class="card">
        <h2>Requieren reinicio</h2>
        <p class="small mono">{orders.restart_required.join(' · ')}</p>
        <h3>Configuración estática de orders (sin secretos)</h3>
        <pre class="small">{JSON.stringify(orders.static, null, 2)}</pre>
      </section>
    </div>
  </div>
{:else if !message}
  <p class="muted">Cargando…</p>
{/if}

{#snippet status(v: ConfigView<unknown>)}
  <p class="muted small">
    <code>{v.status.key}</code> · instancia {v.instance} · aplicadas {v.status.applied} · rechazadas {v.status.rejected}
    {#if v.status.last_applied} · última {time(v.status.last_applied)}{/if}
  </p>
  {#if v.status.last_error}<p class="alert error small">Último rechazo: {v.status.last_error}</p>{/if}
  {#if v.consul_problem}<p class="alert error small">Consul: {v.consul_problem}</p>{/if}
{/snippet}

<style>
  .form { display: grid; gap: 0.7rem; align-content: start; }
  .fields { gap: 0.6rem; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); }
  fieldset { border: 1px solid var(--border); border-radius: 8px; padding: 0.6rem 0.8rem 0.8rem; display: grid; gap: 0.5rem; }
  legend { padding: 0 0.3rem; color: var(--text-2); font-size: 0.85em; }
  .check { display: flex; align-items: center; gap: 0.4rem; color: var(--text); }
  pre { background: var(--surface-2); padding: 0.6rem; border-radius: 8px; overflow-x: auto; margin: 0; }
</style>
