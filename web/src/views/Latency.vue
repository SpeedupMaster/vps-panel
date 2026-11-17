<script>
export default { name: 'Latency' }
</script>
<script setup>
import { ref, computed, onMounted, watch, onBeforeUnmount } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { NButton, NSelect } from 'naive-ui'
import { api } from '../api'
import { state, loadServers, on } from '../store'
import { run } from '../helpers'
import { carrierLabel, flag, latClass } from '../format'
import Chart from '../components/Chart.vue'
import TargetManager from '../components/TargetManager.vue'

const router = useRouter()
const route = useRoute()
const targets = ref([])
const matrix = ref([])
const segs = ref([])
const peers = ref([])
const series = ref({})
const server = ref(Number(route.query.server) || null)
const range = ref(86400)
const managing = ref(false)

const enabled = computed(() => targets.value.filter((t) => t.enabled))
const tmap = computed(() => Object.fromEntries(targets.value.map((t) => [t.id, t])))
const servers = computed(() => state.servers.filter((s) => s.status !== 'pending'))
const serverOpts = computed(() => servers.value.map((s) => ({ label: s.name, value: s.id })))
const ranges = [{ label: '6 小时', value: 21600 }, { label: '24 小时', value: 86400 }, { label: '7 天', value: 604800 }, { label: '30 天', value: 2592000 }]

async function loadAll() {
  const [t, m, s, p] = await Promise.all([
    run(() => api.get('/probes/targets')), run(() => api.get('/probes/matrix?kind=backhaul')),
    run(() => api.get('/probes/matrix?kind=segment')), run(() => api.get('/probes/matrix?kind=peer')),
  ])
  targets.value = t?.items || []; matrix.value = m || []; segs.value = s || []; peers.value = p || []
}
async function loadSeries() {
  if (!server.value || !enabled.value.length) { series.value = {}; return }
  series.value = (await run(() => api.get(`/probes/series?server_id=${server.value}&range=${range.value}&targets=${enabled.value.map((t) => t.id).join(',')}`))) || {}
}
onMounted(async () => {
  if (!state.serversLoaded) await loadServers().catch(() => {})
  await loadAll()
  if (!server.value && servers.value.length) server.value = (servers.value.find((s) => s.mode === 'node') || servers.value[0]).id
})
watch([server, range, () => enabled.value.length], loadSeries)
// Kept alive: follow ?server= when opened again from a server page.
watch(() => route.query.server, (v) => { if (route.name === 'latency' && v) server.value = Number(v) })
let debounce = null
const off = on('probe', () => { clearTimeout(debounce); debounce = setTimeout(() => { loadAll(); if (range.value <= 86400) loadSeries() }, 3000) })
onBeforeUnmount(() => { off(); clearTimeout(debounce) })

