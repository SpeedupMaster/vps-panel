import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { VitePWA } from 'vite-plugin-pwa'
import { existsSync, createReadStream, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, extname, resolve, sep } from 'node:path'

const outDir = resolve(dirname(fileURLToPath(import.meta.url)), '../internal/panel/webui/dist')

// The Go embed needs the directory to exist even before the first build.
const keepFile = {
  name: 'keep-file',
  closeBundle() { writeFileSync(resolve(outDir, '.keep'), '') },
}

const gzMime = {
  '.js': 'application/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.svg': 'image/svg+xml; charset=utf-8',
}

// Serve pre-compressed .gz assets during `pnpm preview` when gzip-dist.mjs unlinked the raw >=1KB file.
const previewGzip = {
  name: 'preview-gzip',
  configurePreviewServer(server) {
    server.middlewares.use((req, res, next) => {
      if (!req.url || (req.method !== 'GET' && req.method !== 'HEAD')) return next()
      const cleanPath = decodeURIComponent(req.url.split('?')[0]).replace(/^\/+/, '')
      if (!cleanPath) return next()
      const rawFile = resolve(outDir, cleanPath)
      if (!rawFile.startsWith(outDir + sep) || existsSync(rawFile)) return next()
      const gzFile = rawFile + '.gz'
      if (!existsSync(gzFile)) return next()
      const mime = gzMime[extname(rawFile)]
      if (mime) res.setHeader('Content-Type', mime)
      res.setHeader('Content-Encoding', 'gzip')
      res.setHeader('Vary', 'Accept-Encoding')
      if (req.method === 'HEAD') { res.end(); return }
      createReadStream(gzFile).pipe(res)
    })
  },
}

// PWA: precache static hashed assets only (never index.html, because Go
// injects dynamic <base href="/<admin_path>/"> and returns 404 on old paths).
// API calls, SSE, WebSSH, subscriptions and install/download endpoints always
// bypass navigation caching.
const pwa = VitePWA({
  registerType: 'prompt',
  injectRegister: false,
  includeManifestIcons: false,
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
    globPatterns: ['**/*.{js,css,svg,png,woff2}'],
    maximumFileSizeToCacheInBytes: 3 * 1024 * 1024,
    navigateFallback: null,
    cleanupOutdatedCaches: true,
    clientsClaim: true,
    runtimeCaching: [
      {
        urlPattern: ({ request, url }) =>
          request.mode === 'navigate' &&
          !/^\/(api|s|dl)\/|^\/(install\.sh|healthz|status)(\/|$)/.test(url.pathname),
        handler: 'NetworkFirst',
        options: {
          cacheName: 'pages',
          networkTimeoutSeconds: 5,
          cacheableResponse: { statuses: [200] },
          expiration: { maxEntries: 16, maxAgeSeconds: 7 * 24 * 3600 },
        },
      },
    ],
  },
})

export default defineConfig({
  base: './',
  plugins: [vue(), pwa, keepFile, previewGzip],
  build: {
    outDir,
    emptyOutDir: true,
    chunkSizeWarningLimit: 800,
    rolldownOptions: {
      output: {
        codeSplitting: {
          includeDependenciesRecursively: false,
          groups: [
            { name: 'vendor', test: /[\\/]node_modules[\\/](?:(?:vue|vue-router)[\\/]|@vue[\\/])/ },
            { name: 'charts', test: /[\\/]node_modules[\\/](?:echarts|zrender|vue-echarts)[\\/]/ },
            { name: 'xterm', test: /[\\/]node_modules[\\/]@xterm[\\/]/ },
            { name: 'flow', test: /[\\/]node_modules[\\/]@vue-flow[\\/]/ },
          ],
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
