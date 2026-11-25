// Small fetch wrapper: JSON in/out, CSRF header, 401 -> login.
import router from './router'
import { state, disconnectEvents, ui } from './store'

export class ApiError extends Error {
  constructor(msg, status) { super(msg); this.status = status }
}

let confirmHook = null
let last401Toast = 0
// Registered by App.vue: shows the TOTP / password prompt and resolves with the code (null = cancelled).
export function setConfirmHook(fn) { confirmHook = fn }

export async function request(method, url, body, opts = {}) {
  const headers = { 'X-Requested-With': 'vps-panel' }
  let payload = body
  if (body !== undefined && !(body instanceof FormData)) {
    headers['Content-Type'] = 'application/json'
    payload = JSON.stringify(body)
  }
  if (opts.confirm) {
    if (!confirmHook) throw new ApiError('无法验证', 0)
    const code = await confirmHook(typeof opts.confirm === 'string' ? opts.confirm : '')
    if (code == null) throw new ApiError('已取消', 0)
    headers['X-Confirm'] = code
  }
  let res
  try {
    res = await fetch('/api' + url, { method, headers, body: payload, credentials: 'same-origin', signal: opts.signal })
  } catch (e) {
    if (e?.name === 'AbortError') {
      const err = new ApiError('已取消', 0)
      err.aborted = true
      err.silent = true
      throw err
    }
    throw new ApiError('网络错误：' + e.message, 0)
  }
  if (res.status === 401 && !url.startsWith('/auth/login')) {
    state.me = null
    state.servers = []
    state.serversLoaded = false
    disconnectEvents()
    const cur = router.currentRoute.value
    const onAppRoute = !!cur.name && cur.name !== 'login' && cur.name !== 'offline'
    if (onAppRoute) {
      const now = Date.now()
      if (now - last401Toast > 3000) {
        last401Toast = now
        ui.message?.error('未登录或会话已过期')
      }
      router.replace({ name: 'login', query: { next: cur.fullPath } })
    }
    const err = new ApiError('未登录或会话已过期', 401)
    err.silent = true
    throw err
  }
  if (opts.responseType === 'blob' && res.ok) {
    return await res.blob()
  }
  const ct = res.headers.get('Content-Type') || ''
  const data = ct.includes('json') ? await res.json() : await res.text()
  if (!res.ok) throw new ApiError((data && data.error) || res.statusText, res.status)
  return data
}

export const api = {
  get: (u, o) => request('GET', u, undefined, o),
  blob: (u, o) => request('GET', u, undefined, { ...o, responseType: 'blob' }),
  post: (u, b, o) => request('POST', u, b ?? {}, o),
  put: (u, b, o) => request('PUT', u, b ?? {}, o),
  del: (u, o) => request('DELETE', u, undefined, o),
}

// streamTask follows /tasks/:id/stream; onChunk gets output text; resolves with the final status.
export function streamTask(id, onChunk) {
  return new Promise((resolve) => {
    const es = new EventSource(`/api/tasks/${id}/stream`)
    es.addEventListener('output', (e) => e.data && onChunk(e.data))
    es.addEventListener('done', (e) => { es.close(); resolve(e.data) })
    es.onerror = () => { es.close(); resolve('disconnected') }
  })
}
