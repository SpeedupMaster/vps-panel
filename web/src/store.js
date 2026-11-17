// Shared app state: the current admin, the server list and the live event stream.
import { reactive } from 'vue'
import { api } from './api'

export const ui = { message: null, dialog: null, notification: null }

export const state = reactive({
  me: null,
  servers: [],
  serversLoaded: false,
  alertsFiring: 0,
  live: false,
})

export async function loadMe() {
  state.me = await api.get('/auth/me')
  return state.me
}

export async function loadServers() {
  state.servers = await api.get('/servers')
  state.serversLoaded = true
  return state.servers
}

export function serverName(id) {
  if (!id) return '—'
  const s = state.servers.find((x) => x.id === id)
  return s ? s.name : '#' + id
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
  es = new EventSource('/api/events')
  es.onopen = () => { state.live = true }
  for (const t of ['metrics', 'server', 'alert', 'probe', 'task', 'tool', 'route', 'xray']) {
    es.addEventListener(t, (e) => {
      let d = null
      try { d = JSON.parse(e.data) } catch { return }
      applyEvent(t, d)
      emit(t, d)
    })
  }
  es.onerror = () => {
    state.live = false
    es.close(); es = null
    clearTimeout(retry)
    retry = setTimeout(connectEvents, 5000)
  }
}

export function disconnectEvents() {
  clearTimeout(retry)
  if (es) { es.close(); es = null }
  state.live = false
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
  } else if (t === 'alert') {
    refreshAlertCount()
  }
}
