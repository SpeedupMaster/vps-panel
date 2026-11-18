<script setup>
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NDrawer, NDrawerContent, NButton } from 'naive-ui'
import Icon from './Icon.vue'
import { navItems, mobileTabs } from '../nav'
import { state, loadServers, connectEvents, disconnectEvents, refreshAlertCount } from '../store'
import { api } from '../api'
import { pwaInstall, installApp } from '../pwa'

const route = useRoute()
const router = useRouter()
const drawer = ref(false)
const version = ref('')

const active = computed(() => {
  const n = route.name
  return navItems.find((i) => i.id === n) ? n : route.meta.parent || n
})
const tabs = computed(() => mobileTabs.map((id) => navItems.find((i) => i.id === id)))
const moreActive = computed(() => !mobileTabs.includes(active.value))

function go(id) { drawer.value = false; router.push({ name: id }) }
async function logout() {
  try { await api.post('/auth/logout') } catch { /* ignore */ }
  disconnectEvents()
  state.me = null
  router.replace({ name: 'login' })
}

// Phones suspend the page (and its EventSource) in the background: on return,
// reconnect right away and refetch what may have changed meanwhile.
const online = ref(navigator.onLine)
let hiddenAt = 0
function resync() {
  disconnectEvents()
  connectEvents()
  refreshAlertCount()
  loadServers().catch(() => {})
}
function onVisibility() {
  if (document.visibilityState === 'hidden') { hiddenAt = Date.now(); return }
  if (hiddenAt && Date.now() - hiddenAt > 20000) resync()
  hiddenAt = 0
}
function onOnline() { online.value = true; resync() }
function onOffline() { online.value = false }

onMounted(async () => {
  connectEvents()
  refreshAlertCount()
  loadServers().catch(() => {})
  api.get('/system/version').then((v) => { version.value = v.current || '' }).catch(() => {})
  document.addEventListener('visibilitychange', onVisibility)
  window.addEventListener('online', onOnline)
  window.addEventListener('offline', onOffline)
})
onBeforeUnmount(() => {
  disconnectEvents()
  document.removeEventListener('visibilitychange', onVisibility)
  window.removeEventListener('online', onOnline)
  window.removeEventListener('offline', onOffline)
})
</script>

<template>
  <div class="shell">
    <div v-if="!online" class="offline-bar" role="status">网络已断开，显示的是断开前的数据</div>
    <aside class="side hide-mobile">
      <a class="brand" :href="$router.resolve({ name: 'overview' }).href" @click.prevent="go('overview')">
        <svg width="32" height="32" viewBox="0 0 32 32" aria-hidden="true"><rect width="32" height="32" rx="7" fill="#0f7b45" /><path d="M7 17h4l3-7 4 12 3-5h4" fill="none" stroke="#fff" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" /></svg>
        <span class="brand-text"><b>VPS Panel</b><small>自用节点管理</small></span>
      </a>
      <nav class="nav" aria-label="主导航">
        <a v-for="it in navItems" :key="it.id" :href="router.resolve({ name: it.id }).href"
          :class="['nav-item', { on: active === it.id }]" :aria-current="active === it.id ? 'page' : undefined"
          @click.prevent="go(it.id)">
          <Icon :d="it.d" />
          <span class="grow">{{ it.label }}</span>
          <span v-if="it.id === 'alerts' && state.alertsFiring" class="badge">{{ state.alertsFiring }}</span>
        </a>
      </nav>
      <div class="side-foot">
        <span class="row" style="gap: 6px"><span :class="['dot', state.live ? 'ok' : 'off']" />{{ state.live ? '实时连接' : '连接中…' }}</span>
        <span>面板 {{ version || '—' }} · {{ state.me?.username }}</span>
        <button v-if="pwaInstall.can || pwaInstall.ios" class="link-btn" style="text-align: left" @click="installApp">安装应用</button>
        <button class="link-btn" style="text-align: left" @click="logout">退出登录</button>
      </div>
    </aside>

    <header class="topbar only-mobile">
      <button class="icon-btn" aria-label="打开菜单" @click="drawer = true">
        <Icon d="M4 6h16 M4 12h16 M4 18h16" :size="22" />
      </button>
      <span class="top-title">{{ route.meta.title || 'VPS Panel' }}</span>
      <span :class="['dot', state.live ? 'ok' : 'off']" :title="state.live ? '实时连接' : '连接中'" />
      <button class="icon-btn" aria-label="告警" @click="go('alerts')">
        <Icon :d="navItems[6].d" :size="22" />
        <span v-if="state.alertsFiring" class="badge small-badge">{{ state.alertsFiring }}</span>
      </button>
    </header>

    <main class="main">
      <router-view v-slot="{ Component }">
        <keep-alive :include="['Overview', 'Latency']"><component :is="Component" /></keep-alive>
      </router-view>
    </main>

    <nav class="bottom only-mobile" aria-label="底部导航">
      <a v-for="it in tabs" :key="it.id" href="#" :class="['tab', { on: active === it.id }]" @click.prevent="go(it.id)">
        <span class="tab-icon"><Icon :d="it.d" :size="22" /><span v-if="it.id === 'alerts' && state.alertsFiring" class="badge small-badge">{{ state.alertsFiring }}</span></span>
        <span>{{ it.label }}</span>
      </a>
      <a href="#" :class="['tab', { on: moreActive }]" @click.prevent="drawer = true">
        <span class="tab-icon"><Icon d="M5 12h.01 M12 12h.01 M19 12h.01" :size="22" :width="3" /></span>
        <span>更多</span>
      </a>
    </nav>

    <n-drawer v-model:show="drawer" placement="left" :width="260">
      <n-drawer-content body-content-style="padding: 12px">
        <div class="brand" style="margin-bottom: 12px">
          <svg width="32" height="32" viewBox="0 0 32 32" aria-hidden="true"><rect width="32" height="32" rx="7" fill="#0f7b45" /><path d="M7 17h4l3-7 4 12 3-5h4" fill="none" stroke="#fff" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" /></svg>
          <span class="brand-text"><b>VPS Panel</b><small>{{ state.me?.username }}</small></span>
        </div>
        <nav class="nav">
          <a v-for="it in navItems" :key="it.id" href="#" :class="['nav-item', { on: active === it.id }]" @click.prevent="go(it.id)">
            <Icon :d="it.d" /><span class="grow">{{ it.label }}</span>
            <span v-if="it.id === 'alerts' && state.alertsFiring" class="badge">{{ state.alertsFiring }}</span>
          </a>
        </nav>
        <n-button v-if="pwaInstall.can || pwaInstall.ios" block secondary style="margin-top: 16px" @click="installApp">添加到主屏幕</n-button>
        <n-button block secondary :style="{ marginTop: pwaInstall.can || pwaInstall.ios ? '8px' : '16px' }" @click="logout">退出登录</n-button>
        <p class="muted small" style="margin-top: 12px">面板 {{ version || '—' }}</p>
      </n-drawer-content>
    </n-drawer>
  </div>
