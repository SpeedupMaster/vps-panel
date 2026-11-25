<script setup>
import { ref, shallowRef, triggerRef, computed, watch, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NTabs, NTabPane, NDropdown } from 'naive-ui'
import { api } from '../api'
import { loadServers, on } from '../store'
import { run, ask } from '../helpers'
import { ago, flag, dur } from '../format'
import { isMobile } from '../mobile'
import { useQueryTab } from '../composables/useQueryTab'
import TaskModal from '../components/TaskModal.vue'
import ServerOverviewTab from './server/ServerOverviewTab.vue'
import ServerXrayTab from './server/ServerXrayTab.vue'
import ServerTrafficTab from './server/ServerTrafficTab.vue'
import ServerAssetTab from './server/ServerAssetTab.vue'
import ServerConfigsTab from './server/ServerConfigsTab.vue'
import ServerModals from './server/ServerModals.vue'

const route = useRoute()
const router = useRouter()
const id = computed(() => Number(route.params.id))
const tab = useQueryTab(['overview', 'xray', 'traffic', 'asset', 'configs'], 'overview')

const sv = ref(null)
const metrics = shallowRef([])
const range = ref(3600)
const backhaul = ref({ targets: [], series: {} })
const xray = ref(null)
const myRoutes = ref([])
const traffic = ref(null)
const configs = ref([])
const releases = ref({ agent: '', xray: '' })
const task = ref({ show: false, id: null, title: '' })
const upgrade = ref({ show: false, cap_reality: true, cap_landing: false })
const install = ref({ show: false, command: '', expires_at: '' })
const cfgView = ref({ show: false, title: '', body: '', diff: false })
const preview = ref({ show: false, config: '', firewall: null })
const moreBusy = ref(false)

let svAbort = null
let metricsAbort = null
let bhAbort = null
let tabAbort = null

async function load() {
  svAbort?.abort()
  const ac = new AbortController()
  svAbort = ac
  const sid = id.value
  const r = await run(() => api.get(`/servers/${sid}`, { signal: ac.signal }))
  if (ac.signal.aborted || id.value !== sid) return
  if (r) {
    sv.value = r
    if (r.mode !== 'node' && (tab.value === 'xray' || tab.value === 'configs')) tab.value = 'overview'
    if (tab.value === 'xray' && !xray.value) loadXray()
    if (tab.value === 'configs' && !configs.value.length) loadConfigs()
  }
}
async function loadMetrics() {
  metricsAbort?.abort()
  const ac = new AbortController()
  metricsAbort = ac
  const sid = id.value
  const r = await run(() => api.get(`/servers/${sid}/metrics?range=${range.value}`, { signal: ac.signal }))
  if (ac.signal.aborted || id.value !== sid) return
  metrics.value = r || []
}
async function loadBackhaul() {
  bhAbort?.abort()
  const ac = new AbortController()
  bhAbort = ac
  const sid = id.value
  const t = (await api.get('/probes/targets', { signal: ac.signal }).catch(() => null))?.items || []
  if (ac.signal.aborted || id.value !== sid) return
  const ids = t.filter((x) => x.enabled && ['telecom', 'unicom', 'mobile'].includes(x.carrier)).map((x) => x.id)
  const s = ids.length ? await api.get(`/probes/series?server_id=${sid}&range=86400&targets=${ids.join(',')}`, { signal: ac.signal }).catch(() => ({})) : {}
  if (ac.signal.aborted || id.value !== sid) return
  backhaul.value = { targets: t, series: s || {} }
}
async function loadXray() {
  if (sv.value?.mode !== 'node') return
  tabAbort?.abort()
  const ac = new AbortController()
  tabAbort = ac
  const sid = id.value
  const [x, rs] = await Promise.all([
    run(() => api.get(`/servers/${sid}/xray`, { signal: ac.signal })),
    run(() => api.get('/routes', { signal: ac.signal })),
  ])
  if (ac.signal.aborted || id.value !== sid) return
  if (x) xray.value = x
  myRoutes.value = (rs || []).filter((r) => r.entry_server_id === sid || r.landing_server_id === sid)
}
async function loadTraffic() {
  tabAbort?.abort()
  const ac = new AbortController()
  tabAbort = ac
  const sid = id.value
  const r = await run(() => api.get(`/servers/${sid}/traffic?days=30`, { signal: ac.signal }))
  if (ac.signal.aborted || id.value !== sid) return
  traffic.value = r
}
async function loadConfigs() {
  tabAbort?.abort()
  const ac = new AbortController()
  tabAbort = ac
  const sid = id.value
  const r = await run(() => api.get(`/servers/${sid}/configs`, { signal: ac.signal }))
  if (ac.signal.aborted || id.value !== sid) return
  configs.value = r || []
}

