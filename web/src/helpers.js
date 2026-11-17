import { ui } from './store'

// run wraps an action: shows the error toast, optional success text, returns the result or undefined.
export async function run(fn, okText) {
  try {
    const r = await fn()
    if (okText) ui.message?.success(okText)
    return r
  } catch (e) {
    if (e?.message !== '已取消') ui.message?.error(e?.message || String(e))
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

export function idsCsv(s) {
  return String(s || '').split(',').map((x) => Number(x.trim())).filter((x) => x > 0)
}
