export function bytes(n, digits = 1) {
  n = Number(n) || 0
  const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = 0
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++ }
  return (i === 0 ? n.toFixed(0) : n.toFixed(n >= 100 ? 0 : digits)) + ' ' + u[i]
}
export const rate = (n) => bytes(n) + '/s'
export function pct(v, d = 0) { return v == null || isNaN(v) ? '—' : Number(v).toFixed(d) + '%' }
export function ms(v) { return v == null || v <= 0 ? '—' : Math.round(v) + ' ms' }

function toMs(t) {
  if (typeof t === 'number') return t < 1e12 ? t * 1000 : t
  const d = new Date(t).getTime()
  return isNaN(d) ? 0 : d
}
export function valid(t) { const d = toMs(t); return d > 946684800000 }

export function ago(t) {
  if (!t || !valid(t)) return '—'
  const s = Math.round((Date.now() - toMs(t)) / 1000)
  if (s < 5) return '刚刚'
  if (s < 60) return s + ' 秒前'
  if (s < 3600) return Math.floor(s / 60) + ' 分钟前'
  if (s < 86400) return Math.floor(s / 3600) + ' 小时前'
  return Math.floor(s / 86400) + ' 天前'
}
export function dur(sec) {
  sec = Math.max(0, Math.round(sec))
  if (sec < 60) return sec + ' 秒'
  if (sec < 3600) return Math.floor(sec / 60) + ' 分钟'
  if (sec < 86400) return (sec / 3600).toFixed(1) + ' 小时'
  return Math.floor(sec / 86400) + ' 天'
}
const p2 = (n) => String(n).padStart(2, '0')
export function dt(t, withYear = false) {
  if (!t || !valid(t)) return '—'
  const d = new Date(toMs(t))
  const s = `${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}`
  return withYear ? d.getFullYear() + '-' + s : s
}
export function date(t) {
  if (!t || !valid(t)) return '—'
  const d = new Date(toMs(t))
  return `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())}`
}
export function daysUntil(t) {
  if (!t || !valid(t)) return null
  return Math.ceil((toMs(t) - Date.now()) / 86400000)
}
export function flag(cc) {
  if (!cc || cc.length !== 2) return '🏳'
  return String.fromCodePoint(...[...cc.toUpperCase()].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65))
}
export const roleLabel = { probe: '仅探针', entry: '入口 · Reality', landing: '落地 · SS2022' }
export const sevLabel = { critical: '严重', warning: '警告', info: '提醒', recovery: '恢复' }
export const sevClass = { critical: 'bad', warning: 'warn', info: 'info' }
export const carrierLabel = { telecom: '电信', unicom: '联通', mobile: '移动' }
export function maskIP(ip) {
  if (!ip) return '—'
  if (ip.includes(':')) return ip.split(':').slice(0, 2).join(':') + ':****'
  const p = ip.split('.')
  return p.length === 4 ? `${p[0]}.**.**.${p[3]}` : ip
}
export async function copy(text) {
  try { await navigator.clipboard.writeText(text); return true } catch { /* insecure context: fall back */ }
  const ta = document.createElement('textarea')
  ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0'
  document.body.appendChild(ta); ta.select()
  const ok = document.execCommand('copy'); ta.remove(); return ok
}
// Overall packet loss (%) of probe rows over a period. Every row is one round of
// the same probe_count packets, so total lost / total sent equals the mean of the
// rows' loss percentages. Hourly rollups (ranges over 2 days) weigh each hour
// equally. Returns null when there are no rows.
export function overallLoss(rows) {
  let sum = 0, n = 0
  for (const r of rows) { if (r && typeof r.loss === 'number') { sum += r.loss; n++ } }
  return n ? sum / n : null
}
export function lossText(v) {
  if (v == null) return '—'
  if (v <= 0) return '0%'
  if (v < 0.1) return '<0.1%'
  return `${v >= 10 ? Math.round(v) : v.toFixed(1)}%`
}
// Same thresholds as the chart markers: orange 1–20%, red above 20%.
export const lossClass = (v) => (v == null || v < 1 ? 'muted' : v > 20 ? 'bad-text' : 'warn-text')
export function latClass(v) {
  if (v == null || v <= 0) return 'off'
  if (v < 120) return 'ok'
  if (v < 250) return 'warn'
  return 'bad'
}
