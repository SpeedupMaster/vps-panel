// Pre-compress built text assets to .gz after Vite + VitePWA finish so Go
// embeds compressed assets without breaking Workbox's precache manifest scan.
// index.html stays uncompressed because the server injects <base href> into it.
import { readdirSync, readFileSync, statSync, unlinkSync, writeFileSync } from 'node:fs'
import { extname, resolve } from 'node:path'
import { gzipSync } from 'node:zlib'

const outDir = resolve(import.meta.dirname, '../../internal/panel/webui/dist')
const exts = new Set(['.js', '.css', '.svg'])

let saved = 0
let count = 0

function walk(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = resolve(dir, entry.name)
    if (entry.isDirectory()) {
      walk(full)
      continue
    }
    if (!exts.has(extname(entry.name))) continue
    if (statSync(full).size < 1024) continue
    const raw = readFileSync(full)
    const gz = gzipSync(raw, { level: 9 })
    if (gz.length < raw.length) {
      writeFileSync(full + '.gz', gz)
      unlinkSync(full)
      saved += raw.length - gz.length
      count++
    }
  }
}

walk(outDir)
console.log(`gzip-dist: compressed ${count} files, saved ${(saved / 1024).toFixed(1)} KiB`)
