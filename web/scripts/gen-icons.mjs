// Rasterizes the favicon glyph (green rounded square + white pulse line) into
// the PNG icons the PWA manifest and iOS need. No dependencies: supersampled
// coverage + zlib. Run: node scripts/gen-icons.mjs
import { deflateSync } from 'node:zlib'
import { writeFileSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const out = resolve(dirname(fileURLToPath(import.meta.url)), '../public')
const GREEN = [0x0f, 0x7b, 0x45]
const WHITE = [255, 255, 255]
// Glyph in the favicon's 32-unit space.
const LINE = [[7, 17], [11, 17], [14, 10], [18, 22], [21, 17], [25, 17]]
const STROKE = 2.2

function segDist(px, py, [ax, ay], [bx, by]) {
  const dx = bx - ax, dy = by - ay
  const t = Math.max(0, Math.min(1, ((px - ax) * dx + (py - ay) * dy) / (dx * dx + dy * dy)))
  return Math.hypot(px - (ax + t * dx), py - (ay + t * dy))
}
function onLine(x, y) {
  for (let i = 1; i < LINE.length; i++) if (segDist(x, y, LINE[i - 1], LINE[i]) <= STROKE / 2) return true
  return false
}
function inRoundRect(x, y, r) {
  const cx = Math.max(r, Math.min(32 - r, x)), cy = Math.max(r, Math.min(32 - r, y))
  return x >= 0 && y >= 0 && x <= 32 && y <= 32 && Math.hypot(x - cx, y - cy) <= r
}

// mode: 'rounded' (transparent corners), 'full' (square background, glyph scaled by `scale`).
function render(size, mode, scale = 1) {
  const SS = 4
  const px = Buffer.alloc(size * size * 4)
  for (let j = 0; j < size; j++) {
    for (let i = 0; i < size; i++) {
      let bg = 0, fg = 0
      for (let sj = 0; sj < SS; sj++) {
        for (let si = 0; si < SS; si++) {
          const x = ((i + (si + 0.5) / SS) / size) * 32
          const y = ((j + (sj + 0.5) / SS) / size) * 32
          const inside = mode === 'full' || inRoundRect(x, y, 7)
          if (!inside) continue
          bg++
          const gx = 16 + (x - 16) / scale, gy = 16 + (y - 16) / scale
          if (onLine(gx, gy)) fg++
        }
      }
      const n = SS * SS, o = (j * size + i) * 4
      const a = bg / n
      const f = bg ? fg / bg : 0
      for (let c = 0; c < 3; c++) px[o + c] = Math.round(GREEN[c] * (1 - f) + WHITE[c] * f)
      px[o + 3] = Math.round(a * 255)
    }
  }
  return png(size, px)
}

const CRC = new Int32Array(256).map((_, n) => { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; return c })
function crc32(buf) { let c = -1; for (const b of buf) c = CRC[(c ^ b) & 0xff] ^ (c >>> 8); return (c ^ -1) >>> 0 }
function chunk(type, data) {
  const len = Buffer.alloc(4); len.writeUInt32BE(data.length)
  const td = Buffer.concat([Buffer.from(type), data])
  const crc = Buffer.alloc(4); crc.writeUInt32BE(crc32(td))
  return Buffer.concat([len, td, crc])
}
function png(size, rgba) {
  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(size, 0); ihdr.writeUInt32BE(size, 4)
  ihdr[8] = 8; ihdr[9] = 6 // 8-bit RGBA
  const raw = Buffer.alloc(size * (size * 4 + 1))
  for (let y = 0; y < size; y++) rgba.copy(raw, y * (size * 4 + 1) + 1, y * size * 4, (y + 1) * size * 4)
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', ihdr), chunk('IDAT', deflateSync(raw, { level: 9 })), chunk('IEND', Buffer.alloc(0))])
}

const icons = [
  ['pwa-192.png', 192, 'rounded', 1],
  ['pwa-512.png', 512, 'rounded', 1],
  ['pwa-maskable-512.png', 512, 'full', 0.72], // glyph inside the 80% safe zone
  ['apple-touch-icon.png', 180, 'full', 0.8], // iOS rounds the corners itself
]
for (const [name, size, mode, scale] of icons) {
  writeFileSync(resolve(out, name), render(size, mode, scale))
  console.log('wrote', name)
}
