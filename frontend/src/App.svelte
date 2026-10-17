<script lang="ts">
  import { router, routes } from './lib/router.svelte';
  import { tools } from './lib/links';
  import Dashboard from './routes/Dashboard.svelte';
  import Products from './routes/Products.svelte';
  import Orders from './routes/Orders.svelte';
  import LoadBalancing from './routes/LoadBalancing.svelte';
  import CircuitBreaker from './routes/CircuitBreaker.svelte';
  import Tracing from './routes/Tracing.svelte';
  import Configuration from './routes/Configuration.svelte';

  let menuOpen = $state(false);
</script>

<div class="layout">
  <header class="topbar">
    <button class="menu" aria-label="Menú" aria-expanded={menuOpen} onclick={() => (menuOpen = !menuOpen)}>☰</button>
    <a class="brand" href="#/dashboard"><span class="logo" aria-hidden="true">◆</span> Go Microservices Lab</a>
  </header>

  <nav class:open={menuOpen} aria-label="Secciones">
    {#each routes as r (r.path)}
      <a href="#/{r.path}" class:active={router.path === r.path} onclick={() => (menuOpen = false)}>{r.label}</a>
    {/each}
    <div class="tools">
      <span class="muted small">Herramientas</span>
      <a href={tools.grafana} target="_blank" rel="noopener">Grafana ↗</a>
      <a href={tools.jaeger} target="_blank" rel="noopener">Jaeger ↗</a>
      <a href={tools.prometheus} target="_blank" rel="noopener">Prometheus ↗</a>
      <a href={tools.consul} target="_blank" rel="noopener">Consul ↗</a>
      <a href={tools.traefik} target="_blank" rel="noopener">Traefik ↗</a>
    </div>
  </nav>

  <main>
    {#if router.path === 'dashboard'}<Dashboard />
    {:else if router.path === 'products'}<Products />
    {:else if router.path === 'orders'}<Orders />
    {:else if router.path === 'lb'}<LoadBalancing />
    {:else if router.path === 'breaker'}<CircuitBreaker />
    {:else if router.path === 'tracing'}<Tracing />
    {:else if router.path === 'config'}<Configuration />
    {/if}
  </main>
</div>

<style>
  .layout {
    display: grid;
    grid-template-columns: 230px 1fr;
    grid-template-rows: auto 1fr;
    min-height: 100vh;
  }
  .topbar {
    grid-column: 1 / -1;
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.6rem 1rem;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
  }
  .brand { font-weight: 700; color: var(--text); text-decoration: none; }
  .logo { color: var(--accent); }
  .menu { display: none; }
  nav {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    padding: 1rem 0.6rem;
    border-right: 1px solid var(--border);
    background: var(--surface);
  }
  nav a {
    color: var(--text-2);
    text-decoration: none;
    padding: 0.45rem 0.7rem;
    border-radius: 8px;
  }
  nav a:hover { background: var(--surface-2); color: var(--text); }
  nav a.active { background: var(--surface-2); color: var(--text); font-weight: 600; box-shadow: inset 3px 0 0 var(--accent); }
  .tools { display: flex; flex-direction: column; gap: 0.1rem; margin-top: 1.25rem; padding-top: 0.75rem; border-top: 1px solid var(--border); }
  .tools > span { padding: 0 0.7rem 0.25rem; }
  main { padding: 1.25rem 1.5rem 3rem; min-width: 0; }

  @media (max-width: 760px) {
    .layout { grid-template-columns: 1fr; }
    .menu { display: inline-block; }
    nav { display: none; border-right: 0; border-bottom: 1px solid var(--border); }
    nav.open { display: flex; }
    main { padding: 1rem 16px 3rem; }
  }
</style>
