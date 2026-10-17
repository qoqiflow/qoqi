import path from 'node:path';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vitest/config';

// The console is served by the daemon itself (qoqi serve), from what
// this build writes into internal/flowd/console. In development, `npm run
// dev` serves it here and sends /api to a daemon: QOQI_FLOW=http://127.0.0.1:7070
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': path.resolve(__dirname, 'src') },
  },
  build: {
    outDir: path.resolve(__dirname, '../internal/flowd/console'),
    emptyOutDir: true,
    sourcemap: false,
    chunkSizeWarningLimit: 900,
  },
  server: {
    proxy: {
      '/api': { target: process.env.QOQI_FLOW ?? 'http://127.0.0.1:7070', changeOrigin: false },
    },
  },
  test: {
    environment: 'happy-dom',
  },
});
