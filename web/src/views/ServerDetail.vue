<script setup>
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NTabs, NTabPane, NSelect, NInput, NInputNumber, NForm, NFormItem, NDatePicker, NCheckbox, NDropdown, NModal, NSwitch, NProgress } from 'naive-ui'
import { api } from '../api'
import { state, loadServers, on, ui } from '../store'
import { run, ask } from '../helpers'
import { bytes, rate, pct, ago, dt, date, daysUntil, flag, dur } from '../format'
import { isMobile } from '../mobile'
import Chart from '../components/Chart.vue'
import TaskModal from '../components/TaskModal.vue'

const route = useRoute()
const router = useRouter()
const id = computed(() => Number(route.params.id))
const sv = ref(null)
const tab = ref('overview')
const metrics = ref([])
const range = ref(3600)
const xray = ref(null)
const myRoutes = ref([])
const traffic = ref(null)
const configs = ref([])
const releases = ref({ agent: '', xray: '' })
const task = ref({ show: false, id: null, title: '' })

async function load() {
  const r = await run(() => api.get(`/servers/${id.value}`))
  if (r) sv.value = r
}
async function loadMetrics() { metrics.value = (await run(() => api.get(`/servers/${id.value}/metrics?range=${range.value}`))) || [] }
async function loadXray() {
  if (sv.value?.mode !== 'node') return
  const [x, rs] = await Promise.all([run(() => api.get(`/servers/${id.value}/xray`)), run(() => api.get('/routes'))])
  if (x) { xray.value = x; fillXrayForms(x) }
  myRoutes.value = (rs || []).filter((r) => r.entry_server_id === id.value || r.landing_server_id === id.value)
}
async function loadTraffic() { traffic.value = await run(() => api.get(`/servers/${id.value}/traffic?days=30`)) }
async function loadConfigs() { configs.value = (await run(() => api.get(`/servers/${id.value}/configs`))) || [] }