watch(id, (nextId, prevId) => {
  if (!nextId) return
  sv.value = null; xray.value = null; traffic.value = null; configs.value = []; metrics.value = []
  backhaul.value = { targets: [], series: {} }
  if (prevId && prevId !== nextId) tab.value = 'overview'
  load()
  loadMetrics()
  loadBackhaul()
  if (tab.value === 'traffic') loadTraffic()
  api.get('/releases/xray').then((r) => { releases.value.xray = r.latest_xray; releases.value.agent = r.agent_published }).catch(() => {})
}, { immediate: true })
watch(range, loadMetrics)
watch(tab, (t) => {
  if (t === 'xray' && !xray.value) loadXray()
  if (t === 'traffic' && !traffic.value) loadTraffic()
  if (t === 'configs') loadConfigs()
})

const offM = on('metrics', (d) => {
  if (d.id !== id.value || !sv.value) return
  sv.value.latest = d.m; sv.value.online = true
  if (range.value <= 3600 && d.m) {
    const m = d.m
    metrics.value.push({ TS: m.ts, CPU: m.cpu, Steal: m.steal, Load1: m.load1, MemUsed: m.mem_used, DiskUsed: m.disk_used, NetIn: m.net_in, NetOut: m.net_out, TCP: m.tcp, UDP: m.udp })
    if (metrics.value.length > 2000) metrics.value.shift()
    triggerRef(metrics)
  }
})
const offS = on('server', (d) => { if (d.id === id.value) load() })
const offX = on('xray', (d) => { if (d.server_id === id.value) { load(); if (xray.value) loadXray() } })
onBeforeUnmount(() => {
  offM(); offS(); offX()
  svAbort?.abort(); metricsAbort?.abort(); bhAbort?.abort(); tabAbort?.abort()
})

const roleText = computed(() => {
  const s = sv.value
  if (!s) return []
  if (s.mode !== 'node') return ['仅探针']
  return [s.cap_reality && '入口 · Reality', s.cap_landing && '落地 · SS2022'].filter(Boolean)
})
const statusTag = computed(() => {
  const s = sv.value
  if (!s) return ['', '']
  if (s.blocked) return ['bad', '疑似被封']
  return { online: ['ok', '在线'], offline: ['bad', '离线'], suspect: ['warn', '不稳定'], pending: ['', '待安装'] }[s.status] || ['', s.status]
})
const moreOptions = computed(() => {
  const s = sv.value
  if (!s) return []
  return [
    { label: '编辑资产信息', key: 'edit' },
    s.mode === 'node' ? { label: '降级为仅探针', key: 'downgrade' } : { label: '升级为节点', key: 'upgrade' },
    { label: '重新生成安装命令', key: 'token' },
    { label: '重启 Agent', key: 'restart_agent' },
    { type: 'divider', key: 'd' },
    { label: '删除服务器', key: 'delete', props: { style: 'color: #b4233a' } },
  ]
})

async function runTool(tool, title, args) {
  const r = await run(() => api.post(`/servers/${id.value}/tools/${tool}`, args || {}))
  if (r) task.value = { show: true, id: r.task_id, title }
}
async function onMore(k) {
  if (k === 'edit') { tab.value = 'asset'; return }
  if (k === 'upgrade') { upgrade.value.show = true; return }
  if (k === 'restart_agent') { runTool('restart_agent', '重启 Agent'); return }
  if (moreBusy.value) return
  moreBusy.value = true
  try {
    if (k === 'downgrade') {
      if (!(await ask('降级为仅探针', '会停止并卸载这台机器上的 Xray，相关配置会删除。确定继续？', { danger: true }))) return
      if (await run(() => api.post(`/servers/${id.value}/downgrade`), '已降级')) { load(); loadServers() }
    } else if (k === 'token') {
      const r = await run(() => api.post(`/servers/${id.value}/token`))
      if (r) install.value = { show: true, command: r.command, expires_at: r.install_token_exp || r.expires_at || new Date(Date.now() + 24 * 3600 * 1000).toISOString() }
    } else if (k === 'delete') {
      if (!(await ask(`删除 ${sv.value.name}`, '会删除这台服务器的所有监控数据和配置历史，Agent 将被断开。此操作不能撤销。', { danger: true, okText: '删除' }))) return
      if (await run(() => api.del(`/servers/${id.value}`, { confirm: '删除服务器需要确认身份' }), '已删除')) { loadServers(); router.replace({ name: 'overview' }) }
    }
  } finally { moreBusy.value = false }
}
</script>

