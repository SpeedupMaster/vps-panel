// Service worker registration with an "update available" prompt (no silent
// reloads: a half-filled form must not be thrown away).
import { h } from 'vue'
import { NButton } from 'naive-ui'
import { registerSW } from 'virtual:pwa-register'
import { ui } from './store'

export function setupPWA() {
  if (!('serviceWorker' in navigator) || import.meta.env.DEV) return
  const update = registerSW({
    immediate: true,
    onNeedRefresh() {
      const n = ui.notification?.create({
        title: '面板已更新',
        content: '刷新后使用新版本。',
        action: () => h(NButton, { size: 'small', type: 'primary', onClick: () => {
          n?.destroy()
          update(true)
          // update() reloads once the new worker takes control; make sure we
          // land on the new build even if that event never arrives.
          setTimeout(() => location.reload(), 2000)
        } }, { default: () => '刷新' }),
      })
    },
    onRegisteredSW(_url, reg) {
      // Check for a new build every hour while the app stays open.
      if (reg) setInterval(() => reg.update().catch(() => {}), 3600 * 1000)
    },
  })
}
