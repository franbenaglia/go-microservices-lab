<script lang="ts">
  import { onMount } from 'svelte';
  import { get, post, put, errorText, type ConfigView, type Probe, type Resilience } from '../lib/api';
  import { num, time } from '../lib/format';
  import Status from '../lib/components/Status.svelte';

  type Faults = { latency_ms: number; jitter_ms: number; error_rate: number; error_code: number; unavailable: boolean; fail_readiness: boolean; operations: string[] | null };
  type InvDyn = { log: { level: string }; lab: { faults: Faults } };
  type Tick = { at: number; ok: number; failed: number; rejected: number; state: string };

  const none: Faults = { latency_ms: 0, jitter_ms: 0, error_rate: 0, error_code: 503, unavailable: false, fail_readiness: false, operations: null };

  let faults = $state<Faults>({ ...none });
  let res = $state<Resilience | null>(null);
  let error = $state('');
  let notice = $state('');
  let running = $state(false);
  let ticks = $state<Tick[]>([]);
  const MAX_TICKS = 60;

  async function loadFaults() {
    try {
      const v = (await get<ConfigView<InvDyn>>('/admin/inventory/config')).data;
      faults = { ...none, ...v.dynamic.lab.faults, error_code: v.dynamic.lab.faults.error_code || 503 };
    } catch (e) {
      error = errorText(e);
    }
  }

  async function loadResilience() {
    try {
      res = (await get<Resilience>('/api/orders/lab/resilience')).data;
    } catch (e) {
      error = errorText(e);
    }
  }

  // Los fallos se guardan en Consul KV y los aplican TODAS las réplicas de Inventory.
  async function applyFaults(f: Faults) {
    error = notice = '';
    try {
      await put('/admin/inventory/config', { lab: { faults: { ...f, error_rate: Number(f.error_rate), latency_ms: Number(f.latency_ms), jitter_ms: Number(f.jitter_ms), error_code: Number(f.error_code) } } });
      notice = 'Guardado en Consul KV: las réplicas de Inventory lo aplican al recibir el aviso.';
      setTimeout(loadFaults, 800);
    } catch (e) {
      error = errorText(e);
    }
  }

  const presets: { label: string; f: Partial<Faults> }[] = [
    { label: 'Errores 30 %', f: { error_rate: 0.3 } },
    { label: 'Latencia 3 s', f: { latency_ms: 3000 } },
    { label: 'Indisponible', f: { unavailable: true } },
  ];

  // Generador de tráfico: cada ~1 s, 10 llamadas Orders → Inventory y lectura del breaker.
  async function loop() {
    while (running) {
      const started = Date.now();
      let tick: Tick = { at: started, ok: 0, failed: 0, rejected: 0, state: '' };
      try {
        // quiet: el tráfico generado no se añade a la lista de Tracing.
        const p = (await post<Probe>('/api/orders/lab/lb', { count: 10, interval_ms: 30 }, true)).data;
        const rejected = (p.errors ?? []).filter((e) => e.reason === 'CIRCUITBREAKER').reduce((a, e) => a + e.count, 0);
        tick = { ...tick, ok: p.succeeded ?? 0, failed: (p.failed ?? 0) - rejected, rejected };
      } catch (e) {
        error = errorText(e);
      }
      await loadResilience();
      tick.state = res?.breaker.state || (res?.breaker.enabled ? 'no observable' : 'desactivado');
      ticks = [...ticks, tick].slice(-MAX_TICKS);
      await new Promise((r) => setTimeout(r, Math.max(0, 1000 - (Date.now() - started))));
    }
  }

  function toggle() {
    running = !running;
    if (running) loop();
  }

  onMount(() => {
    loadFaults();
    loadResilience();
    const t = setInterval(() => !running && loadResilience(), 2000);
    return () => {
      running = false;
      clearInterval(t);
    };
  });

  // --- gráfico (SVG): barras apiladas por segundo + banda de estado ---
  const W = 720;
  const H = 176;
  const BAND = 30; // banda de estado (12px) + etiqueta debajo
  const barW = $derived(W / MAX_TICKS);
  const yMax = 10;
  const yOf = (v: number) => ((H - BAND - 8) * v) / yMax;
  // Tramos consecutivos con el mismo estado (para etiquetarlos una vez).
  const segments = $derived.by(() => {
    const out: { state: string; start: number; len: number }[] = [];
    ticks.forEach((t, i) => {
      const last = out[out.length - 1];
      if (last && last.state === t.state) last.len++;
      else out.push({ state: t.state, start: i, len: 1 });
    });
    return out;
  });
  const stateColor = (s: string) => (s === 'open' ? 'var(--critical)' : s === 'half-open' ? 'var(--warning)' : s === 'closed' ? 'var(--good)' : 'var(--border)');
