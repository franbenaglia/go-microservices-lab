<script lang="ts">
  import { onMount } from 'svelte';
  import { get, post, errorText, type Product } from '../lib/api';
  import { money } from '../lib/format';

  let products = $state<Product[]>([]);
  let error = $state('');
  let notice = $state('');

  let form = $state({ sku: '', name: '', description: '', price: '10.00', stock: 10 });
  let deltas = $state<Record<string, number>>({});
  let avail = $state({ product_id: '', quantity: 1 });
  let availResult = $state('');

  async function load() {
    try {
      products = (await get<{ products?: Product[] }>('/api/products?limit=500')).data.products ?? [];
    } catch (e) {
      error = errorText(e);
    }
  }

  async function create(e: SubmitEvent) {
    e.preventDefault();
    error = notice = '';
    try {
      const p = (await post<Product>('/api/products', {
        sku: form.sku,
        name: form.name,
        description: form.description,
        price_cents: Math.round(Number(form.price) * 100),
        initial_stock: Number(form.stock),
      })).data;
      notice = `Producto ${p.sku} creado`;
      form = { sku: '', name: '', description: '', price: '10.00', stock: 10 };
      await load();
    } catch (err) {
      error = errorText(err);
    }
  }

  async function adjust(p: Product, sign: 1 | -1) {
    error = notice = '';
    const delta = sign * Math.abs(Number(deltas[p.id] ?? 1));
    try {
      await post(`/api/inventory/${p.id}/adjust`, { delta });
      notice = `Stock de ${p.sku}: ${delta > 0 ? '+' : ''}${delta}`;
      await load();
    } catch (err) {
      error = errorText(err);
    }
  }

  async function check(e: SubmitEvent) {
    e.preventDefault();
    try {
      const r = (await post<{ all_available: boolean; items: { requested: number; available: number }[] }>(
        '/api/inventory/availability',
        { items: [{ product_id: avail.product_id, quantity: Number(avail.quantity) }] },
      )).data;
      const it = r.items[0];
      availResult = r.all_available
        ? `Disponible: se piden ${it.requested}, hay ${it.available}`
        : `No disponible: se piden ${it.requested}, hay ${it.available}`;
    } catch (err) {
      availResult = errorText(err);
    }
  }

  onMount(load);
</script>

<h1>Productos</h1>
<p class="lead">Catálogo y stock de Inventory (vía Traefik: <code>/api/products</code>, <code>/api/inventory</code>).</p>

{#if error}<p class="alert error">{error}</p>{/if}
{#if notice}<p class="alert ok">{notice}</p>{/if}

<div class="grid cols-2">
  <form class="card form" onsubmit={create}>
    <h2>Nuevo producto</h2>
    <div class="grid cols-2 fields">
      <label>SKU <input required bind:value={form.sku} placeholder="KB-001" /></label>
      <label>Nombre <input required bind:value={form.name} placeholder="Teclado" /></label>
      <label>Precio (€) <input required type="number" min="0" step="0.01" bind:value={form.price} /></label>
      <label>Stock inicial <input required type="number" min="0" bind:value={form.stock} /></label>
    </div>
    <label>Descripción <input bind:value={form.description} /></label>
    <div class="row"><button class="primary" type="submit">Crear</button></div>
  </form>

  <form class="card form" onsubmit={check}>
    <h2>Consultar disponibilidad</h2>
    <label>Producto
      <select required bind:value={avail.product_id}>
        <option value="" disabled>Elegir…</option>
        {#each products as p (p.id)}<option value={p.id}>{p.sku} — {p.name}</option>{/each}
      </select>
    </label>
    <label>Cantidad <input type="number" min="1" bind:value={avail.quantity} /></label>
    <div class="row"><button type="submit">Consultar</button>{#if availResult}<span class="muted">{availResult}</span>{/if}</div>
  </form>
</div>

<section class="card" style="margin-top: 1rem">
  <div class="row"><h2>Inventario</h2><span class="spacer"></span><button onclick={load}>Recargar</button></div>
  <div class="table-wrap">
    <table>
      <thead>
        <tr><th>SKU</th><th>Nombre</th><th class="num">Precio</th><th class="num">Disponible</th><th class="num">Reservado</th><th>Ajustar stock</th></tr>
      </thead>
      <tbody>
        {#each products as p (p.id)}
          <tr>
            <td class="mono">{p.sku}</td>
            <td>{p.name}</td>
            <td class="num">{money(p.price_cents)}</td>
            <td class="num">{p.available ?? 0}</td>
            <td class="num">{p.reserved ?? 0}</td>
            <td>
              <div class="row nowrap">
                <input class="qty" type="number" min="1" value={deltas[p.id] ?? 1}
                  oninput={(e) => (deltas[p.id] = Number((e.target as HTMLInputElement).value))} aria-label="Cantidad a ajustar" />
                <button onclick={() => adjust(p, 1)} title="Incrementar">+</button>
                <button onclick={() => adjust(p, -1)} title="Reducir">−</button>
              </div>
            </td>
          </tr>
        {:else}
          <tr><td colspan="6" class="muted">Sin productos</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</section>

<style>
  .form { display: grid; gap: 0.75rem; align-content: start; }
  .fields { gap: 0.6rem; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); }
  .qty { width: 5rem; }
  .nowrap { flex-wrap: nowrap; }
</style>