</template>

<style scoped>
.shell { min-height: 100vh; min-height: 100dvh; }
.side { position: fixed; inset: 0 auto 0 0; width: 220px; background: #fff; border-right: 1px solid var(--c-border); display: flex; flex-direction: column; padding: 18px 12px; gap: 18px; z-index: 10; }
.main { margin-left: 220px; min-width: 0; }
.brand { display: flex; align-items: center; gap: 10px; padding: 0 8px; color: var(--c-text); }
.brand-text { display: flex; flex-direction: column; line-height: 1.25; }
.brand-text b { font-size: 15px; }
.brand-text small { font-size: 12px; color: var(--c-muted); }
.nav { display: flex; flex-direction: column; gap: 2px; flex: 1; overflow-y: auto; }
.nav-item { display: flex; align-items: center; gap: 10px; min-height: 40px; padding: 0 12px; border-radius: 6px; color: var(--c-text-2); font-size: 14px; }
.nav-item:hover { background: var(--c-surface); color: var(--c-text); }
.nav-item.on { background: var(--c-primary-soft); color: var(--c-primary); font-weight: 600; }
.grow { flex: 1; }
.badge { min-width: 20px; height: 20px; padding: 0 6px; border-radius: 10px; background: var(--c-danger); color: #fff; font-size: 12px; font-weight: 600; display: inline-flex; align-items: center; justify-content: center; }
.side-foot { border-top: 1px solid var(--c-border); padding: 12px 8px 0; display: flex; flex-direction: column; gap: 6px; font-size: 12px; color: var(--c-muted); }
@media (max-width: 767px) {
  .main { margin-left: 0; padding-top: calc(52px + var(--safe-top)); }
  .topbar { position: fixed; top: 0; left: 0; right: 0; z-index: 20; height: calc(52px + var(--safe-top)); padding: var(--safe-top) 6px 0; background: rgba(255, 255, 255, .96); backdrop-filter: blur(8px); border-bottom: 1px solid var(--c-border); display: flex; align-items: center; gap: 4px; }
  .top-title { flex: 1; font-weight: 600; font-size: 16px; padding-left: 4px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .icon-btn { position: relative; width: 44px; height: 44px; border: none; background: none; color: var(--c-text); display: inline-flex; align-items: center; justify-content: center; border-radius: 6px; cursor: pointer; }
  .small-badge { position: absolute; top: 4px; right: 2px; min-width: 16px; height: 16px; font-size: 10px; padding: 0 4px; }
  .bottom { position: fixed; bottom: 0; left: 0; right: 0; z-index: 20; display: flex; background: rgba(255, 255, 255, .97); backdrop-filter: blur(8px); border-top: 1px solid var(--c-border); padding-bottom: var(--safe-bottom); }
  .tab { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 2px; height: 56px; font-size: 11px; color: var(--c-muted); }
  .tab.on { color: var(--c-primary); font-weight: 600; }
  .tab-icon { position: relative; display: inline-flex; }
  .tab-icon .small-badge { top: -4px; right: -10px; }
}
</style>
