/// <reference types="vitest/config" />
import { fileURLToPath } from 'node:url';
import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { CONTENT_SECURITY_POLICY } from './csp.ts';

// Same origin as the API (TDR-019 §3): the dev and preview servers proxy API paths to the
// wallet service, so the browser only ever talks to one origin.
const apiTarget = process.env.WALLET_API_URL ?? 'http://localhost:8080';
const proxy = {
  '/v1': { target: apiTarget, changeOrigin: true },
  '/internal': { target: apiTarget, changeOrigin: true },
  '/healthz': { target: apiTarget, changeOrigin: true },
};

const cspMeta: Plugin = {
  name: 'csp-meta',
  apply: 'build',
  transformIndexHtml: {
    order: 'post',
    handler: () => [
      { tag: 'meta', attrs: { 'http-equiv': 'Content-Security-Policy', content: CONTENT_SECURITY_POLICY }, injectTo: 'head-prepend' },
    ],
  },
};

export default defineConfig({
  plugins: [react(), tailwindcss(), cspMeta],
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  server: { proxy },
  preview: { proxy },
  build: { sourcemap: false },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    restoreMocks: true,
  },
});
