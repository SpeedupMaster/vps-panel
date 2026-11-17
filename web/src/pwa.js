// Service worker registration with an "update available" prompt (no silent
// reloads: a half-filled form must not be thrown away), plus the "install
// app" entry point.
import { h, reactive } from 'vue'
import { NButton } from 'naive-ui'
import { registerSW } from 'virtual:pwa-register'
import { ui } from './store'

const standalone = () => window.matchMedia?.('(display-mode: standalone)').matches || navigator.standalone === true
const isIOS = /iphone|ipad|ipod/i.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1)

// can: the browser offered an install prompt (Chrome / Edge / Android).
// ios: Safari has no prompt; the user adds it from the share menu.
export const pwaInstall = reactive({ can: false, ios: false, installed: standalone() })
let deferred = null

if (typeof window !== 'undefined') {
  window.addEventListener('beforeinstallprompt', (e) => {
    e.preventDefault()
    deferred = e
    pwaInstall.can = true
  })
  window.addEventListener('appinstalled', () => {
    deferred = null
    Object.assign(pwaInstall, { can: false, installed: true })
    ui.message?.success('已安装，可以从桌面或主屏幕打开')
  })
  pwaInstall.ios = isIOS && !pwaInstall.installed
}

export async function installApp() {
  if (deferred) {
    deferred.prompt()
    const { outcome } = await deferred.userChoice
    if (outcome === 'accepted') pwaInstall.can = false
    deferred = null
    return
  }
  if (pwaInstall.ios) {
    ui.dialog?.info({ title: '添加到主屏幕', content: '在 Safari 中点底部的「分享」按钮，再选「添加到主屏幕」。', positiveText: '知道了' })
  }
}

export function setupPWA() {
  if (!('serviceWorker' in navigator) || import.meta.env.DEV) return
  let notice = null
  const update = registerSW({
    immediate: true,
    onNeedRefresh() {
      // The waiting worker can be reported more than once (initial
      // registration plus the hourly check): keep a single notice.
      if (notice) return
      const n = notice = ui.notification?.create({
        onClose: () => { notice = null },
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
