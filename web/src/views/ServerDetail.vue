<script setup>
import { ref, shallowRef, triggerRef, computed, watch, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NTabs, NTabPane, NSelect, NInput, NInputNumber, NDatePicker, NDropdown, NModal, NSwitch, NProgress } from 'naive-ui'
import { api } from '../api'
import { state, loadServers, on, ui } from '../store'
import { run, ask } from '../helpers'
import { bytes, rate, pct, ago, dt, date, daysUntil, flag, dur, carrierLabel, overallLoss, lossText, lossClass, copy, countdown } from '../format'
import { isMobile } from '../mobile'
import Chart from '../components/Chart.vue'
import TaskModal from '../components/TaskModal.vue'

const route = useRoute()
const router = useRouter()
const id = computed(() => Number(route.params.id))
const sv = ref(null)
const tab = ref('overview')
const metrics = shallowRef([])
const range = ref(3600)
const xray = ref(null)
const myRoutes = ref([])
const traffic = ref(null)
const configs = ref([])
const releases = ref({ agent: '', xray: '' })
const task = ref({ show: false, id: null, title: '' })

let svAbort = null
let metricsAbort = null
let bhAbort = null
let tabAbort = null
let rollbackTimer = null

async function load() {
  svAbort?.abort()
  const ac = new AbortController()
  svAbort = ac
  const sid = id.value
  const r = await run(() => api.get(`/servers/${sid}`, { signal: ac.signal }))
  if (ac.signal.aborted || id.value !== sid) return
  if (r) sv.value = r
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
  if (x) { xray.value = x; fillXrayForms(x) }
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

// ---------- backhaul latency (last 24h, median per carrier) ----------
const backhaul = ref({ targets: [], series: {} })
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
const mbps = (v) => (!v ? '未设置' : v >= 1000 && v % 1000 === 0 ? `${v / 1000} Gbps` : `${v} Mbps`)
// Why the backhaul chart is empty, so the user knows what to fix.
const bhEmpty = computed(() => {
  const t = backhaul.value.targets
  if (!t.length) return '还没有探测目标，去「延迟」页同步 zstatic 节点或添加自定义目标。'
  if (!t.some((x) => x.enabled && ['telecom', 'unicom', 'mobile'].includes(x.carrier))) return '没有启用的国内三网探测目标，去「延迟」页启用。'
  if (sv.value?.status !== 'online') return '机器不在线，上线后约 1 分钟出数据。'
  return '探测目标已启用，但这台机器近 24 小时没有上报结果。可能刚连上（约 1 分钟出第一轮）；一直没有的话，用 journalctl -u vpsagent 看 Agent 日志。'
})
const bhColors = { telecom: '#2f6fd0', unicom: '#b8650a', mobile: '#0f7b45' }
// Overall 24h loss per carrier (total lost / total sent), only for carriers with data.
const bhLoss = computed(() => {
  const { targets, series } = backhaul.value
  return ['telecom', 'unicom', 'mobile']
    .map((c) => ({ c, v: overallLoss(targets.filter((t) => t.carrier === c).flatMap((t) => series[t.id] || [])) }))
    .filter((x) => x.v != null)
})
const bhOpt = computed(() => {
  const { targets, series } = backhaul.value
  const buckets = new Map()
  for (const tg of targets) {
    for (const r of series[tg.id] || []) {
      const k = Math.floor(r.ts / 300) * 300
      if (!buckets.has(k)) buckets.set(k, { telecom: { v: [], l: [] }, unicom: { v: [], l: [] }, mobile: { v: [], l: [] } })
      const b = buckets.get(k)[tg.carrier]
      if (!b) continue
      b.l.push(r.loss)
      if (r.loss < 100 && r.avg > 0) b.v.push(r.avg)
    }
  }
  const ks = [...buckets.keys()].sort((a, b) => a - b)
  if (!ks.length) return null
  const med = (a) => { if (!a.length) return null; const s = [...a].sort((x, y) => x - y); const m = s.length >> 1; return Math.round(s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2) }
  const x = ks.map((k) => { const d = new Date(k * 1000); return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}` })
  const lossOf = (c, i) => { const l = buckets.get(ks[i])[c].l; return l.length ? Math.round(l.reduce((a, b) => a + b, 0) / l.length) : 0 }
  return {
    grid: { left: 44, right: 10, top: 28, bottom: 24 },
    legend: { top: 0, itemWidth: 14, textStyle: { fontSize: 12 } },
    tooltip: {
      trigger: 'axis',
      formatter: (ps) => `${ps[0].axisValue}<br>` + ps.map((p) => {
        const c = ['telecom', 'unicom', 'mobile'][p.seriesIndex]
        const loss = lossOf(c, p.dataIndex)
        return `${p.marker}${p.seriesName} ${p.value == null ? '超时' : p.value + ' ms'}${loss ? ` · 丢包 ${loss}%` : ''}`
      }).join('<br>'),
    },
    xAxis: { type: 'category', data: x, axisLabel: { fontSize: 11 } },
    yAxis: { type: 'value', name: 'ms', nameTextStyle: { fontSize: 11 }, axisLabel: { fontSize: 11 }, splitLine: { lineStyle: { color: '#eef0f2' } } },
    series: ['telecom', 'unicom', 'mobile'].map((c) => ({
      name: carrierLabel[c], type: 'line', showSymbol: false, connectNulls: false, lineStyle: { width: 1.5 }, color: bhColors[c],
      data: ks.map((k) => med(buckets.get(k)[c].v)),
    })),
  }
})

watch(id, () => {
  if (!id.value) return
  sv.value = null; xray.value = null; traffic.value = null; configs.value = []; metrics.value = []; tab.value = 'overview'
  backhaul.value = { targets: [], series: {} }
  load()
  loadMetrics()
  loadBackhaul()
  api.get('/releases/xray').then((r) => { releases.value.xray = r.latest_xray; releases.value.agent = r.agent_published }).catch(() => {})
}, { immediate: true })
watch(range, loadMetrics)
watch(tab, (t) => {
  if (t === 'xray' && !xray.value) loadXray()
  if (t === 'traffic' && !traffic.value) loadTraffic()
  if (t === 'configs') loadConfigs()
  if (t === 'asset') fillAsset()
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
  clearTimeout(rollbackTimer)
})

// ---------- header ----------
const sys = computed(() => sv.value?.system || {})
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
const upgrade = ref({ show: false, cap_reality: true, cap_landing: false })
const install = ref({ show: false, command: '', expires_at: '' })
const now = ref(Date.now())
let installTimer = null
watch(() => install.value.show, (active) => {
  if (installTimer) { clearInterval(installTimer); installTimer = null }
  if (active) {
    now.value = Date.now()
    installTimer = setInterval(() => { now.value = Date.now() }, 1000)
  }
})
onBeforeUnmount(() => clearInterval(installTimer))
const installLeft = computed(() => countdown(install.value.expires_at, now.value))
const moreBusy = ref(false)
const upgrading = ref(false)
const rotatingKeys = ref(false)
const deploying = ref(false)
const savingAsset = ref(false)
const rollingBack = ref(0)

async function copyInstall() {
  if (await copy(install.value.command)) ui.message.success('已复制安装命令')
}

async function onMore(k) {
  if (k === 'edit') { tab.value = 'asset'; return }
  if (k === 'upgrade') { upgrade.value.show = true; return }
  if (k === 'restart_agent') { runTool('restart_agent', '重启 Agent'); return }
  if (moreBusy.value) return
  if (k === 'downgrade') {
    moreBusy.value = true
    try {
      if (!(await ask('降级为仅探针', '会停止并卸载这台机器上的 Xray，相关配置会删除。确定继续？', { danger: true }))) return
      if (await run(() => api.post(`/servers/${id.value}/downgrade`), '已降级')) { load(); loadServers() }
    } finally { moreBusy.value = false }
    return
  }
  if (k === 'token') {
    moreBusy.value = true
    try {
      const r = await run(() => api.post(`/servers/${id.value}/token`))
      if (r) {
        now.value = Date.now()
        install.value = { show: true, command: r.command, expires_at: r.install_token_exp || r.expires_at || new Date(Date.now() + 24 * 3600 * 1000).toISOString() }
      }
    } finally { moreBusy.value = false }
    return
  }
  if (k === 'delete') {
    moreBusy.value = true
    try {
      if (!(await ask(`删除 ${sv.value.name}`, '会删除这台服务器的所有监控数据和配置历史，Agent 将被断开。此操作不能撤销。', { danger: true, okText: '删除' }))) return
      if (await run(() => api.del(`/servers/${id.value}`, { confirm: '删除服务器需要确认身份' }), '已删除')) { loadServers(); router.replace({ name: 'overview' }) }
    } finally { moreBusy.value = false }
  }
}
async function doUpgrade() {
  if (upgrading.value) return
  const u = upgrade.value
  upgrading.value = true
  try {
    if (await run(() => api.post(`/servers/${id.value}/upgrade`, { cap_reality: u.cap_reality, cap_landing: u.cap_landing }), '已升级，Xray 将自动安装')) {
      u.show = false; load(); loadServers()
    }
  } finally { upgrading.value = false }
}

// ---------- overview charts ----------
const ranges = [{ label: '1 小时', value: 3600 }, { label: '6 小时', value: 21600 }, { label: '24 小时', value: 86400 }, { label: '7 天', value: 604800 }, { label: '30 天', value: 2592000 }]
const fmtTime = (ts) => { const d = new Date(ts * 1000); return range.value > 86400 ? `${d.getMonth() + 1}-${d.getDate()} ${String(d.getHours()).padStart(2, '0')}:00` : `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}` }
function lineOpt(series, yFmt, max) {
  return {
    grid: { left: 52, right: 12, top: 28, bottom: 26 },
    tooltip: { trigger: 'axis', valueFormatter: yFmt },
    legend: { top: 0, right: 0, itemWidth: 12, itemHeight: 8, textStyle: { fontSize: 11 } },
    xAxis: { type: 'category', data: metrics.value.map((m) => fmtTime(m.TS)), axisLabel: { fontSize: 10, color: '#5f6368' }, axisLine: { lineStyle: { color: '#d5d8dd' } } },
    yAxis: { type: 'value', max, axisLabel: { fontSize: 10, color: '#5f6368', formatter: yFmt }, splitLine: { lineStyle: { color: '#eef0f2' } } },
    series: series.map(([name, key, color, area]) => ({ name, type: 'line', showSymbol: false, smooth: 0.2, lineStyle: { width: 1.5, color }, itemStyle: { color }, areaStyle: area ? { color, opacity: 0.08 } : undefined, data: metrics.value.map(key) })),
  }
}
const cpuOpt = computed(() => lineOpt([['CPU', (m) => +m.CPU.toFixed(1), '#0f7b45', true], ['Steal', (m) => +m.Steal.toFixed(1), '#b8650a']], (v) => v + '%', 100))
const memOpt = computed(() => {
  const t = sys.value.mem_total
  return lineOpt([['内存', (m) => (t ? +(m.MemUsed / t * 100).toFixed(1) : 0), '#2f6fd0', true]], (v) => v + '%', 100)
})
const netOpt = computed(() => lineOpt([['出站', (m) => m.NetOut, '#0f7b45', true], ['入站', (m) => m.NetIn, '#2f6fd0']], (v) => rate(v)))
const connOpt = computed(() => lineOpt([['TCP', (m) => m.TCP, '#0f7b45'], ['UDP', (m) => m.UDP, '#b8650a'], ['Load', (m) => m.Load1, '#5f6368']], (v) => String(v)))
const usage = computed(() => sv.value?.usage || {})
const countModeText = { out: '只计出站', in: '只计入站', both: '双向合计', max: '取出入较大值' }

// ---------- xray ----------
const reality = ref({})
const landing = ref({})
function fillXrayForms(x) {
  if (x.reality) reality.value = { port: x.reality.port, target: x.reality.target, server_names: x.reality.server_names, short_ids: x.reality.short_ids, address: x.reality.address, fingerprint: x.reality.fingerprint || 'chrome', regen_keys: false }
  if (x.landing) landing.value = { port: x.landing.port, method: x.landing.method, address: x.landing.address || '' }
}
const editReality = ref(false)
const editLanding = ref(false)
const fps = ['chrome', 'firefox', 'safari', 'ios', 'edge', 'android', 'random', 'randomized'].map((v) => ({ label: v, value: v }))
const methods = ['2022-blake3-aes-128-gcm', '2022-blake3-aes-256-gcm', '2022-blake3-chacha20-poly1305'].map((v) => ({ label: v, value: v }))
async function saveReality() {
  if (await run(() => api.put(`/servers/${id.value}/reality`, reality.value), '已保存，配置将自动下发')) { editReality.value = false; loadXray() }
}
async function rotateKeys() {
  if (rotatingKeys.value) return
  rotatingKeys.value = true
  try {
    if (!(await ask('轮换 Reality 密钥', '会生成新的 X25519 密钥对，所有使用这台入口的客户端需要刷新订阅。确定继续？'))) return
    const r = xray.value.reality
    if (await run(() => api.put(`/servers/${id.value}/reality`, { port: r.port, target: r.target, server_names: r.server_names, short_ids: r.short_ids, address: r.address, fingerprint: r.fingerprint, regen_keys: true }), '密钥已轮换')) loadXray()
  } finally { rotatingKeys.value = false }
}
async function saveLanding() {
  if (await run(() => api.put(`/servers/${id.value}/landing`, landing.value), '已保存，配置将自动下发')) { editLanding.value = false; loadXray() }
}
async function deploy() {
  if (deploying.value) return
  deploying.value = true
  try {
    const r = await run(() => api.post(`/servers/${id.value}/deploy`))
    if (r) task.value = { show: true, id: r.task_id, title: '下发配置' }
  } finally { deploying.value = false }
}
const preview = ref({ show: false, config: '', firewall: null })
async function showPreview() {
  const r = await run(() => api.get(`/servers/${id.value}/preview-config`))
  if (r) preview.value = { show: true, config: r.config, firewall: r.firewall }
}
const proc = computed(() => sv.value?.latest?.xray)

// ---------- traffic ----------
const dailyOpt = computed(() => {
  const d = traffic.value?.daily || []
  return {
    grid: { left: 56, right: 12, top: 28, bottom: 26 },
    tooltip: { trigger: 'axis', valueFormatter: (v) => bytes(v) },
    legend: { top: 0, right: 0, itemWidth: 12, itemHeight: 8 },
    xAxis: { type: 'category', data: d.map((r) => { const x = new Date(r.hour * 1000); return `${x.getMonth() + 1}-${x.getDate()}` }), axisLabel: { fontSize: 10, color: '#5f6368' } },
    yAxis: { type: 'value', axisLabel: { fontSize: 10, color: '#5f6368', formatter: (v) => bytes(v, 0) }, splitLine: { lineStyle: { color: '#eef0f2' } } },
    series: [
      { name: '出站', type: 'bar', stack: 't', data: d.map((r) => r.up), itemStyle: { color: '#0f7b45' }, barMaxWidth: 18 },
      { name: '入站', type: 'bar', stack: 't', data: d.map((r) => r.down), itemStyle: { color: '#8cc7a6' }, barMaxWidth: 18 },
    ],
  }
})
const dailyTotal = computed(() => (traffic.value?.daily || []).reduce((a, r) => a + r.up + r.down, 0))
const routeTotal = computed(() => (traffic.value?.routes || []).reduce((a, r) => a + r.up + r.down, 0))

// ---------- asset ----------
const asset = ref({})
const assetOrig = ref({})
const xrayOutdated = computed(() => !!(releases.value.xray && sv.value?.xray_version && releases.value.xray.replace(/^v/, '') !== sv.value.xray_version.replace(/^v/, '')))
const assetDirty = (k) => (asset.value[k] ?? null) !== (assetOrig.value[k] ?? null)
const assetDirtyCount = computed(() => Object.keys(asset.value).filter(assetDirty).length)
function fillAsset() {
  const s = sv.value
  if (!s) return
  asset.value = {
    name: s.name, group: s.group, tags: s.tags, region: s.region, country_code: s.country_code, provider: s.provider, provider_url: s.provider_url,
    price: s.price, currency: s.currency || 'USD', pay_cycle: s.pay_cycle || 'month', expire_at: s.expire_at ? new Date(s.expire_at).getTime() : null,
    note: s.note, bandwidth_mbps: s.bandwidth_mbps || 0, reset_day: s.reset_day || 1, quota_gb: s.quota_bytes ? +(s.quota_bytes / 1024 ** 3).toFixed(2) : 0,
    count_mode: s.count_mode || 'out', on_exceeded: s.on_exceeded || 'alert', sort: s.sort, mode: s.mode, cap_reality: s.cap_reality, cap_landing: s.cap_landing,
  }
  assetOrig.value = { ...asset.value }
}
const currencies = ['USD', 'CNY', 'EUR', 'HKD', 'JPY', 'GBP', 'CAD'].map((v) => ({ label: v, value: v }))
const cycles = [{ label: '月付', value: 'month' }, { label: '季付', value: 'quarter' }, { label: '半年付', value: 'half' }, { label: '年付', value: 'year' }, { label: '一次性', value: 'once' }]
const countModes = Object.entries(countModeText).map(([value, label]) => ({ label, value }))
const exceeded = [{ label: '仅告警（80% / 90% / 100%）', value: 'alert' }, { label: '告警并停止 Xray', value: 'stop_xray' }]
const isHttpUrl = (u) => /^https?:\/\//i.test(String(u || '').trim())
async function saveAsset() {
  if (savingAsset.value) return
  const providerUrl = String(asset.value.provider_url || '').trim()
  if (providerUrl && !isHttpUrl(providerUrl)) { ui.message.warning('商家后台链接需以 http:// 或 https:// 开头'); return }
  savingAsset.value = true
  try {
    const a = { ...asset.value, provider_url: providerUrl, expire_at: asset.value.expire_at ? new Date(asset.value.expire_at).toISOString() : null }
    if (await run(() => api.put(`/servers/${id.value}`, a), '已保存')) { await load(); fillAsset(); loadServers() }
  } finally { savingAsset.value = false }
}

// ---------- configs ----------
const cfgView = ref({ show: false, title: '', body: '', diff: false })
async function viewConfig(v) {
  const r = await run(() => api.get(`/servers/${id.value}/configs/${v.id}`))
  if (r) cfgView.value = { show: true, title: `配置 v${v.version}`, body: r.config, diff: false }
}
async function viewDiff(v) {
  const r = await run(() => api.get(`/servers/${id.value}/configs/${v.id}/diff`))
  if (r) cfgView.value = { show: true, title: r.from ? `v${r.from} → v${r.to}` : `v${r.to}（首个版本）`, body: r.diff, diff: true }
}
async function rollback(v) {
  if (rollingBack.value) return
  rollingBack.value = v.id
  try {
    if (!(await ask(`回滚到 v${v.version}`, '会把这个版本重新下发到服务器。若新配置启动失败，Agent 会自动恢复上一份。'))) return
    const r = await run(() => api.post(`/servers/${id.value}/configs/${v.id}/rollback`, {}, { confirm: '回滚配置需要确认身份' }))
    if (r) { task.value = { show: true, id: r.task_id, title: `回滚到 v${v.version}` }; clearTimeout(rollbackTimer); rollbackTimer = setTimeout(loadConfigs, 3000) }
  } finally { rollingBack.value = 0 }
}
function diffClass(line) {
  if (line.startsWith('@@')) return 'diff-hunk'
  if (line.startsWith('+')) return 'diff-add'
  if (line.startsWith('-')) return 'diff-del'
  return ''
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
          <div class="sections">
            <section class="s-group">
              <div class="s-head">
                <h2>监控</h2>
                <p>CPU、内存、网络和连接数，按所选时间范围显示。</p>
              </div>
              <div class="card s-card">
                <div class="s-card-head">
                  <span class="f-hint">时间范围</span>
                  <span class="spacer" />
                  <n-select v-model:value="range" :options="ranges" size="small" style="width: 110px" aria-label="时间范围" />
                </div>
                <div v-if="!metrics.length" class="empty">暂无数据</div>
                <div v-else class="charts">
                  <div><div class="chart-h">CPU</div><chart :option="cpuOpt" height="180px" /></div>
                  <div><div class="chart-h">内存</div><chart :option="memOpt" height="180px" /></div>
                  <div><div class="chart-h">网络</div><chart :option="netOpt" height="180px" /></div>
                  <div><div class="chart-h">连接数 / 负载</div><chart :option="connOpt" height="180px" /></div>
                </div>
              </div>
            </section>

            <section class="s-group">
              <div class="s-head">
                <h2>回程延迟</h2>
                <p>近 24 小时到国内三网的延迟中位数。<router-link :to="{ name: 'latency', query: { server: id } }">查看详细</router-link></p>
                <div v-if="bhOpt && bhLoss.length" class="bh-loss" title="近 24 小时的整体丢包率（总丢包数 / 总发包数）">
                  <span class="muted">24 小时整体丢包</span>
                  <span v-for="x in bhLoss" :key="x.c" class="bh-loss-item"><span class="muted">{{ carrierLabel[x.c] }}</span> <b :class="lossClass(x.v)">{{ lossText(x.v) }}</b></span>
                </div>
              </div>
              <div class="card s-card">
                <div v-if="!bhOpt" class="empty">还没有探测数据<div class="small muted" style="margin-top: 6px">{{ bhEmpty }}</div></div>
                <div v-else class="chart-pad"><chart :option="bhOpt" height="200px" /></div>
              </div>
            </section>

            <section class="s-group">
              <div class="s-head">
                <h2>本周期流量</h2>
                <p>{{ date(usage.cycle_start) }} 至 {{ date(usage.cycle_end) }}，每月 {{ sv.reset_day || 1 }} 日重置。额度和计费口径在「资产」里修改。</p>
              </div>
              <div class="card s-card">
                <div class="usage">
                  <div class="big">{{ bytes(usage.counted) }}<small v-if="sv.quota_bytes"> / {{ bytes(sv.quota_bytes, 0) }}</small><small v-else> · 不限额</small></div>
                  <n-progress v-if="sv.quota_bytes" type="line" :percentage="Math.min(100, usage.percent || 0)" :show-indicator="false" :height="6" :color="usage.percent >= 90 ? '#b4233a' : '#0f7b45'" />
                  <span v-if="sv.quota_bytes" :class="['small', usage.forecast_at ? 'bad-text' : 'muted']">
                    {{ usage.forecast_at ? '按当前速度，预计 ' + date(usage.forecast_at) + ' 用完额度' : '按当前速度，本周期不会超额' }}
                  </span>
                </div>
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">带宽</span></div>
                  <div class="f-ctl"><span class="f-val">{{ mbps(sv.bandwidth_mbps) }}</span></div>
                </div>
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">出站 / 入站</span></div>
                  <div class="f-ctl"><span class="f-val">↑ {{ bytes(usage.up) }} · ↓ {{ bytes(usage.down) }}</span></div>
                </div>
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">计费口径</span></div>
                  <div class="f-ctl"><span class="f-val">{{ countModeText[sv.count_mode] || '只计出站' }}</span></div>
                </div>
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">超额动作</span></div>
                  <div class="f-ctl"><span class="f-val">{{ sv.on_exceeded === 'stop_xray' ? '告警并停止 Xray' : '仅告警（80% / 90% / 100%）' }}</span></div>
                </div>
              </div>
            </section>

            <section class="s-group">
              <div class="s-head">
                <h2>系统信息</h2>
                <p>由 Agent 自动上报。</p>
              </div>
              <div class="card s-card">
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">IPv4</span></div>
                  <div class="f-ctl"><span class="f-val mono">{{ sv.ipv4 || '—' }}</span></div>
                </div>
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">IPv6</span></div>
                  <div class="f-ctl"><span class="f-val mono">{{ sv.ipv6 || '—' }}</span></div>
                </div>
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">系统</span><span v-if="sys.kernel" class="f-hint">内核 {{ sys.kernel }}</span></div>
                  <div class="f-ctl"><span class="f-val">{{ sys.os || '—' }}</span></div>
                </div>
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">配置</span><span v-if="sys.cpu_model" class="f-hint">{{ sys.cpu_model }}</span></div>
                  <div class="f-ctl"><span class="f-val">{{ sys.cpu_cores ? sys.cpu_cores + ' vCPU · ' + bytes(sys.mem_total, 0) + ' · ' + bytes(sys.disk_total, 0) : '—' }}</span></div>
                </div>
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">虚拟化</span></div>
                  <div class="f-ctl"><span class="f-val">{{ [sys.virt, sys.arch].filter(Boolean).join(' · ') || '—' }}</span></div>
                </div>
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">磁盘</span></div>
                  <div class="f-ctl"><span class="f-val">{{ sys.disk_total ? bytes(sv.latest?.disk_used) + ' / ' + bytes(sys.disk_total, 0) : '—' }}</span></div>
                </div>
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">连接数</span></div>
                  <div class="f-ctl"><span class="f-val">TCP {{ sv.latest?.tcp ?? '—' }} · UDP {{ sv.latest?.udp ?? '—' }}</span></div>
                </div>
              </div>
            </section>

            <section class="s-group">
              <div class="s-head">
                <h2>网络优化</h2>
                <p>TCP 拥塞控制与队列算法。调整在「工具 → 网络优化」里进行。</p>
              </div>
              <div class="card s-card">
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">拥塞控制</span></div>
                  <div class="f-ctl"><span :class="['tag', sys.congestion === 'bbr' ? 'ok' : 'warn']">{{ sys.congestion === 'bbr' ? 'BBR 已开启' : (sys.congestion || '未知') }}</span></div>
                </div>
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">队列</span></div>
                  <div class="f-ctl"><span class="f-val mono">{{ sys.qdisc || '—' }}</span></div>
                </div>
                <div v-if="!sv.switches?.allow_system_tune" class="banner warn">这台机器的 Agent 关闭了系统调优（本地配置 allow_system_tune），不能在面板里调整。</div>
                <div class="f-actions">
                  <button v-if="sv.switches?.allow_system_tune" class="link-btn small" :disabled="!sv.online" @click="runTool('restore_net', '恢复网络参数')">恢复调优前的参数</button>
                  <span class="spacer" />
                  <n-button :disabled="!sv.online || !sv.switches?.allow_system_tune" @click="router.push({ name: 'tools', query: { server: sv.id, tab: 'bbr' } })">调整</n-button>
                </div>
              </div>
            </section>

            <section class="s-group">
              <div class="s-head">
                <h2>版本</h2>
                <p>批量更新在<router-link :to="{ name: 'updates' }">更新中心</router-link>。</p>
              </div>
              <div class="card s-card">
                <div class="field f-inline">
                  <div class="f-label">
                    <span class="f-name">Agent</span>
                    <span v-if="releases.agent && sv.agent_version && releases.agent !== sv.agent_version" class="f-hint">最新 {{ releases.agent }}</span>
                  </div>
                  <div class="f-ctl ver">
                    <span class="f-val mono">{{ sv.agent_version || '—' }}</span>
                    <span v-if="releases.agent && sv.agent_version && releases.agent !== sv.agent_version" class="tag warn">可更新</span>
                  </div>
                </div>
                <div v-if="sv.mode === 'node'" class="field f-inline">
                  <div class="f-label">
                    <span class="f-name">Xray</span>
                    <span v-if="sv.xray_locked_version" class="f-hint">已锁定在 {{ sv.xray_locked_version }}，批量更新会跳过</span>
                    <span v-else-if="xrayOutdated" class="f-hint">最新 {{ releases.xray }}</span>
                  </div>
                  <div class="f-ctl ver">
                    <span class="f-val mono">{{ sv.xray_version || '—' }}</span>
                    <span v-if="sv.xray_locked_version" class="tag">锁定</span>
                    <span v-else-if="xrayOutdated" class="tag warn">可更新</span>
                  </div>
                </div>
              </div>
            </section>
          </div>
        </n-tab-pane>

        <n-tab-pane v-if="sv.mode === 'node'" name="xray" tab="Xray">
          <div v-if="!xray" class="empty">加载中…</div>
          <div v-else class="sections">
            <section class="s-group">
              <div class="s-head">
                <h2>运行状态</h2>
                <p>Xray 配置由面板生成。改了线路或入站后会自动下发；本地配置被改动时可以手动重新下发。</p>
              </div>
              <div class="card s-card">
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">Xray</span><span v-if="sv.xray_version" class="f-hint">版本 {{ sv.xray_version }}</span></div>
                  <div class="f-ctl"><span :class="['tag', sv.xray_running ? 'ok' : 'bad']"><span :class="['dot', sv.xray_running ? 'ok' : 'bad']" />{{ sv.xray_running ? '运行中' : '未运行' }}</span></div>
                </div>
                <div v-if="proc" class="field f-inline">
                  <div class="f-label"><span class="f-name">进程</span></div>
                  <div class="f-ctl"><span class="f-val">内存 {{ bytes(proc.mem) }} · 已运行 {{ dur(proc.uptime) }}</span></div>
                </div>
                <div class="field f-inline">
                  <div class="f-label">
                    <span class="f-name">配置</span>
                    <span class="f-hint">{{ sv.xray_drift ? '服务器上的配置被改过，重新下发会覆盖' : '与面板一致' }}</span>
                  </div>
                  <div class="f-ctl"><span :class="['tag', sv.xray_drift ? 'warn' : 'ok']">{{ sv.xray_drift ? '不一致' : 'v' + sv.xray_config_ver }}</span></div>
                </div>
                <div class="f-actions">
                  <n-button @click="runTool('xray_log', 'Xray 日志')">查看日志</n-button>
                  <n-button @click="showPreview">预览配置</n-button>
                  <span class="spacer" />
                  <n-button type="primary" :loading="deploying" :disabled="!sv.online || deploying" @click="deploy">重新下发</n-button>
                </div>
              </div>
            </section>

            <section v-if="xray.reality" class="s-group">
              <div class="s-head">
                <h2>Reality 入站</h2>
                <p>客户端直接连接的入站（VLESS · TCP · xtls-rprx-vision）。保存后会重新下发，已导入的订阅需要刷新。</p>
              </div>
              <div class="card s-card">
                <template v-if="!editReality">
                  <div class="field f-inline">
                    <div class="f-label"><span class="f-name">端口</span></div>
                    <div class="f-ctl"><span class="f-val mono">{{ xray.reality.port }}</span></div>
                  </div>
                  <div class="field f-inline">
                    <div class="f-label"><span class="f-name">目标站</span></div>
                    <div class="f-ctl"><span class="f-val mono">{{ xray.reality.target }}</span></div>
                  </div>
                  <div class="field f-inline">
                    <div class="f-label"><span class="f-name">SNI</span></div>
                    <div class="f-ctl"><span class="f-val mono">{{ xray.reality.server_names }}</span></div>
                  </div>
                  <div class="field f-inline">
                    <div class="f-label"><span class="f-name">shortIds</span></div>
                    <div class="f-ctl"><span class="f-val">{{ (xray.reality.short_ids || '').split(',').filter(Boolean).length }} 个</span></div>
                  </div>
                  <div class="field f-inline">
                    <div class="f-label"><span class="f-name">客户端连接地址</span><span v-if="!xray.reality.address" class="f-hint">未单独设置，使用 IPv4</span></div>
                    <div class="f-ctl"><span class="f-val mono">{{ xray.reality.address || sv.ipv4 || '—' }}</span></div>
                  </div>
                  <div class="field f-inline">
                    <div class="f-label"><span class="f-name">指纹</span></div>
                    <div class="f-ctl"><span class="f-val">{{ xray.reality.fingerprint }}</span></div>
                  </div>
                  <div class="field stacked">
                    <div class="f-label"><span class="f-name">公钥</span><span class="f-hint">轮换密钥后，使用这台入口的客户端需要刷新订阅</span></div>
                    <div class="f-ctl"><span class="f-val mono key">{{ xray.reality.public_key }}</span></div>
                  </div>
                  <div class="f-actions">
                    <n-button :loading="rotatingKeys" :disabled="rotatingKeys" @click="rotateKeys">轮换密钥</n-button>
                    <span class="spacer" />
                    <n-button type="primary" @click="editReality = true">编辑</n-button>
                  </div>
                </template>
                <template v-else>
                  <div class="field">
                    <div class="f-label"><span class="f-name">端口</span></div>
                    <div class="f-ctl"><n-input-number v-model:value="reality.port" :min="1" :max="65535" :show-button="false" /></div>
                  </div>
                  <div class="field">
                    <div class="f-label"><span class="f-name">目标站</span><span class="f-hint">host:port。可在「工具 → Reality 目标扫描」里挑选：需支持 TLS 1.3 与 H2、握手快</span></div>
                    <div class="f-ctl"><n-input v-model:value="reality.target" placeholder="www.example.com:443" /></div>
                  </div>
                  <div class="field">
                    <div class="f-label"><span class="f-name">SNI</span><span class="f-hint">逗号分隔</span></div>
                    <div class="f-ctl"><n-input v-model:value="reality.server_names" /></div>
                  </div>
                  <div class="field">
                    <div class="f-label"><span class="f-name">shortIds</span><span class="f-hint">逗号分隔，留空自动生成</span></div>
                    <div class="f-ctl"><n-input v-model:value="reality.short_ids" placeholder="自动生成" /></div>
                  </div>
                  <div class="field">
                    <div class="f-label"><span class="f-name">客户端连接地址</span><span class="f-hint">订阅里给客户端用的地址，留空用 IPv4</span></div>
                    <div class="f-ctl"><n-input v-model:value="reality.address" placeholder="默认 IPv4" clearable /></div>
                  </div>
                  <div class="field">
                    <div class="f-label"><span class="f-name">指纹</span></div>
                    <div class="f-ctl"><n-select v-model:value="reality.fingerprint" :options="fps" /></div>
                  </div>
                  <div class="f-actions"><span class="spacer" /><n-button @click="editReality = false">取消</n-button><n-button type="primary" @click="saveReality">保存并下发</n-button></div>
                </template>
              </div>
            </section>

            <section v-if="xray.landing" class="s-group">
              <div class="s-head">
                <h2>SS2022 落地</h2>
                <p>只接收入口机转发来的流量，防火墙只放行入口机 IP。</p>
              </div>
              <div class="card s-card">
                <template v-if="!editLanding">
                  <div class="field f-inline">
                    <div class="f-label"><span class="f-name">端口</span></div>
                    <div class="f-ctl"><span class="f-val mono">{{ xray.landing.port }}</span></div>
                  </div>
                  <div class="field f-inline">
                    <div class="f-label"><span class="f-name">加密方式</span></div>
                    <div class="f-ctl"><span class="f-val mono">{{ xray.landing.method }}</span></div>
                  </div>
                  <div class="field f-inline">
                    <div class="f-label"><span class="f-name">连接地址</span><span v-if="!xray.landing.address" class="f-hint">未单独设置，入口机连公网 IP</span></div>
                    <div class="f-ctl"><span class="f-val mono">{{ xray.landing.address || sv.ipv4 || '公网 IP' }}</span></div>
                  </div>
                  <div class="f-actions">
                    <span class="spacer" />
                    <n-button type="primary" @click="editLanding = true">编辑</n-button>
                  </div>
                </template>
                <template v-else>
                  <div class="field">
                    <div class="f-label"><span class="f-name">端口</span><span class="f-hint">1024–65535</span></div>
                    <div class="f-ctl"><n-input-number v-model:value="landing.port" :min="1024" :max="65535" :show-button="false" /></div>
                  </div>
                  <div class="field">
                    <div class="f-label"><span class="f-name">加密方式</span></div>
                    <div class="f-ctl"><n-select v-model:value="landing.method" :options="methods" /></div>
                  </div>
                  <div class="field">
                    <div class="f-label"><span class="f-name">连接地址</span><span class="f-hint">入口和落地在同一内网、或落地是 NAT 机端口映射时填写。填内网 IP 时，落地防火墙会同时放行内网网段</span></div>
                    <div class="f-ctl"><n-input v-model:value="landing.address" placeholder="默认 公网 IP" clearable /></div>
                  </div>
                  <div class="f-actions"><span class="spacer" /><n-button @click="editLanding = false">取消</n-button><n-button type="primary" @click="saveLanding">保存并下发</n-button></div>
                </template>
              </div>
            </section>

            <section v-if="xray.landing" class="s-group">
              <div class="s-head">
                <h2>入口用户</h2>
                <p>连到这台落地的入口机，以及它们本月的流量。</p>
              </div>
              <div class="card s-card">
                <div v-if="!xray.landing_users?.length" class="empty">还没有入口机连到这台落地</div>
                <div v-else class="table-wrap">
                  <table class="plain">
                    <thead><tr><th>入口</th><th>上行</th><th>下行</th></tr></thead>
                    <tbody><tr v-for="u in xray.landing_users" :key="u.id"><td>{{ u.entry_name }}</td><td class="mono">{{ bytes(u.month_up) }}</td><td class="mono">{{ bytes(u.month_down) }}</td></tr></tbody>
                  </table>
                </div>
              </div>
            </section>

            <section class="s-group">
              <div class="s-head">
                <h2>本机线路</h2>
                <p>{{ myRoutes.length ? `${myRoutes.length} 条线路经过这台机器。` : '还没有线路经过这台机器。' }}线路在「线路」页统一管理。</p>
              </div>
              <div class="card s-card">
                <div v-if="sv.cap_reality" class="s-card-head">
                  <span class="f-hint">以这台为入口新建直连或链式线路</span>
                  <span class="spacer" />
                  <n-button size="small" type="primary" @click="router.push({ name: 'route-new', query: { entry: sv.id } })">新建线路</n-button>
                </div>
                <div v-if="!myRoutes.length" class="empty">没有线路经过这台机器</div>
                <div class="table-wrap" v-else>
                  <table class="plain">
                    <thead><tr><th>类型</th><th>名称</th><th>延迟</th><th>今日</th><th>状态</th></tr></thead>
                    <tbody>
                      <tr v-for="r in myRoutes" :key="r.id">
                        <td><span class="tag">{{ r.type === 'direct' ? '直连' : '链式' }}</span></td>
                        <td>{{ r.name }}<div class="muted small">{{ r.type === 'chain' ? `${r.entry_name} → ${r.landing_name}` : '' }}</div></td>
                        <td class="mono small">{{ r.type === 'chain' && r.segment_ms ? '线路段 ' + Math.round(r.segment_ms) + ' ms' : r.latency_ms ? Math.round(r.latency_ms) + ' ms' : '—' }}</td>
                        <td class="mono">{{ bytes(r.today_bytes) }}</td>
                        <td><span :class="['tag', { ok: 'ok', degraded: 'warn', down: 'bad' }[r.status]]">{{ { ok: '正常', degraded: '降级', down: '中断', disabled: '已停用' }[r.status] || r.status }}</span></td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </div>
            </section>
          </div>
        </n-tab-pane>

        <n-tab-pane name="traffic" tab="流量">
          <div v-if="!traffic" class="empty">加载中…</div>
          <div v-else class="sections">
            <section class="s-group">
              <div class="s-head">
                <h2>近 30 天</h2>
                <p>网卡统计的每日出站与入站，包含所有流量。</p>
              </div>
              <div class="card s-card">
                <div class="s-card-head">
                  <span class="f-hint">合计</span>
                  <b class="mono">{{ bytes(dailyTotal) }}</b>
                </div>
                <div v-if="!traffic.daily?.length" class="empty">还没有流量数据</div>
                <div v-else class="chart-pad"><chart :option="dailyOpt" height="260px" /></div>
              </div>
            </section>

            <section class="s-group">
              <div class="s-head">
                <h2>本周期按线路</h2>
                <p>{{ date(usage.cycle_start) }} 至 {{ date(usage.cycle_end) }}。代理流量按线路统计，剩下的是系统、探针等非代理流量。</p>
              </div>
              <div class="card s-card">
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">本周期合计</span><span class="f-hint">出站 + 入站</span></div>
                  <div class="f-ctl"><span class="f-val mono">{{ bytes(traffic.usage.up + traffic.usage.down) }}</span></div>
                </div>
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">代理流量</span><span class="f-hint">{{ traffic.routes?.length || 0 }} 条线路</span></div>
                  <div class="f-ctl"><span class="f-val mono">{{ bytes(routeTotal) }}</span></div>
                </div>
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">非代理流量</span><span class="f-hint">系统、探针等</span></div>
                  <div class="f-ctl"><span class="f-val mono">{{ bytes(Math.max(0, (traffic.usage.up + traffic.usage.down) - routeTotal)) }}</span></div>
                </div>
                <div v-if="traffic.routes?.length" class="table-wrap route-table">
                  <table class="plain">
                    <thead><tr><th>线路</th><th>上行</th><th>下行</th><th>合计</th></tr></thead>
                    <tbody>
                      <tr v-for="r in traffic.routes" :key="r.id"><td>{{ r.name }}</td><td class="mono">{{ bytes(r.up) }}</td><td class="mono">{{ bytes(r.down) }}</td><td class="mono">{{ bytes(r.up + r.down) }}</td></tr>
                    </tbody>
                  </table>
                </div>
              </div>
            </section>
          </div>
        </n-tab-pane>

        <n-tab-pane name="asset" tab="资产">
          <div class="sections">
            <section class="s-group">
              <div class="s-head">
                <h2>基本信息</h2>
                <p>显示名称与分类。总览可以按分组筛选。创建于 {{ dt(sv.created_at, true) }}。</p>
              </div>
              <div class="card s-card">
                <div class="field">
                  <div class="f-label"><span class="f-name">名称<i v-if="assetDirty('name')" class="f-dot" /></span></div>
                  <div class="f-ctl"><n-input v-model:value="asset.name" placeholder="服务器名称" /></div>
                </div>
                <div class="field">
                  <div class="f-label"><span class="f-name">分组<i v-if="assetDirty('group')" class="f-dot" /></span></div>
                  <div class="f-ctl"><n-input v-model:value="asset.group" placeholder="未分组" clearable /></div>
                </div>
                <div class="field">
                  <div class="f-label"><span class="f-name">标签<i v-if="assetDirty('tags')" class="f-dot" /></span><span class="f-hint">逗号分隔，可在总览里搜索</span></div>
                  <div class="f-ctl"><n-input v-model:value="asset.tags" placeholder="未设置" clearable /></div>
                </div>
                <div class="field">
                  <div class="f-label"><span class="f-name">排序<i v-if="assetDirty('sort')" class="f-dot" /></span><span class="f-hint">数字小的排在前面</span></div>
                  <div class="f-ctl"><n-input-number v-model:value="asset.sort" :show-button="false" placeholder="默认 0" /></div>
                </div>
                <div class="field">
                  <div class="f-label"><span class="f-name">地区<i v-if="assetDirty('region')" class="f-dot" /></span></div>
                  <div class="f-ctl"><n-input v-model:value="asset.region" placeholder="自动识别" clearable /></div>
                </div>
                <div class="field">
                  <div class="f-label"><span class="f-name">国家代码<i v-if="assetDirty('country_code')" class="f-dot" /></span><span class="f-hint">两位字母，用于显示国旗</span></div>
                  <div class="f-ctl"><n-input v-model:value="asset.country_code" maxlength="2" placeholder="例如 HK" /></div>
                </div>
                <div class="field stacked">
                  <div class="f-label"><span class="f-name">备注<i v-if="assetDirty('note')" class="f-dot" /></span></div>
                  <div class="f-ctl"><n-input v-model:value="asset.note" type="textarea" :autosize="{ minRows: 2, maxRows: 6 }" placeholder="未设置" /></div>
                </div>
              </div>
            </section>

            <section class="s-group">
              <div class="s-head">
                <h2>服务商与费用</h2>
                <p>用于总览里的月度花费和到期提醒。</p>
              </div>
              <div class="card s-card">
                <div class="field">
                  <div class="f-label"><span class="f-name">服务商<i v-if="assetDirty('provider')" class="f-dot" /></span></div>
                  <div class="f-ctl"><n-input v-model:value="asset.provider" placeholder="未设置" clearable /></div>
                </div>
                <div class="field">
                  <div class="f-label">
                    <span class="f-name">商家后台链接<i v-if="assetDirty('provider_url')" class="f-dot" /></span>
                    <span v-if="sv.provider_url" class="f-hint">
                      <a v-if="isHttpUrl(sv.provider_url)" :href="sv.provider_url.trim()" target="_blank" rel="noopener noreferrer">打开商家后台</a>
                      <template v-else>{{ sv.provider_url }}</template>
                    </span>
                  </div>
                  <div class="f-ctl"><n-input v-model:value="asset.provider_url" placeholder="https://" clearable /></div>
                </div>
                <div class="field">
                  <div class="f-label"><span class="f-name">价格<i v-if="assetDirty('price') || assetDirty('currency')" class="f-dot" /></span></div>
                  <div class="f-ctl ctl-row">
                    <n-input-number v-model:value="asset.price" :min="0" :precision="2" :show-button="false" placeholder="未设置" />
                    <n-select v-model:value="asset.currency" :options="currencies" tag filterable aria-label="币种" class="cur" />
                  </div>
                </div>
                <div class="field">
                  <div class="f-label"><span class="f-name">付费周期<i v-if="assetDirty('pay_cycle')" class="f-dot" /></span></div>
                  <div class="f-ctl"><n-select v-model:value="asset.pay_cycle" :options="cycles" /></div>
                </div>
                <div class="field">
                  <div class="f-label">
                    <span class="f-name">到期日<i v-if="assetDirty('expire_at')" class="f-dot" /></span>
                    <span v-if="daysUntil(sv.expire_at) !== null" class="f-hint">{{ daysUntil(sv.expire_at) }} 天后到期</span>
                  </div>
                  <div class="f-ctl"><n-date-picker v-model:value="asset.expire_at" type="date" clearable placeholder="未设置" /></div>
                </div>
              </div>
            </section>

            <section class="s-group">
              <div class="s-head">
                <h2>流量</h2>
                <p>带宽、本周期流量的计算方式，以及超出额度后的动作。</p>
              </div>
              <div class="card s-card">
                <div class="field">
                  <div class="f-label"><span class="f-name">带宽<i v-if="assetDirty('bandwidth_mbps')" class="f-dot" /></span><span class="f-hint">商家标称端口速率，0 = 未设置</span></div>
                  <div class="f-ctl">
                    <n-input-number v-model:value="asset.bandwidth_mbps" :min="0" :precision="0" :show-button="false" placeholder="0">
                      <template #suffix><span class="f-unit">Mbps</span></template>
                    </n-input-number>
                  </div>
                </div>
                <div class="field">
                  <div class="f-label"><span class="f-name">流量额度<i v-if="assetDirty('quota_gb')" class="f-dot" /></span><span class="f-hint">0 = 不限</span></div>
                  <div class="f-ctl">
                    <n-input-number v-model:value="asset.quota_gb" :min="0" :show-button="false" placeholder="0">
                      <template #suffix><span class="f-unit">GB</span></template>
                    </n-input-number>
                  </div>
                </div>
                <div class="field">
                  <div class="f-label"><span class="f-name">每月重置日<i v-if="assetDirty('reset_day')" class="f-dot" /></span></div>
                  <div class="f-ctl">
                    <n-input-number v-model:value="asset.reset_day" :min="1" :max="31" :show-button="false" placeholder="默认 1">
                      <template #suffix><span class="f-unit">日</span></template>
                    </n-input-number>
                  </div>
                </div>
                <div class="field">
                  <div class="f-label"><span class="f-name">计费口径<i v-if="assetDirty('count_mode')" class="f-dot" /></span></div>
                  <div class="f-ctl"><n-select v-model:value="asset.count_mode" :options="countModes" /></div>
                </div>
                <div class="field">
                  <div class="f-label"><span class="f-name">超额动作<i v-if="assetDirty('on_exceeded')" class="f-dot" /></span></div>
                  <div class="f-ctl"><n-select v-model:value="asset.on_exceeded" :options="exceeded" /></div>
                </div>
              </div>
            </section>
          </div>
          <div class="savebar">
            <span v-if="assetDirtyCount" class="small dirty-note"><i class="f-dot" />{{ assetDirtyCount }} 项未保存</span>
            <span v-else class="small muted">修改后点保存</span>
            <span class="spacer" />
            <n-button :disabled="!assetDirtyCount" @click="fillAsset">撤销</n-button>
            <n-button type="primary" :loading="savingAsset" :disabled="!assetDirtyCount || savingAsset" @click="saveAsset">保存</n-button>
          </div>
        </n-tab-pane>

        <n-tab-pane v-if="sv.mode === 'node'" name="configs" tab="配置历史">
          <div class="card">
            <div v-if="!configs.length" class="empty">还没有下发过配置</div>
            <div class="table-wrap" v-else>
              <table class="plain">
                <thead><tr><th>版本</th><th>时间</th><th>原因</th><th>状态</th><th /></tr></thead>
                <tbody>
                  <tr v-for="v in configs" :key="v.id">
                    <td class="mono">v{{ v.version }}</td>
                    <td class="nowrap">{{ dt(v.created_at) }}</td>
                    <td>{{ v.reason || '—' }}</td>
                    <td><span v-if="v.version === sv.xray_config_ver" class="tag ok">当前</span><span v-else-if="v.applied" class="tag">已应用</span><span v-else class="tag warn">未应用</span></td>
                    <td class="nowrap" style="text-align: right">
                      <n-button size="tiny" quaternary @click="viewConfig(v)">查看</n-button>
                      <n-button size="tiny" quaternary @click="viewDiff(v)">差异</n-button>
                      <n-button v-if="v.version !== sv.xray_config_ver" size="tiny" quaternary type="warning" :loading="rollingBack === v.id" :disabled="!!rollingBack" @click="rollback(v)">回滚到此</n-button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </n-tab-pane>
      </n-tabs>
    </template>
    <div v-else class="empty">加载中…</div>

    <task-modal v-model:show="task.show" :task-id="task.id" :title="task.title" @done="load" />

    <n-modal v-model:show="upgrade.show" preset="card" title="升级为节点" style="width: min(460px, 94vw)">
      <div class="m-form">
        <p class="f-note first">会在这台机器上安装 Xray，并由面板生成配置。至少选择一种能力。</p>
        <div class="field f-bool">
          <div class="f-label"><span class="f-name">Reality 入站</span><span class="f-hint">直连或入口：客户端连接的节点，默认端口 443</span></div>
          <div class="f-ctl"><n-switch v-model:value="upgrade.cap_reality" aria-label="Reality 入站" /></div>
        </div>
        <div class="field f-bool">
          <div class="f-label"><span class="f-name">SS2022 落地</span><span class="f-hint">只接收入口机流量，端口随机，防火墙只放行入口机 IP</span></div>
          <div class="f-ctl"><n-switch v-model:value="upgrade.cap_landing" aria-label="SS2022 落地" /></div>
        </div>
      </div>
      <template #footer><div class="m-foot"><n-button @click="upgrade.show = false">取消</n-button><n-button type="primary" :loading="upgrading" :disabled="(!upgrade.cap_reality && !upgrade.cap_landing) || upgrading" @click="doUpgrade">升级</n-button></div></template>
    </n-modal>

    <n-modal v-model:show="install.show" preset="card" title="安装命令" style="width: min(640px, 96vw)">
      <p class="small" style="margin-top: 0">用 root 登录 VPS 执行（{{ installLeft }} 后失效，只能使用一次）：</p>
      <pre class="pre" style="user-select: all">{{ install.command }}</pre>
      <template #footer>
        <div class="m-foot">
          <n-button @click="install.show = false">关闭</n-button>
          <n-button type="primary" @click="copyInstall">复制命令</n-button>
        </div>
      </template>
    </n-modal>

    <n-modal v-model:show="cfgView.show" preset="card" :title="cfgView.title" style="width: min(900px, 96vw)">
      <pre v-if="!cfgView.diff" class="pre light" style="max-height: 70vh">{{ cfgView.body }}</pre>
      <pre v-else class="pre light" style="max-height: 70vh"><span v-for="(l, i) in cfgView.body.split('\n')" :key="i" :class="diffClass(l)">{{ l || ' ' }}</span></pre>
      <p class="small muted">密钥字段已隐藏。</p>
    </n-modal>

    <n-modal v-model:show="preview.show" preset="card" title="配置预览（未下发）" style="width: min(900px, 96vw)">
      <pre class="pre light" style="max-height: 60vh">{{ preview.config }}</pre>
      <template v-if="preview.firewall">
        <h3 class="sub-h">防火墙规则</h3>
        <pre class="pre light">{{ typeof preview.firewall === 'string' ? preview.firewall : JSON.stringify(preview.firewall, null, 2) }}</pre>
      </template>
    </n-modal>
  </div>
</template>

<style scoped>
.crumb a { color: var(--c-muted); }
.head .flag { font-size: 34px; }
.title h1 { font-size: 22px; }
.chart-pad { padding: 12px 0; }
.usage { display: flex; flex-direction: column; gap: 8px; padding: 16px 0 14px; }
.f-val { font-size: 13.5px; text-align: right; overflow-wrap: anywhere; }
.field.stacked .f-val { text-align: left; }
.f-val.key { font-size: 12.5px; word-break: break-all; }
.route-table { border-top: 1px solid var(--c-border); }
.f-ctl.ver { display: flex; align-items: center; gap: 8px; }
.s-card > .charts { padding: 4px 0 16px; }
.s-card > .banner { margin: 4px 0 0; }
.charts { display: grid; grid-template-columns: 1fr 1fr; gap: 12px 20px; }
.chart-h { font-size: 12px; color: var(--c-muted); }
.big { font-size: 26px; font-weight: 700; font-variant-numeric: tabular-nums; }
.big small { font-size: 15px; color: var(--c-muted); font-weight: 500; }
.bad-text { color: var(--c-danger); }
.warn-text { color: var(--c-warning); }
.bh-loss { display: flex; flex-wrap: wrap; align-items: baseline; gap: 2px 12px; font-size: 13px; }
.bh-loss > .muted:first-child { flex-basis: 100%; }
.bh-loss b { font-weight: 600; font-variant-numeric: tabular-nums; }
.bh-loss b.muted { font-weight: 500; }
.ctl-row .cur { flex: none; width: 96px; }
.form-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 0 12px; }
.sub-h { font-size: 14px; margin: 18px 0 8px; }
p { margin: 6px 0; }
@media (max-width: 900px) {
  .charts { grid-template-columns: 1fr; }
}
@media (max-width: 767px) {
  .head .flag { font-size: 26px; }
  .head .title { flex: 1 1 calc(100% - 50px); }
  .form-2 { grid-template-columns: 1fr; }
}
</style>