<template>
  <div class="page">
    <div class="crumb small muted"><router-link :to="{ name: 'overview' }">总览</router-link> / {{ sv?.name || '…' }}</div>
    <template v-if="sv">
      <div class="page-head head">
        <span class="flag">{{ flag(sv.country_code) }}</span>
        <div class="title">
          <div class="row" style="gap: 8px">
            <h1>{{ sv.name }}</h1>
            <span v-for="r in roleText" :key="r" class="tag info">{{ r }}</span>
            <span :class="['tag', statusTag[0]]">{{ statusTag[1] }}</span>
          </div>
          <div class="page-desc">
            {{ sv.region || '地区未知' }}
            <template v-if="sv.latest?.uptime"> · 已运行 {{ dur(sv.latest.uptime) }}</template>
            · 最后心跳 {{ ago(sv.last_seen_at) }}
          </div>
        </div>
        <span class="spacer" />
        <n-button v-if="sv.mode === 'node'" :disabled="!sv.online" @click="runTool('restart_xray', '重启 Xray')">重启 Xray</n-button>
        <n-button :disabled="!sv.online" @click="router.push({ name: 'tools', query: { server: sv.id, tool: 'diagnose', run: 1 } })">一键诊断</n-button>
        <n-dropdown :options="moreOptions" trigger="click" @select="onMore"><n-button :loading="moreBusy" :disabled="moreBusy">更多</n-button></n-dropdown>
      </div>

      <div v-if="sv.status === 'pending'" class="banner info">
        Agent 还没连上。<button class="link-btn" @click="onMore('token')">获取安装命令</button>
      </div>

      <n-tabs v-model:value="tab" type="line" animated :tab-style="isMobile ? 'padding: 8px 4px' : ''">
        <n-tab-pane name="overview" tab="概览">
          <server-overview-tab :sv="sv" :metrics="metrics" :range="range" :backhaul="backhaul" :releases="releases"
            @update:range="range = $event" @run-tool="runTool" />
        </n-tab-pane>
        <n-tab-pane v-if="sv.mode === 'node'" name="xray" tab="Xray">
          <server-xray-tab :sv="sv" :xray="xray" :my-routes="myRoutes"
            @reload="loadXray" @run-tool="runTool" @task="(t) => (task = { show: true, ...t })" @preview="(r) => (preview = { show: true, config: r.config, firewall: r.firewall })" />
        </n-tab-pane>
        <n-tab-pane name="traffic" tab="流量">
          <server-traffic-tab :sv="sv" :traffic="traffic" />
        </n-tab-pane>
        <n-tab-pane name="asset" tab="资产">
          <server-asset-tab :sv="sv" @saved="load" />
        </n-tab-pane>
        <n-tab-pane v-if="sv.mode === 'node'" name="configs" tab="配置历史">
          <server-configs-tab :sv="sv" :configs="configs"
            @view-config="(v) => (cfgView = { show: true, ...v })" @task="(t) => (task = { show: true, ...t })" @reload="loadConfigs" />
        </n-tab-pane>
      </n-tabs>
    </template>
    <div v-else class="empty">加载中…</div>

    <task-modal v-model:show="task.show" :task-id="task.id" :title="task.title" @done="load" />
    <server-modals :server-id="id" :upgrade="upgrade" :install="install" :cfg-view="cfgView" :preview="preview" @upgraded="load" />
  </div>
</template>

<style scoped>
.crumb a { color: var(--c-muted); }
.head .flag { font-size: 34px; }
.title h1 { font-size: 22px; }
@media (max-width: 767px) {
  .head .flag { font-size: 26px; }
  .head .title { flex: 1 1 calc(100% - 50px); }
}
</style>
