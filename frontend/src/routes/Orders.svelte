<script lang="ts">
  import { onMount } from 'svelte';
  import { get, post, errorText, ApiError, type Order, type Product } from '../lib/api';
  import { money, shortId, statusName, time } from '../lib/format';
  import Status from '../lib/components/Status.svelte';
  import TraceLink from '../lib/components/TraceLink.svelte';

  const filters = ['', 'ORDER_STATUS_CONFIRMED', 'ORDER_STATUS_REJECTED', 'ORDER_STATUS_CANCELLED', 'ORDER_STATUS_PENDING'];

  let products = $state<Product[]>([]);
  let orders = $state<Order[]>([]);
  let filter = $state('');
  let lines = $state<{ product_id: string; quantity: number }[]>([{ product_id: '', quantity: 1 }]);
  let last = $state<{ order?: Order; traceId?: string; error?: string } | null>(null);
  let busy = $state(false);

  async function loadOrders() {
    const q = filter ? `&status=${filter}` : '';
    try {
      orders = (await get<{ orders?: Order[] }>(`/api/orders?limit=100${q}`)).data.orders ?? [];
    } catch (e) {
      last = { error: errorText(e) };
    }
  }

  async function create(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    try {
      const r = await post<Order>('/api/orders', {
        items: lines.filter((l) => l.product_id).map((l) => ({ product_id: l.product_id, quantity: Number(l.quantity) })),
      });
      last = { order: r.data, traceId: r.traceId };
      await loadOrders();
    } catch (err) {
      last = { error: errorText(err), traceId: err instanceof ApiError ? err.traceId : undefined };
    } finally {
      busy = false;
    }
  }

  async function cancel(o: Order) {
    try {
      const r = await post<Order>(`/api/orders/${o.id}/cancel`, {});
      last = { order: r.data, traceId: r.traceId };
    } catch (err) {
      last = { error: errorText(err), traceId: err instanceof ApiError ? err.traceId : undefined };
    }
    await loadOrders();
  }

  onMount(async () => {
    try {
      products = (await get<{ products?: Product[] }>('/api/products?limit=500')).data.products ?? [];
    } catch (e) {
      last = { error: errorText(e) };
    }
    await loadOrders();
  });

  const cancellable = (o: Order) => o.status === 'ORDER_STATUS_CONFIRMED' || o.status === 'ORDER_STATUS_PENDING';
</script>

<h1>Pedidos</h1>
<p class="lead">
  Orders consulta precios y disponibilidad y reserva el stock en Inventory por gRPC. Los rechazos por falta de
  stock o por indisponibilidad de Inventory quedan registrados con su código.
</p>

<div class="grid cols-2">
  <form class="card form" onsubmit={create}>
    <h2>Nuevo pedido</h2>
    {#each lines as line, i}
      <div class="row line">
        <select required bind:value={line.product_id} aria-label="Producto">
          <option value="" disabled>Producto…</option>
          {#each products as p (p.id)}<option value={p.id}>{p.sku} — {p.name} ({money(p.price_cents)}, {p.available ?? 0} disp.)</option>{/each}
        </select>
        <input type="number" min="1" bind:value={line.quantity} aria-label="Cantidad" />
        {#if lines.length > 1}<button type="button" onclick={() => lines.splice(i, 1)} aria-label="Quitar línea">✕</button>{/if}
      </div>
    {/each}
    <div class="row">
      <button type="button" onclick={() => lines.push({ product_id: '', quantity: 1 })}>+ Línea</button>
      <span class="spacer"></span>
      <button class="primary" type="submit" disabled={busy}>Crear pedido</button>
    </div>
  </form>

  <section class="card">
    <h2>Resultado</h2>
    {#if last?.order}
      {@const o = last.order}
      <p class="row"><Status status={statusName(o.status)} /> <span class="mono">{o.id}</span></p>
      {#if o.rejection_code}
        <p class="alert error"><strong>{o.rejection_code}</strong> — {o.rejection_reason}</p>
      {/if}
      <p>Total: <strong>{money(o.total_cents)}</strong></p>
      <p class="small">Traza: <TraceLink id={last.traceId} /></p>
    {:else if last?.error}
      <p class="alert error">{last.error}</p>
      <p class="small">Traza: <TraceLink id={last.traceId} /></p>
    {:else}
      <p class="muted">Crea un pedido para ver el resultado y su traza.</p>
    {/if}
  </section>
</div>

<section class="card" style="margin-top: 1rem">
  <div class="row">
    <h2>Pedidos</h2>
    <span class="spacer"></span>
    <select bind:value={filter} onchange={loadOrders} aria-label="Filtrar por estado">
      {#each filters as f}<option value={f}>{f ? statusName(f) : 'Todos'}</option>{/each}
    </select>
    <button onclick={loadOrders}>Recargar</button>
  </div>
  <div class="table-wrap">
    <table>
      <thead><tr><th>Hora</th><th>Pedido</th><th>Estado</th><th>Productos</th><th class="num">Total</th><th>Motivo</th><th></th></tr></thead>
      <tbody>
        {#each orders as o (o.id)}
          <tr>
            <td>{time(o.created_at)}</td>
            <td class="mono">{shortId(o.id)}</td>
            <td><Status status={statusName(o.status)} /></td>
            <td class="small">{(o.items ?? []).map((i) => `${i.quantity}× ${i.sku || shortId(i.product_id)}`).join(', ')}</td>
            <td class="num">{money(o.total_cents)}</td>
            <td class="small">{o.rejection_code}</td>
            <td>{#if cancellable(o)}<button class="danger" onclick={() => cancel(o)}>Cancelar</button>{/if}</td>
          </tr>
        {:else}
          <tr><td colspan="7" class="muted">Sin pedidos</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</section>

<style>
  .form { display: grid; gap: 0.6rem; align-content: start; }
  .line select { flex: 1; min-width: 12rem; }
  .line input { width: 5.5rem; }
</style>
