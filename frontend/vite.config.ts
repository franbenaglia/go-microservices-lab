import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// En desarrollo (npm run dev) las llamadas a /api y /admin se envían al
// gateway (Traefik), igual que en producción, donde el frontend y la API
// comparten origen.
export default defineConfig({
  plugins: [svelte()],
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8080',
      '/admin': 'http://127.0.0.1:8080',
    },
  },
});
