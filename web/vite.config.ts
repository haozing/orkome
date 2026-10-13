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
// ploykit v0.6.0 frontend packages, installed via pnpm (dist ESM output).
// Aliases pin subpath imports to concrete files: package `exports` only
// exposes ".", so deep imports (client/api, hooks/ws-query-bridge) need
// explicit mapping.
const pkPkg = (name: string) =>
  fileURLToPath(new URL(`./node_modules/@ploykit/${name}`, import.meta.url))
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
      '@ploykit/client/api': `${pkPkg('client')}/dist/api.js`,
      '@ploykit/client': `${pkPkg('client')}/dist/index.js`,
      '@ploykit/hooks/ws-query-bridge': `${pkPkg('hooks')}/dist/lib/ws-query-bridge.js`,
      '@ploykit/hooks': `${pkPkg('hooks')}/dist/index.js`,
      '@ploykit/ui': `${pkPkg('ui')}/dist/index.js`,
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
    // ploykit dist 产物内部用无扩展名相对导入（'./api'）。vite dev/build 的
    // resolve 能补全，vitest 的 node 解析器（pnpm 符号链接布局）不能——内联
    // 交给 vite 管线转换。
    server: { deps: { inline: [/@ploykit\//] } },
  },
})
