import { api } from './api'
import { ui } from './store'

export { idsCsv, isHttpUrl, isSafeNextPath, computeDirtyKeys } from './utils'

// run wraps an action: shows the error toast, optional success text, returns the result or undefined.
export async function run(fn, okText) {
  try {
    const r = await fn()
    if (okText) ui.message?.success(okText)
    return r
  } catch (e) {
    if (!e?.silent && e?.message !== '已取消') ui.message?.error(e?.message || String(e))
    return undefined
  }
}

// ask shows a confirmation dialog; resolves true/false.
export function ask(title, content, { danger = false, okText = '确定' } = {}) {
  return new Promise((resolve) => {
    let settled = false
    const fin = (v) => { if (!settled) { settled = true; resolve(v) } }
    ui.dialog[danger ? 'error' : 'warning']({
      title, content, positiveText: okText, negativeText: '取消',
      onPositiveClick: () => fin(true), onNegativeClick: () => fin(false),
      onClose: () => fin(false), onMaskClick: () => fin(false), onEsc: () => fin(false),
    })
  })
}

// Shared step-up rotation action used by Routes and Subscription views.
export async function rotateAllKeys({ title = '一键轮换密钥', confirm = '轮换全部密钥需要确认身份' } = {}) {
  const ok = await ask(
    title,
    '会重新生成所有线路 UUID、Reality 密钥与 SS2022 密钥并下发（订阅链接本身不变）。所有客户端都要刷新订阅后才能继续使用。',
    { danger: true, okText: '继续' },
  )
  if (!ok) return false
  return Boolean(await run(() => api.post('/sub/rotate-all', {}, { confirm }), '已轮换，配置正在下发'))
}
