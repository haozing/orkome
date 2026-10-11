import { tanstackRouter } from '@tanstack/router-plugin/vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitest/config'
import type { ProxyOptions } from 'vite'
const apiTarget = process.env.API_TARGET ?? 'http://localhost:8080'
const stripOrigin: NonNullable<ProxyOptions['configure']> = (proxy) => {
  proxy.on('proxyReq', (proxyReq) => {
    proxyReq.removeHeader('origin')
  })
}
// ploykit v0.4.0 frontend packages, aliased to the Go module cache sources
// (packages publish ESM relative-import output; the `development` export
// condition points at src, which is what an alias must consume).
const ploykitSrc = 'C:/Users/bzlih/go/pkg/mod/github.com/haozing/ploykit@v0.4.0/packages'
export default defineConfig({
  plugins: [
    tanstackRouter({
      target: 'react',
      autoCodeSplitting: true,
    }),
    react(),
    tailwindcss(),
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
      // deep import for the test-only CSRF reset (not re-exported by the
      // barrel); MUST precede '@ploykit/client' — alias entries match by
      // prefix, first match wins.
      '@ploykit/client/api': `${ploykitSrc}/client/src/api.ts`,
      '@ploykit/client': `${ploykitSrc}/client/src/index.ts`,
      // The ws-query-bridge leaf has type-only external imports (erased at
      // build), so it is safe to consume straight from the module cache.
      // The '@ploykit/hooks' barrel and '@ploykit/hooks/ws' are NOT aliased:
      // they value-import 'react'/'react-router' (via '@ploykit/runtime'),
      // unresolvable from the module-cache directory.
      '@ploykit/hooks/ws-query-bridge': `${ploykitSrc}/hooks/src/lib/ws-query-bridge.ts`,
      '@ploykit/ui': `${ploykitSrc}/ui/src/index.ts`,
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: apiTarget, changeOrigin: true, configure: stripOrigin },
      '/auth': { target: apiTarget, changeOrigin: true, configure: stripOrigin },
      '/config': { target: apiTarget, changeOrigin: true, configure: stripOrigin },
      '/uploads': { target: apiTarget, changeOrigin: true },
      '/avatars': { target: apiTarget, changeOrigin: true },
      '/ws': { target: apiTarget, ws: true },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./test/setup.ts'],
    include: ['test/**/*.{test,spec}.{ts,tsx}'],
  },
})
