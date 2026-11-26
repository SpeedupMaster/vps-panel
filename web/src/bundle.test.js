import { before, after, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { gunzipSync } from 'node:zlib'

const webRoot = resolve(import.meta.dirname, '..')
const defaultAssetsDir = resolve(webRoot, '../internal/panel/webui/dist/assets')

function readChunksFrom(dir) {
  const chunks = new Map()
  if (!existsSync(dir)) return chunks
  for (const entry of readdirSync(dir)) {
    if (!entry.endsWith('.js') && !entry.endsWith('.js.gz')) continue
    const name = entry.replace(/\.gz$/, '')
    const raw = readFileSync(join(dir, entry))
    const code = entry.endsWith('.gz') ? gunzipSync(raw).toString('utf8') : raw.toString('utf8')
    chunks.set(name, code)
  }
  return chunks
}

describe('production bundle chunks', () => {
  let chunks = new Map()
  let tempOutDir = ''

  before(async () => {
    chunks = readChunksFrom(defaultAssetsDir)
    if (chunks.size === 0) {
      tempOutDir = join(tmpdir(), `vps-vite-build-${process.pid}-${Date.now()}`)
      const { build } = await import('vite')
      await build({
        root: webRoot,
        configFile: resolve(webRoot, 'vite.config.js'),
        logLevel: 'silent',
        build: { outDir: tempOutDir, emptyOutDir: true },
      })
      chunks = readChunksFrom(join(tempOutDir, 'assets'))
    }
  })

  after(() => {
    if (tempOutDir) rmSync(tempOutDir, { recursive: true, force: true })
  })

  it('contains no circular static imports across built JS chunks', () => {
    assert.ok(chunks.size > 0, 'expected built JS chunks')

    const graph = new Map()
    for (const [name, code] of chunks) {
      const deps = [...code.matchAll(/from\s*["']\.\/([^"']+)["']/g)].map((m) => m[1])
      graph.set(name, deps)
    }

    const visited = new Set()
    const inStack = []
    const cycles = []
    function dfs(node) {
      visited.add(node)
      inStack.push(node)
      for (const dep of graph.get(node) || []) {
        const idx = inStack.indexOf(dep)
        if (idx !== -1) {
          cycles.push([...inStack.slice(idx), dep].join(' -> '))
        } else if (!visited.has(dep)) {
          dfs(dep)
        }
      }
      inStack.pop()
    }
    for (const name of [...graph.keys()].sort()) {
      if (!visited.has(name)) dfs(name)
    }

    assert.deepEqual(cycles, [])
  })

  it('evaluates Chart and charts chunks cleanly without TDZ or helper initialization errors', async () => {
    const chartChunk = [...chunks.keys()].find((k) => /^Chart-.*\.js$/.test(k))
    assert.ok(chartChunk, 'expected Chart-*.js chunk in built assets')

    const dir = join(tmpdir(), `vps-bundle-test-${process.pid}-${Date.now()}`)
    mkdirSync(dir, { recursive: true })
    writeFileSync(join(dir, 'package.json'), JSON.stringify({ type: 'module' }))

    const prevWindow = globalThis.window
    const prevDocument = globalThis.document
    const prevNavigator = Object.getOwnPropertyDescriptor(globalThis, 'navigator')
    try {
      for (const [name, code] of chunks) {
        if (name.startsWith('sw') || name.startsWith('workbox')) continue
        writeFileSync(join(dir, name), code)
      }
      const el = () => ({ style: {}, setAttribute: () => {}, getAttribute: () => null, appendChild: () => {}, getContext: () => ({}) })
      globalThis.window = globalThis
      globalThis.addEventListener ??= () => {}
      globalThis.removeEventListener ??= () => {}
      globalThis.document = {
        documentElement: el(), head: el(), body: el(),
        createElement: el, createElementNS: el,
        addEventListener: () => {}, removeEventListener: () => {},
      }
      Object.defineProperty(globalThis, 'navigator', {
        value: { userAgent: 'Mozilla/5.0', platform: 'Linux x86_64', onLine: true },
        configurable: true,
      })

      const mod = await import(pathToFileURL(join(dir, chartChunk)).href)
      assert.ok(mod && Object.keys(mod).length > 0)
    } finally {
      if (prevWindow === undefined) delete globalThis.window
      else globalThis.window = prevWindow
      if (prevDocument === undefined) delete globalThis.document
      else globalThis.document = prevDocument
      if (prevNavigator) Object.defineProperty(globalThis, 'navigator', prevNavigator)
      rmSync(dir, { recursive: true, force: true })
    }
  })
})
