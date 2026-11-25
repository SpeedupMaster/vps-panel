// Shared app state: the current admin, the server list and the live event stream.
import { reactive } from 'vue'
import { api } from './api'

/**
 * @typedef {Object} AdminInfo
 * @property {string} username
 * @property {boolean} totp
 * @property {number} [recovery_left]
 * @property {string} [password_at]
 */

/**
 * @typedef {Object} ServerSummary
 * @property {number} id
 * @property {string} name
 * @property {string} mode
 * @property {string} status
 * @property {boolean} online
 * @property {boolean} blocked
 * @property {boolean} cap_reality
 * @property {boolean} cap_landing
 * @property {string} [group]
 * @property {string} [region]
 * @property {string} [country_code]
 * @property {string} [ipv4]
 * @property {string} [ipv6]
 * @property {Record<string, any>} [system]
 * @property {Record<string, any>} [switches]
 * @property {Record<string, any>} [latest]
 * @property {Record<string, any>} [usage]
 */

/**
 * @typedef {Object} AppState
 * @property {AdminInfo | null} me
 * @property {ServerSummary[]} servers
 * @property {boolean} serversLoaded
 * @property {number} alertsFiring
 * @property {boolean} live
 */

/** @type {{ message: any, dialog: any, notification: any }} */
export const ui = { message: null, dialog: null, notification: null }

/** @type {AppState} */
export const state = reactive({
  me: null,
  servers: [],
  serversLoaded: false,
  alertsFiring: 0,
  live: false,
})

/** @returns {Promise<AdminInfo>} */
export async function loadMe() {
  state.me = await api.get('/auth/me')
  return state.me
}

/** @returns {Promise<ServerSummary[]>} */
export async function loadServers() {
  state.servers = await api.get('/servers')
  state.serversLoaded = true
  return state.servers
}

/** @param {number | string} id @returns {string} */
export function serverName(id) {
  const n = Number(id)
  if (!n) return '—'
  const s = state.servers.find((x) => x.id === n)
  return s ? s.name : '#' + n
}

export async function refreshAlertCount() {
  try {
    const r = await api.get('/alerts/events?status=firing&limit=1')
    state.alertsFiring = r.firing || 0
  } catch { /* ignore */ }
}

// ---- server-sent events ----
const listeners = new Map()
let es = null
let retry = null
let dropped = false
let backoffMs = 1000
let connGen = 0

export function on(type, fn) {
  if (!listeners.has(type)) listeners.set(type, new Set())
  listeners.get(type).add(fn)
  return () => listeners.get(type)?.delete(fn)
}

function emit(type, data) {
  listeners.get(type)?.forEach((fn) => { try { fn(data) } catch (e) { console.error(e) } })
}

export function connectEvents() {
  if (es) return
  clearTimeout(retry)
  retry = null
  const gen = connGen
  const sock = new EventSource('/api/events')
  es = sock
  sock.onopen = () => {
    if (es !== sock || gen !== connGen) { sock.close(); return }
    state.live = true
    backoffMs = 1000
    if (dropped) {
      dropped = false
      loadServers().catch(() => {})
      refreshAlertCount()
    }
  }
  for (const t of ['metrics', 'server', 'alert', 'probe', 'task', 'tool', 'route', 'xray']) {
    sock.addEventListener(t, (e) => {
      if (es !== sock) return
      let d = null
      try { d = JSON.parse(e.data) } catch { return }
      applyEvent(t, d)
      emit(t, d)
    })
  }
  sock.onerror = () => {
    if (es !== sock) return
    state.live = false
    sock.close()
    es = null
    dropped = true
    clearTimeout(retry)
    const delay = backoffMs
    backoffMs = Math.min(backoffMs * 2, 30000)
    retry = setTimeout(async () => {
      retry = null
      if (gen !== connGen) return
      try {
        await api.get('/auth/me')
      } catch (e) {
        if (gen !== connGen || e?.status === 401) return
        const nextDelay = backoffMs
        backoffMs = Math.min(backoffMs * 2, 30000)
        retry = setTimeout(() => { if (gen === connGen) connectEvents() }, nextDelay)
        return
      }
      if (gen === connGen) connectEvents()
    }, delay)
  }
}

export function disconnectEvents() {
  connGen++
  clearTimeout(retry)
  retry = null
  if (es) { es.close(); es = null }
  state.live = false
  dropped = false
  backoffMs = 1000
}

function applyEvent(t, d) {
  if (t === 'metrics') {
    const s = state.servers.find((x) => x.id === d.id)
    if (s) { s.latest = d.m; s.online = true; if (s.status !== 'online') s.status = 'online' }
  } else if (t === 'server') {
    const s = state.servers.find((x) => x.id === d.id)
    if (!s) return
    if (d.status) { s.status = d.status; s.online = d.status === 'online' }
    if (d.blocked !== undefined) s.blocked = d.blocked
    if (d.system) s.system = d.system
  } else if (t === 'alert') {
    refreshAlertCount()
  }
}