// ---- smoke chart per carrier: band = min..max across that carrier's targets, line = median of averages ----
function median(a) { if (!a.length) return null; const s = [...a].sort((x, y) => x - y); const m = s.length >> 1; return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2 }
function carrierSeries(c) {
  const ids = enabled.value.filter((t) => t.carrier === c).map((t) => t.id)
  const bucket = range.value > 86400 * 2 ? 3600 : range.value > 21600 ? 300 : 60
  const by = new Map()
  for (const id of ids) for (const r of series.value[id] || []) {
    const k = Math.floor(r.ts / bucket) * bucket
    if (!by.has(k)) by.set(k, { avg: [], min: [], max: [], loss: [] })
    const b = by.get(k)
    b.loss.push(r.loss)
    if (r.loss < 100 && r.avg > 0) { b.avg.push(r.avg); b.min.push(r.min || r.avg); b.max.push(r.max || r.avg) }
  }
  const ks = [...by.keys()].sort((a, b) => a - b)
  return ks.map((k) => {
    const b = by.get(k)
    return { ts: k, med: median(b.avg), min: b.min.length ? Math.min(...b.min) : null, max: b.max.length ? Math.max(...b.max) : null, loss: b.loss.reduce((a, x) => a + x, 0) / (b.loss.length || 1) }
  })
}
const fmt = (ts) => { const d = new Date(ts * 1000); const hm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`; return range.value > 86400 ? `${d.getMonth() + 1}-${d.getDate()} ${hm}` : hm }
function smokeOpt(c) {
  const pts = carrierSeries(c)
  const x = pts.map((p) => fmt(p.ts))
  return {
    grid: { left: 44, right: 10, top: 10, bottom: 24 },
    tooltip: {
      trigger: 'axis',
      formatter: (ps) => {
        const p = pts[ps[0].dataIndex]
        return `${x[ps[0].dataIndex]}<br>中位数 ${p.med != null ? Math.round(p.med) + ' ms' : '—'}<br>范围 ${p.min != null ? Math.round(p.min) + ' ~ ' + Math.round(p.max) + ' ms' : '—'}<br>丢包 ${p.loss.toFixed(1)}%`
      },
    },
    xAxis: { type: 'category', data: x, boundaryGap: false, axisLabel: { fontSize: 10, color: '#5f6368' }, axisLine: { lineStyle: { color: '#d5d8dd' } } },
    yAxis: { type: 'value', min: 0, axisLabel: { fontSize: 10, color: '#5f6368' }, splitLine: { lineStyle: { color: '#eef0f2' } } },
    series: [
      { type: 'line', stack: 'band', data: pts.map((p) => p.min), lineStyle: { opacity: 0 }, showSymbol: false, silent: true },
      { type: 'line', stack: 'band', data: pts.map((p) => (p.max != null && p.min != null ? p.max - p.min : null)), lineStyle: { opacity: 0 }, areaStyle: { color: '#0f7b45', opacity: 0.15 }, showSymbol: false, silent: true },
      {
        type: 'line', data: pts.map((p) => ({ value: p.med, itemStyle: { color: p.loss > 20 ? '#b4233a' : p.loss >= 1 ? '#d98613' : '#0f7b45' } })),
        lineStyle: { color: '#0f7b45', width: 1.5 }, symbol: 'circle', symbolSize: (v, p) => (pts[p.dataIndex].loss >= 1 ? 5 : 0), showSymbol: true,
      },
    ],
  }
}
const carriers = computed(() => ['telecom', 'unicom', 'mobile'].filter((c) => enabled.value.some((t) => t.carrier === c)))

// ---- summary: latest per server × carrier ----
const summary = computed(() => {
  const rows = []
  for (const s of servers.value) {
    const cells = matrix.value.filter((c) => c.server_id === s.id && tmap.value[c.target]?.enabled)
    if (!cells.length) continue
    const per = {}
    for (const c of ['telecom', 'unicom', 'mobile']) {
      const cs = cells.filter((x) => tmap.value[x.target]?.carrier === c)
      if (!cs.length) continue
      const ok = cs.filter((x) => x.loss < 100 && x.avg > 0)
      per[c] = { avg: ok.length ? ok.reduce((a, x) => a + x.avg, 0) / ok.length : 0, loss: cs.reduce((a, x) => a + x.loss, 0) / cs.length, n: cs.length }
    }
    const losses = Object.values(per).map((p) => p.loss)
    const allLost = losses.length && losses.every((l) => l >= 99)
    let verdict = ['ok', '正常']
    if (s.blocked || allLost) verdict = ['bad', '疑似被封']
    else if (losses.some((l) => l > 20)) verdict = ['warn', '丢包严重']
    else if (Object.values(per).some((p) => p.avg > 250)) verdict = ['warn', '延迟偏高']
    rows.push({ s, per, verdict })
  }
  return rows
})
// ---- segment list ----
const segRows = computed(() => segs.value.map((c) => ({ ...c, entry: state.servers.find((s) => s.id === c.server_id), landing: state.servers.find((s) => `seg-${s.id}` === c.target) })).filter((r) => r.entry && r.landing))
// ---- peer matrix ----
const peerServers = computed(() => {
  const ids = new Set()
  for (const c of peers.value) { ids.add(c.server_id); ids.add(Number(c.target.replace('peer-', ''))) }
  return state.servers.filter((s) => ids.has(s.id))
})
function peer(a, b) { return peers.value.find((c) => c.server_id === a && c.target === `peer-${b}`) }
const peerMax = computed(() => Math.max(1, ...peers.value.filter((c) => c.loss < 100).map((c) => c.avg)))
function peerStyle(c) {
  if (!c) return {}
  if (c.loss >= 100) return { background: '#fbe7ea', color: '#b4233a' }
  const t = Math.min(1, c.avg / peerMax.value)
  return { background: `rgba(15, 123, 69, ${0.08 + t * 0.55})`, color: t > 0.6 ? '#fff' : '#1f2225' }
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div class="ph"><h1>延迟</h1><p class="page-desc">各台 VPS 到国内三网目标的回程延迟与丢包。</p></div>
      <span class="spacer" />
      <n-button @click="managing = true">管理拨测目标（{{ enabled.length }} 个）</n-button>
    </div>
    <div class="banner info">
      没有国内探测点：下面是各台 VPS 访问国内目标（zstaticcdn.com 三网节点）的回程数据，能反映线路质量和 IP 是否被封，测不出封端口或协议干扰。
    </div>

    <section class="l-sec">
      <div class="sec-head">
        <div class="s-head"><h2>回程</h2><p>所选服务器到电信、联通、移动目标的延迟分布和丢包。</p></div>
        <span class="spacer" />
        <n-select v-model:value="server" :options="serverOpts" aria-label="服务器" style="width: 150px" filterable placeholder="选择服务器" />
        <n-select v-model:value="range" :options="ranges" aria-label="时间范围" style="width: 100px" />
      </div>
      <div class="card s-card">
        <div v-if="!enabled.length" class="empty">还没有启用拨测目标。<button class="link-btn" @click="managing = true">选择目标</button></div>
        <template v-else>
          <div class="s-card-head"><span class="legend small"><i class="band" />最小 ~ 最大 <i class="line" />中位数 <i class="pt warn" />丢包 1–20% <i class="pt bad" />丢包 &gt; 20%</span></div>
          <div class="smokes">
            <div v-for="c in carriers" :key="c">
              <div class="chart-h">{{ carrierLabel[c] }} <span class="muted">· {{ enabled.filter((t) => t.carrier === c).length }} 个目标</span></div>
              <chart :option="smokeOpt(c)" height="170px" />
            </div>
          </div>
        </template>
      </div>
    </section>

    <section class="l-sec">
      <div class="sec-head"><div class="s-head"><h2>现在</h2><p>最近一轮的结果，三网各自平均。点击一行查看这台的回程图。</p></div></div>
      <div class="card s-card">
      <div v-if="!summary.length" class="empty">暂无数据</div>
      <div v-else class="table-wrap">
        <table class="plain">
          <thead><tr><th>服务器</th><th>电信</th><th>联通</th><th>移动</th><th>判断</th></tr></thead>
          <tbody>
            <tr v-for="r in summary" :key="r.s.id" class="click" @click="server = r.s.id">
              <td class="nowrap">{{ flag(r.s.country_code) }} <b>{{ r.s.name }}</b></td>
              <td v-for="c in ['telecom', 'unicom', 'mobile']" :key="c" class="mono nowrap">
                <template v-if="r.per[c]">
                  <span :class="['dot', r.per[c].loss >= 99 ? 'bad' : latClass(r.per[c].avg)]" />
                  {{ r.per[c].loss >= 99 ? '不可达' : Math.round(r.per[c].avg) + ' ms' }}
                  <span v-if="r.per[c].loss >= 1 && r.per[c].loss < 99" class="small warn-text">丢 {{ Math.round(r.per[c].loss) }}%</span>
                </template>
                <span v-else class="muted">—</span>
              </td>
              <td><span :class="['tag', r.verdict[0]]">{{ r.verdict[1] }}</span></td>
            </tr>
          </tbody>
        </table>
      </div>
      </div>
    </section>

    <div class="grid-2">
      <section class="l-sec">
        <div class="sec-head"><div class="s-head"><h2>线路段 · 入口 → 落地</h2><p>入口机对落地 SS 端口做 TCP 探测；Xray Observatory 同时每分钟探活，用于备用落地切换。</p></div></div>
        <div class="card s-card">
        <div v-if="!segRows.length" class="empty">没有链式线路</div>
        <div v-else class="table-wrap">
        <table class="plain">
          <thead><tr><th>入口</th><th>落地</th><th>延迟</th><th>丢包</th></tr></thead>
          <tbody>
            <tr v-for="r in segRows" :key="r.server_id + r.target">
              <td>{{ r.entry.name }}</td><td>{{ r.landing.name }}</td>
              <td class="mono"><span :class="['dot', r.loss >= 100 ? 'bad' : latClass(r.avg)]" /> {{ r.loss >= 100 ? '中断' : Math.round(r.avg) + ' ms' }}</td>
              <td class="mono">{{ Math.round(r.loss) }}%</td>
            </tr>
          </tbody>
        </table>
        </div>
        </div>
      </section>
      <section class="l-sec">
        <div class="sec-head"><div class="s-head"><h2>VPS 互测矩阵</h2><p>单位 ms，颜色越深延迟越高。每行是发起方，每列是目标。</p></div></div>
        <div class="card s-card">
        <div v-if="peerServers.length < 2" class="empty">需要在设置里开启 VPS 互测，且至少两台在线</div>
        <div v-else class="table-wrap s-pad">
          <table class="peer">
            <thead><tr><th /><th v-for="b in peerServers" :key="b.id" class="small">{{ b.name }}</th></tr></thead>
            <tbody>
              <tr v-for="a in peerServers" :key="a.id">
                <th class="small nowrap">{{ a.name }}</th>
                <td v-for="b in peerServers" :key="b.id" class="mono small" :style="a.id === b.id ? {} : peerStyle(peer(a.id, b.id))">
                  {{ a.id === b.id ? '·' : peer(a.id, b.id) ? (peer(a.id, b.id).loss >= 100 ? '✕' : Math.round(peer(a.id, b.id).avg)) : '' }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        </div>
      </section>
    </div>

    <target-manager v-model:show="managing" @changed="loadAll" />
  </div>
</template>

<style scoped>
.smokes { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 16px; padding: 4px 0 16px; }
.chart-h { font-size: 13px; font-weight: 600; }
.legend { display: inline-flex; align-items: center; gap: 6px; color: var(--c-muted); flex-wrap: wrap; }
.legend i { display: inline-block; margin-left: 8px; }
.legend i:first-child { margin-left: 0; }
.band { width: 16px; height: 10px; background: rgba(15, 123, 69, .2); }
.line { width: 16px; height: 0; border-top: 2px solid #0f7b45; }
.pt { width: 8px; height: 8px; border-radius: 50%; }
.pt.warn { background: #d98613; } .pt.bad { background: #b4233a; }
.warn-text { color: var(--c-warning); }
tr.click { cursor: pointer; }
table.peer { border-collapse: separate; border-spacing: 2px; }
table.peer th { font-weight: 500; color: var(--c-muted); padding: 4px 6px; text-align: center; }
table.peer td { min-width: 46px; height: 34px; text-align: center; border-radius: 4px; background: var(--c-surface); }
@media (max-width: 1100px) { .smokes { grid-template-columns: 1fr; } }
</style>
