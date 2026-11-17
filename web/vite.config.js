import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { writeFileSync } from 'node:fs'
import { resolve } from 'node:path'

const outDir = resolve(__dirname, '../internal/panel/webui/dist')

// The Go embed needs the directory to exist even before the first build.
const keepFile = {
  name: 'keep-file',
  closeBundle() { writeFileSync(resolve(outDir, '.keep'), '') },
}

export default defineConfig({
  base: './',
  plugins: [vue(), keepFile],
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