watch(id, async () => {
  if (!id.value) return
  sv.value = null; xray.value = null; traffic.value = null; configs.value = []; tab.value = 'overview'
  await load()
  loadMetrics()
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
  }
})
const offS = on('server', (d) => { if (d.id === id.value) load() })
const offX = on('xray', (d) => { if (d.server_id === id.value) { load(); if (xray.value) loadXray() } })
onBeforeUnmount(() => { offM(); offS(); offX() })

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
const install = ref({ show: false, command: '' })
async function onMore(k) {
  if (k === 'edit') { tab.value = 'asset'; return }
  if (k === 'upgrade') { upgrade.value.show = true; return }
  if (k === 'downgrade') {
    if (!(await ask('降级为仅探针', '会停止并卸载这台机器上的 Xray，相关配置会删除。确定继续？', { danger: true }))) return
    if (await run(() => api.post(`/servers/${id.value}/downgrade`), '已降级')) { load(); loadServers() }
    return
  }
  if (k === 'token') {
    const r = await run(() => api.post(`/servers/${id.value}/token`))
    if (r) install.value = { show: true, command: r.command }
    return
  }
  if (k === 'restart_agent') { runTool('restart_agent', '重启 Agent'); return }
  if (k === 'delete') {
    if (!(await ask(`删除 ${sv.value.name}`, '会删除这台服务器的所有监控数据和配置历史，Agent 将被断开。此操作不能撤销。', { danger: true, okText: '删除' }))) return
    if (await run(() => api.del(`/servers/${id.value}`), '已删除')) { loadServers(); router.replace({ name: 'overview' }) }
  }
}
async function doUpgrade() {
  const u = upgrade.value
  if (await run(() => api.post(`/servers/${id.value}/upgrade`, { cap_reality: u.cap_reality, cap_landing: u.cap_landing }), '已升级，Xray 将自动安装')) {
    u.show = false; load(); loadServers()
  }
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
  if (x.landing) landing.value = { port: x.landing.port, method: x.landing.method }
}
const editReality = ref(false)
const editLanding = ref(false)
const fps = ['chrome', 'firefox', 'safari', 'ios', 'edge', 'android', 'random', 'randomized'].map((v) => ({ label: v, value: v }))
const methods = ['2022-blake3-aes-128-gcm', '2022-blake3-aes-256-gcm', '2022-blake3-chacha20-poly1305'].map((v) => ({ label: v, value: v }))
async function saveReality() {
  if (await run(() => api.put(`/servers/${id.value}/reality`, reality.value), '已保存，配置将自动下发')) { editReality.value = false; loadXray() }
}
async function rotateKeys() {
  if (!(await ask('轮换 Reality 密钥', '会生成新的 X25519 密钥对，所有使用这台入口的客户端需要刷新订阅。确定继续？'))) return
  const r = xray.value.reality
  if (await run(() => api.put(`/servers/${id.value}/reality`, { port: r.port, target: r.target, server_names: r.server_names, short_ids: r.short_ids, address: r.address, fingerprint: r.fingerprint, regen_keys: true }), '密钥已轮换')) loadXray()
}
async function saveLanding() {
  if (await run(() => api.put(`/servers/${id.value}/landing`, landing.value), '已保存，配置将自动下发')) { editLanding.value = false; loadXray() }
}
async function deploy() {
  const r = await run(() => api.post(`/servers/${id.value}/deploy`))
  if (r) task.value = { show: true, id: r.task_id, title: '下发配置' }
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
function fillAsset() {
  const s = sv.value
  if (!s) return
  asset.value = {
    name: s.name, group: s.group, tags: s.tags, region: s.region, country_code: s.country_code, provider: s.provider, provider_url: s.provider_url,
    price: s.price, currency: s.currency || 'USD', pay_cycle: s.pay_cycle || 'month', expire_at: s.expire_at ? new Date(s.expire_at).getTime() : null,
    note: s.note, reset_day: s.reset_day || 1, quota_gb: s.quota_bytes ? +(s.quota_bytes / 1024 ** 3).toFixed(2) : 0,
    count_mode: s.count_mode || 'out', on_exceeded: s.on_exceeded || 'alert', sort: s.sort, mode: s.mode, cap_reality: s.cap_reality, cap_landing: s.cap_landing,
  }
}
const currencies = ['USD', 'CNY', 'EUR', 'HKD', 'JPY', 'GBP'].map((v) => ({ label: v, value: v }))
const cycles = [{ label: '月付', value: 'month' }, { label: '季付', value: 'quarter' }, { label: '半年付', value: 'half' }, { label: '年付', value: 'year' }, { label: '一次性', value: 'once' }]
const countModes = Object.entries(countModeText).map(([value, label]) => ({ label, value }))
const exceeded = [{ label: '仅告警（80% / 90% / 100%）', value: 'alert' }, { label: '告警并停止 Xray', value: 'stop_xray' }]
async function saveAsset() {
  const a = { ...asset.value, expire_at: asset.value.expire_at ? new Date(asset.value.expire_at).toISOString() : null }
  if (await run(() => api.put(`/servers/${id.value}`, a), '已保存')) { await load(); loadServers() }
}
const cycleText = { month: '月', quarter: '季', half: '半年', year: '年', once: '一次性' }

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
  if (!(await ask(`回滚到 v${v.version}`, '会把这个版本重新下发到服务器。若新配置启动失败，Agent 会自动恢复上一份。'))) return
  const r = await run(() => api.post(`/servers/${id.value}/configs/${v.id}/rollback`, {}, { confirm: '回滚配置需要确认身份' }))
  if (r) { task.value = { show: true, id: r.task_id, title: `回滚到 v${v.version}` }; setTimeout(loadConfigs, 3000) }
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
          <div class="muted small">
            {{ sv.region || '地区未知' }}
            <template v-if="sv.latest?.uptime"> · 已运行 {{ dur(sv.latest.uptime) }}</template>
            · 最后心跳 {{ ago(sv.last_seen_at) }}
          </div>
        </div>
        <span class="spacer" />
        <n-button v-if="sv.mode === 'node'" :disabled="!sv.online" @click="runTool('restart_xray', '重启 Xray')">重启 Xray</n-button>
        <n-button :disabled="!sv.online" @click="router.push({ name: 'tools', query: { server: sv.id, tool: 'diagnose' } })">一键诊断</n-button>
        <n-dropdown :options="moreOptions" trigger="click" @select="onMore"><n-button>更多</n-button></n-dropdown>
      </div>

      <div v-if="sv.status === 'pending'" class="banner info">
        Agent 还没连上。<button class="link-btn" @click="onMore('token')">获取安装命令</button>
      </div>

      <n-tabs v-model:value="tab" type="line" animated :tab-style="isMobile ? 'padding: 8px 4px' : ''">
        <n-tab-pane name="overview" tab="概览">
          <div class="detail-grid">
            <div class="card span2">
              <div class="card-title"><h2>监控</h2><span class="spacer" /><n-select v-model:value="range" :options="ranges" size="small" style="width: 110px" /></div>
              <div v-if="!metrics.length" class="empty">暂无数据</div>
              <div v-else class="charts">
                <div><div class="chart-h">CPU</div><chart :option="cpuOpt" height="180px" /></div>
                <div><div class="chart-h">内存</div><chart :option="memOpt" height="180px" /></div>
                <div><div class="chart-h">网络</div><chart :option="netOpt" height="180px" /></div>
                <div><div class="chart-h">连接数 / 负载</div><chart :option="connOpt" height="180px" /></div>
              </div>
            </div>
            <div class="card">
              <div class="card-title"><h2>系统信息</h2></div>
              <div class="kv">
                <span>IPv4</span><span class="mono">{{ sv.ipv4 || '—' }}</span>
                <span>IPv6</span><span class="mono">{{ sv.ipv6 || '—' }}</span>
                <span>系统</span><span>{{ sys.os || '—' }}{{ sys.kernel ? ' · 内核 ' + sys.kernel : '' }}</span>
                <span>虚拟化</span><span>{{ sys.virt || '—' }} · {{ sys.arch || '' }}</span>
                <span>配置</span><span>{{ sys.cpu_cores || '?' }} vCPU · {{ bytes(sys.mem_total, 0) }} · {{ bytes(sys.disk_total, 0) }}</span>
                <span>CPU</span><span class="small">{{ sys.cpu_model || '—' }}</span>
                <span>连接数</span><span>TCP {{ sv.latest?.tcp ?? '—' }} · UDP {{ sv.latest?.udp ?? '—' }}</span>
                <span>磁盘</span><span>{{ bytes(sv.latest?.disk_used) }} / {{ bytes(sys.disk_total, 0) }}</span>
              </div>
            </div>
            <div class="card">
              <div class="card-title"><h2>本周期流量</h2><span class="muted small">{{ date(usage.cycle_start).slice(5) }} → {{ date(usage.cycle_end).slice(5) }}</span></div>
              <div class="big">{{ bytes(usage.counted) }}<small v-if="sv.quota_bytes"> / {{ bytes(sv.quota_bytes, 0) }}</small></div>
              <n-progress v-if="sv.quota_bytes" type="line" :percentage="Math.min(100, usage.percent || 0)" :show-indicator="false" :height="6" :color="usage.percent >= 90 ? '#b4233a' : '#0f7b45'" style="margin: 8px 0" />
              <p class="small muted">计费口径：{{ countModeText[sv.count_mode] || '只计出站' }} · 每月 {{ sv.reset_day || 1 }} 日重置</p>
              <p v-if="sv.quota_bytes" class="small">
                <template v-if="usage.forecast_at">按当前速度，预计 <b class="bad-text">{{ date(usage.forecast_at) }}</b> 用完额度</template>
                <template v-else>按当前速度，本周期不会超额</template>
              </p>
              <p class="small muted">↑ 出站 {{ bytes(usage.up) }} · ↓ 入站 {{ bytes(usage.down) }}</p>
              <p class="small muted">超额动作：{{ sv.on_exceeded === 'stop_xray' ? '告警并停止 Xray' : '仅告警（80% / 90% / 100%）' }}</p>
            </div>
            <div class="card">
              <div class="card-title"><h2>网络优化</h2><span class="spacer" />
                <n-button size="small" :disabled="!sv.online || !sv.switches?.allow_system_tune" @click="router.push({ name: 'tools', query: { server: sv.id, tab: 'bbr' } })">调整</n-button>
              </div>
              <div class="row">
                <span :class="['tag', sys.congestion === 'bbr' ? 'ok' : 'warn']">{{ sys.congestion === 'bbr' ? 'BBR 已开启' : '拥塞控制 ' + (sys.congestion || '未知') }}</span>
                <span class="tag">队列 {{ sys.qdisc || '—' }}</span>
              </div>
              <p v-if="!sv.switches?.allow_system_tune" class="small muted">这台机器的 Agent 关闭了系统调优开关（本地配置 allow_system_tune）。</p>
              <button v-else class="link-btn small" style="margin-top: 10px" :disabled="!sv.online" @click="runTool('restore_net', '恢复网络参数')">恢复调优前的参数</button>
            </div>
            <div class="card">
              <div class="card-title"><h2>版本</h2><span class="spacer" /><router-link :to="{ name: 'updates' }" class="small">更新中心</router-link></div>
              <div class="kv">
                <span>Agent</span><span>{{ sv.agent_version || '—' }} <span v-if="releases.agent && sv.agent_version && releases.agent !== sv.agent_version" class="tag warn">可更新到 {{ releases.agent }}</span></span>
                <template v-if="sv.mode === 'node'">
                  <span>Xray</span><span>{{ sv.xray_version || '—' }} <span v-if="releases.xray && sv.xray_version && releases.xray.replace(/^v/, '') !== sv.xray_version.replace(/^v/, '')" class="tag warn">可更新到 {{ releases.xray }}</span><span v-if="sv.xray_locked_version" class="tag">锁定 {{ sv.xray_locked_version }}</span></span>
                </template>
              </div>
            </div>
          </div>
        </n-tab-pane>

        <n-tab-pane v-if="sv.mode === 'node'" name="xray" tab="Xray">
          <div v-if="!xray" class="empty">加载中…</div>
          <div v-else class="detail-grid">
            <div class="card span2">
              <div class="row">
                <span :class="['dot', sv.xray_running ? 'ok' : 'bad']" />
                <b>Xray {{ sv.xray_running ? '运行中' : '未运行' }}</b>
                <span class="muted small">
                  {{ sv.xray_version }}
                  <template v-if="proc"> · 内存 {{ bytes(proc.mem) }} · 已运行 {{ dur(proc.uptime) }}</template>
                  · {{ sv.xray_drift ? '本地配置与面板不一致' : `配置与面板一致（v${sv.xray_config_ver}）` }}
                </span>
                <span class="spacer" />
                <n-button size="small" @click="runTool('xray_log', 'Xray 日志')">查看日志</n-button>
                <n-button size="small" @click="showPreview">预览配置</n-button>
                <n-button size="small" type="primary" :disabled="!sv.online" @click="deploy">重新下发</n-button>
              </div>
            </div>

            <div v-if="xray.reality" class="card">
              <div class="card-title"><h2>Reality 入站</h2><span class="spacer" />
                <n-button size="small" @click="editReality = !editReality">{{ editReality ? '取消' : '编辑' }}</n-button>
                <n-button size="small" @click="rotateKeys">轮换密钥</n-button>
              </div>
              <div v-if="!editReality" class="kv">
                <span>端口</span><span class="mono">{{ xray.reality.port }}</span>
                <span>协议</span><span>VLESS · TCP · xtls-rprx-vision</span>
                <span>目标站</span><span class="mono">{{ xray.reality.target }}</span>
                <span>SNI</span><span class="mono small">{{ xray.reality.server_names }}</span>
                <span>公钥</span><span class="mono small" style="word-break: break-all">{{ xray.reality.public_key }}</span>
                <span>shortIds</span><span>{{ (xray.reality.short_ids || '').split(',').filter(Boolean).length }} 个</span>
                <span>客户端地址</span><span class="mono">{{ xray.reality.address || sv.ipv4 }}</span>
                <span>指纹</span><span>{{ xray.reality.fingerprint }}</span>
              </div>
              <n-form v-else label-placement="top" size="small">
                <div class="form-2">
                  <n-form-item label="端口"><n-input-number v-model:value="reality.port" :min="1" :max="65535" :show-button="false" /></n-form-item>
                  <n-form-item label="指纹"><n-select v-model:value="reality.fingerprint" :options="fps" /></n-form-item>
                </div>
                <n-form-item label="目标站（host:port）"><n-input v-model:value="reality.target" placeholder="www.example.com:443" /></n-form-item>
                <n-form-item label="SNI（逗号分隔）"><n-input v-model:value="reality.server_names" /></n-form-item>
                <n-form-item label="shortIds（逗号分隔，留空自动生成）"><n-input v-model:value="reality.short_ids" /></n-form-item>
                <n-form-item label="客户端连接地址（留空用 IPv4）"><n-input v-model:value="reality.address" placeholder="域名或 IP" /></n-form-item>
                <p class="small muted" style="margin-top: 0">目标站可在「工具 → Reality 目标扫描」里挑选：需支持 TLS 1.3 与 H2、握手快。</p>
                <n-button type="primary" @click="saveReality">保存并下发</n-button>
              </n-form>
            </div>

            <div v-if="xray.landing" class="card">
              <div class="card-title"><h2>SS2022 落地</h2><span class="spacer" />
                <n-button size="small" @click="editLanding = !editLanding">{{ editLanding ? '取消' : '编辑' }}</n-button>
              </div>
              <div v-if="!editLanding" class="kv">
                <span>端口</span><span class="mono">{{ xray.landing.port }}</span>
                <span>加密</span><span class="mono small">{{ xray.landing.method }}</span>
                <span>防火墙</span><span>只放行入口机 IP</span>
              </div>
              <n-form v-else label-placement="top" size="small">
                <n-form-item label="端口（1024–65535）"><n-input-number v-model:value="landing.port" :min="1024" :max="65535" :show-button="false" /></n-form-item>
                <n-form-item label="加密方式"><n-select v-model:value="landing.method" :options="methods" /></n-form-item>
                <n-button type="primary" @click="saveLanding">保存并下发</n-button>
              </n-form>
              <h3 class="sub-h">入口用户（本月）</h3>
              <div v-if="!xray.landing_users?.length" class="muted small">还没有入口机连到这台落地</div>
              <table v-else class="plain">
                <thead><tr><th>入口</th><th>上行</th><th>下行</th></tr></thead>
                <tbody><tr v-for="u in xray.landing_users" :key="u.id"><td>{{ u.entry_name }}</td><td class="mono">{{ bytes(u.month_up) }}</td><td class="mono">{{ bytes(u.month_down) }}</td></tr></tbody>
              </table>
            </div>

            <div class="card span2">
              <div class="card-title"><h2>本机线路</h2><span class="spacer" />
                <n-button v-if="sv.cap_reality" size="small" type="primary" @click="router.push({ name: 'route-new', query: { entry: sv.id } })">新建线路</n-button>
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
          </div>
        </n-tab-pane>

        <n-tab-pane name="traffic" tab="流量">
          <div v-if="!traffic" class="empty">加载中…</div>
          <div v-else class="detail-grid">
            <div class="card span2">
              <div class="card-title"><h2>近 30 天（网卡）</h2><span class="muted small">合计 {{ bytes(dailyTotal) }}</span></div>
              <chart :option="dailyOpt" height="260px" />
            </div>
            <div class="card span2">
              <div class="card-title"><h2>本周期按线路</h2></div>
              <div class="table-wrap">
                <table class="plain">
                  <thead><tr><th>线路</th><th>上行</th><th>下行</th><th>合计</th></tr></thead>
                  <tbody>
                    <tr v-for="r in traffic.routes" :key="r.id"><td>{{ r.name }}</td><td class="mono">{{ bytes(r.up) }}</td><td class="mono">{{ bytes(r.down) }}</td><td class="mono">{{ bytes(r.up + r.down) }}</td></tr>
                    <tr><td class="muted">非代理流量（系统、探针等）</td><td class="mono muted">—</td><td class="mono muted">—</td>
                      <td class="mono muted">{{ bytes(Math.max(0, (traffic.usage.up + traffic.usage.down) - routeTotal)) }}</td></tr>
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        </n-tab-pane>

        <n-tab-pane name="asset" tab="资产">
          <div class="detail-grid">
            <div class="card">
              <div class="card-title"><h2>当前</h2></div>
              <div class="kv">
                <span>服务商</span><span>{{ sv.provider || '—' }} <a v-if="sv.provider_url" :href="sv.provider_url" target="_blank" rel="noopener noreferrer">打开商家后台</a></span>
                <span>价格</span><span>{{ sv.price ? `${sv.price} ${sv.currency} / ${cycleText[sv.pay_cycle] || '月'}` : '—' }}</span>
                <span>到期</span><span>{{ date(sv.expire_at) }}<template v-if="daysUntil(sv.expire_at) !== null">（{{ daysUntil(sv.expire_at) }} 天后）</template></span>
                <span>流量额度</span><span>{{ sv.quota_bytes ? bytes(sv.quota_bytes, 0) : '不限' }} · {{ countModeText[sv.count_mode] || '只计出站' }}</span>
                <span>分组 / 标签</span><span>{{ [sv.group, sv.tags].filter(Boolean).join(' · ') || '—' }}</span>
                <span>备注</span><span style="white-space: pre-wrap">{{ sv.note || '—' }}</span>
                <span>创建于</span><span>{{ dt(sv.created_at, true) }}</span>
              </div>
            </div>
            <div class="card">
              <div class="card-title"><h2>编辑</h2></div>
              <n-form label-placement="top" size="small">
                <div class="form-2">
                  <n-form-item label="名称"><n-input v-model:value="asset.name" /></n-form-item>
                  <n-form-item label="分组"><n-input v-model:value="asset.group" /></n-form-item>
                  <n-form-item label="标签"><n-input v-model:value="asset.tags" placeholder="逗号分隔" /></n-form-item>
                  <n-form-item label="排序"><n-input-number v-model:value="asset.sort" :show-button="false" /></n-form-item>
                  <n-form-item label="地区"><n-input v-model:value="asset.region" placeholder="自动识别" /></n-form-item>
                  <n-form-item label="国家代码"><n-input v-model:value="asset.country_code" maxlength="2" placeholder="HK" /></n-form-item>
                  <n-form-item label="服务商"><n-input v-model:value="asset.provider" /></n-form-item>
                  <n-form-item label="商家后台链接"><n-input v-model:value="asset.provider_url" placeholder="https://" /></n-form-item>
                  <n-form-item label="价格"><n-input-number v-model:value="asset.price" :min="0" :precision="2" :show-button="false" /></n-form-item>
                  <n-form-item label="币种"><n-select v-model:value="asset.currency" :options="currencies" tag filterable /></n-form-item>
                  <n-form-item label="付费周期"><n-select v-model:value="asset.pay_cycle" :options="cycles" /></n-form-item>
                  <n-form-item label="到期日"><n-date-picker v-model:value="asset.expire_at" type="date" clearable style="width: 100%" /></n-form-item>
                  <n-form-item label="流量额度（GB，0 = 不限）"><n-input-number v-model:value="asset.quota_gb" :min="0" :show-button="false" /></n-form-item>
                  <n-form-item label="每月重置日"><n-input-number v-model:value="asset.reset_day" :min="1" :max="31" /></n-form-item>
                  <n-form-item label="计费口径"><n-select v-model:value="asset.count_mode" :options="countModes" /></n-form-item>
                  <n-form-item label="超额动作"><n-select v-model:value="asset.on_exceeded" :options="exceeded" /></n-form-item>
                </div>
                <n-form-item label="备注"><n-input v-model:value="asset.note" type="textarea" :rows="2" /></n-form-item>
                <n-button type="primary" @click="saveAsset">保存</n-button>
              </n-form>
            </div>
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
                      <n-button v-if="v.version !== sv.xray_config_ver" size="tiny" quaternary type="warning" @click="rollback(v)">回滚到此</n-button>
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
      <p class="small muted" style="margin-top: 0">会在这台机器上安装 Xray，并由面板生成配置。</p>
      <n-checkbox v-model:checked="upgrade.cap_reality">Reality 入站（直连 / 入口）</n-checkbox><br>
      <n-checkbox v-model:checked="upgrade.cap_landing" style="margin-top: 8px">SS2022 落地</n-checkbox>
      <template #footer><div class="row" style="justify-content: flex-end"><n-button @click="upgrade.show = false">取消</n-button><n-button type="primary" @click="doUpgrade">升级</n-button></div></template>
    </n-modal>

    <n-modal v-model:show="install.show" preset="card" title="安装命令" style="width: min(640px, 96vw)">
      <p class="small" style="margin-top: 0">用 root 登录 VPS 执行（30 分钟内有效，只能使用一次）：</p>
      <pre class="pre" style="user-select: all">{{ install.command }}</pre>
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
.detail-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; padding-top: 8px; }
.span2 { grid-column: span 2; }
.charts { display: grid; grid-template-columns: 1fr 1fr; gap: 12px 20px; }
.chart-h { font-size: 12px; color: var(--c-muted); }
.big { font-size: 26px; font-weight: 700; font-variant-numeric: tabular-nums; }
.big small { font-size: 15px; color: var(--c-muted); font-weight: 500; }
.bad-text { color: var(--c-danger); }
.form-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 0 12px; }
.sub-h { font-size: 14px; margin: 18px 0 8px; }
p { margin: 6px 0; }
@media (max-width: 900px) {
  .detail-grid { grid-template-columns: 1fr; }
  .span2 { grid-column: auto; }
  .charts { grid-template-columns: 1fr; }
}
@media (max-width: 767px) {
  .head .flag { font-size: 26px; }
  .head .title { flex: 1 1 calc(100% - 50px); }
  .form-2 { grid-template-columns: 1fr; }
}
</style>
