import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { VitePWA } from 'vite-plugin-pwa'
import { writeFileSync } from 'node:fs'
import { resolve } from 'node:path'

const outDir = resolve(__dirname, '../internal/panel/webui/dist')

// The Go embed needs the directory to exist even before the first build.
const keepFile = {
  name: 'keep-file',
  closeBundle() { writeFileSync(resolve(outDir, '.keep'), '') },
}

// PWA: precache the app shell only. API calls, SSE, the WebSSH socket,
// subscriptions and install/download endpoints always go to the network.
const pwa = VitePWA({
  registerType: 'prompt',
  injectRegister: false,
  includeAssets: ['favicon.svg', 'apple-touch-icon.png'],
  manifest: {
    name: 'VPS Panel',
    short_name: 'VPS Panel',
    description: 'VPS 节点、线路与回程监控面板',
    lang: 'zh-CN',
    start_url: './',
    scope: './',
    display: 'standalone',
    orientation: 'any',
    background_color: '#f7f8fa',
    theme_color: '#0f7b45',
    icons: [
      { src: 'pwa-192.png', sizes: '192x192', type: 'image/png' },
      { src: 'pwa-512.png', sizes: '512x512', type: 'image/png' },
      { src: 'pwa-maskable-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
      { src: 'favicon.svg', sizes: 'any', type: 'image/svg+xml' },
    ],
    shortcuts: [
      { name: '回程延迟', url: './latency' },
      { name: '告警', url: './alerts' },
    ],
  },
  workbox: {
    globPatterns: ['**/*.{js,css,html,svg,png,woff2}'],
    maximumFileSizeToCacheInBytes: 3 * 1024 * 1024,
    navigateFallback: 'index.html',
    navigateFallbackAllowlist: [
      /\/$/,
      /\/(login|offline|servers(\/.*)?|routes(\/.*)?|topology|latency|subscription|updates|alerts|tools|settings)$/,
    ],
    navigateFallbackDenylist: [/^\/api\//, /^\/s\//, /^\/dl\//, /^\/install\.sh/, /^\/healthz/, /^\/status/],
    cleanupOutdatedCaches: true,
    clientsClaim: true,
    runtimeCaching: [],
  },
})

export default defineConfig({
  base: './',
  plugins: [vue(), pwa, keepFile],
  build: {
    outDir,
    emptyOutDir: true,
    chunkSizeWarningLimit: 1500,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('echarts') || id.includes('zrender')) return 'charts'
          if (id.includes('naive-ui') || id.includes('vueuc') || id.includes('css-render')) return 'ui'
          if (id.includes('@xterm')) return 'xterm'
          if (id.includes('@vue-flow')) return 'flow'
        },
      },
    },
  },
  server: {
    proxy: {
      '/api': { target: 'http://127.0.0.1:8080', ws: true },
      '/s/': 'http://127.0.0.1:8080',
    },
  },
})