</script>

<h1>Circuit Breaker Lab</h1>
<p class="lead">
  Simula fallos en Inventory (todas las réplicas, vía Consul KV) y genera tráfico Orders → Inventory para ver
  cómo reaccionan los reintentos y el circuit breaker: CLOSED → OPEN → HALF-OPEN → CLOSED.
</p>

{#if error}<p class="alert error">{error}</p>{/if}
{#if notice}<p class="alert ok">{notice}</p>{/if}

<div class="grid cols-2">
  <form class="card form" onsubmit={(e) => { e.preventDefault(); applyFaults(faults); }}>
    <h2>Fallos simulados en Inventory</h2>
    <div class="row">
      {#each presets as p}<button type="button" onclick={() => applyFaults({ ...none, ...p.f })}>{p.label}</button>{/each}
      <button type="button" class="primary" onclick={() => applyFaults({ ...none })}>Recuperar</button>
    </div>
    <div class="grid cols-2 fields">
      <label>Latencia (ms) <input type="number" min="0" max="30000" bind:value={faults.latency_ms} /></label>
      <label>Jitter (ms) <input type="number" min="0" max="30000" bind:value={faults.jitter_ms} /></label>
      <label>Tasa de errores (0–1) <input type="number" min="0" max="1" step="0.05" bind:value={faults.error_rate} /></label>
      <label>Código de error
        <select bind:value={faults.error_code}><option value={503}>503 (transitorio)</option><option value={500}>500 (interno)</option></select>
      </label>
    </div>
    <label class="check"><input type="checkbox" bind:checked={faults.unavailable} /> Indisponibilidad (todas las peticiones 503)</label>
    <label class="check"><input type="checkbox" bind:checked={faults.fail_readiness} /> Readiness fallida (Consul retira las instancias)</label>
    <div class="row"><button class="primary" type="submit">Aplicar</button></div>
  </form>

  <section class="card">
    <div class="row">
      <h2>Circuit breaker</h2>
      <span class="spacer"></span>
      {#if res}<Status status={res.breaker.state || (res.breaker.enabled ? 'no observable' : 'desactivado')} />{/if}
    </div>
    {#if res}
      <div class="grid cols-3">
        <div class="stat"><div class="value">{num(res.breaker.allowed)}</div><div class="label">permitidas</div></div>
        <div class="stat"><div class="value">{num(res.breaker.failures)}</div><div class="label">intentos fallidos</div></div>
        <div class="stat"><div class="value">{num(res.breaker.rejected)}</div><div class="label">rechazadas localmente</div></div>
        <div class="stat"><div class="value">{num(res.retry.retries)}</div><div class="label">reintentos</div></div>
        <div class="stat"><div class="value">{num(res.retry.recovered)}</div><div class="label">recuperadas por retry</div></div>
        <div class="stat"><div class="value">{res.call_timeout}</div><div class="label">timeout por llamada</div></div>
      </div>
      <p class="muted small">
        {res.breaker.kind}: abre con {res.breaker.consecutive_failures_threshold} fallos seguidos o ≥{Math.round(res.breaker.failure_ratio * 100)} %
        (mín. {res.breaker.min_requests}) en {res.breaker.window}; {res.breaker.open_timeout} en OPEN; {res.breaker.half_open_max_requests} pruebas en HALF-OPEN.
        Retry: {res.retry.enabled ? `${res.retry.max_attempts} intentos, ${res.retry.attempt_timeout} cada uno` : 'desactivado'}.
        <a href="#/config">Cambiar</a>
      </p>
      <h3>Transiciones</h3>
      <ul class="transitions small">
        {#each [...(res.breaker.transitions ?? [])].reverse().slice(0, 8) as t}
          <li>{time(t.at)} <Status status={t.from} /> → <Status status={t.to} /></li>
        {:else}
          <li class="muted">Sin transiciones</li>
        {/each}
      </ul>
    {/if}
  </section>
</div>

<section class="card" style="margin-top: 1rem">
  <div class="row">
    <h2>Tráfico (10 llamadas por segundo)</h2>
    <span class="spacer"></span>
    <button class:primary={!running} onclick={toggle}>{running ? 'Detener' : 'Generar tráfico'}</button>
    <button onclick={() => (ticks = [])} disabled={running}>Limpiar</button>
  </div>
  <div class="legend small">
    <span><i style="background: var(--good)"></i>correctas</span>
    <span><i style="background: var(--critical)"></i>fallidas (llegaron a Inventory)</span>
    <span><i style="background: var(--warning)"></i>rechazadas por el breaker</span>
    <span class="muted">· banda inferior: estado del breaker</span>
  </div>
  <svg viewBox="0 0 {W} {H}" role="img" aria-label="Resultado de las llamadas por segundo y estado del circuit breaker">
    {#each ticks as t, i}
      {@const x = i * barW + 1}
      {@const w = Math.max(1, barW - 2)}
      {@const base = H - BAND - 4}
      <g>
        <title>{new Date(t.at).toLocaleTimeString('es-ES')}: {t.ok} correctas, {t.failed} fallidas, {t.rejected} rechazadas · {t.state}</title>
        <rect x={x} y={base - yOf(t.ok)} width={w} height={yOf(t.ok)} fill="var(--good)" rx="2" />
        <rect x={x} y={base - yOf(t.ok) - yOf(t.failed)} width={w} height={Math.max(0, yOf(t.failed) - 2)} fill="var(--critical)" rx="2" />
        <rect x={x} y={base - yOf(t.ok) - yOf(t.failed) - yOf(t.rejected)} width={w} height={Math.max(0, yOf(t.rejected) - 2)} fill="var(--warning)" rx="2" />
        <rect x={i * barW} y={H - BAND} width={barW} height={12} fill={stateColor(t.state)} />
      </g>
    {/each}
    {#each segments as seg}
      {#if seg.len * barW >= 44}
        <text x={seg.start * barW + 2} y={H - 4} class="state-label">{seg.state.toUpperCase()}</text>
      {/if}
    {/each}
    <line x1="0" x2={W} y1={H - BAND - 4} y2={H - BAND - 4} stroke="var(--border)" />
  </svg>
  <details>
    <summary class="small">Ver como tabla</summary>
    <div class="table-wrap">
      <table class="small">
        <thead><tr><th>Hora</th><th class="num">Correctas</th><th class="num">Fallidas</th><th class="num">Rechazadas</th><th>Breaker</th></tr></thead>
        <tbody>
          {#each [...ticks].reverse() as t}
            <tr><td>{new Date(t.at).toLocaleTimeString('es-ES')}</td><td class="num">{t.ok}</td><td class="num">{t.failed}</td><td class="num">{t.rejected}</td><td>{t.state}</td></tr>
          {/each}
        </tbody>
      </table>
    </div>
  </details>
</section>

<style>
  .form { display: grid; gap: 0.6rem; align-content: start; }
  .fields { gap: 0.6rem; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); }
  .check { display: flex; align-items: center; gap: 0.4rem; color: var(--text); }
  .transitions { list-style: none; padding: 0; margin: 0; display: grid; gap: 0.25rem; }
  .legend { display: flex; flex-wrap: wrap; gap: 0.9rem; margin: 0.25rem 0 0.5rem; color: var(--text-2); }
  .legend i { display: inline-block; width: 10px; height: 10px; border-radius: 2px; margin-right: 0.3rem; vertical-align: -1px; }
  svg { width: 100%; height: auto; display: block; }
  .state-label { font-size: 10px; font-weight: 600; fill: var(--text-2); }
</style>
