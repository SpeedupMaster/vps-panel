<script setup>
import { shallowRef, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { useRouter } from 'vue-router'
import { VueFlow, MarkerType } from '@vue-flow/core'
import { Background } from '@vue-flow/background'
import { Controls } from '@vue-flow/controls'
import '@vue-flow/core/dist/style.css'
import '@vue-flow/core/dist/theme-default.css'
import '@vue-flow/controls/dist/style.css'
import { NButton } from 'naive-ui'
import { api } from '../api'
import { on } from '../store'
import { run } from '../helpers'
import { bytes, flag } from '../format'
import { isMobile } from '../mobile'

const router = useRouter()
const topo = shallowRef(null)
const dragged = new Map()
watch(isMobile, () => dragged.clear())
let loadAbort = null
async function load() {
  loadAbort?.abort()
  const ac = new AbortController()
  loadAbort = ac
  const r = await run(() => api.get('/topology', { signal: ac.signal }))
  if (!ac.signal.aborted && r) topo.value = r
}
onMounted(load)
const off = on('route', load)
const t = setInterval(() => { if (!document.hidden) load() }, 60000)
onBeforeUnmount(() => { off(); clearInterval(t); loadAbort?.abort() })
function onNodeDragStop({ node }) {
  if (node?.id && node.position) dragged.set(node.id, { x: node.position.x, y: node.position.y })
}

const graph = computed(() => {
  const tp = topo.value
  if (!tp) return { nodes: [], edges: [] }
  const edges = [...(tp.edges || [])]
  const isEntry = (id) => edges.some((e) => e.from === id) || false
  const cols = { entry: [], landing: [], idle: [] }
  for (const n of tp.nodes) {
    if (isEntry(n.id) || (n.roles || []).includes('entry')) cols.entry.push(n)
    else if ((n.roles || []).includes('landing')) cols.landing.push(n)
    else cols.idle.push(n)
  }
  const vertical = isMobile.value
  const gapA = vertical ? 200 : 300 // between columns
  const gapB = vertical ? 150 : 110 // within a column
  const place = (col, i, len) => {
    const off = i * gapB - ((len - 1) * gapB) / 2
    return vertical ? { x: off * 1.2, y: col * gapA } : { x: col * gapA, y: off }
  }
  const nodes = []
  const mk = (n, col, i, len) => {
    const id = String(n.id)
    nodes.push({
      id, position: dragged.get(id) || place(col, i, len), type: 'server', data: n,
      sourcePosition: vertical ? 'bottom' : 'right', targetPosition: vertical ? 'top' : 'left',
    })
  }
  cols.entry.forEach((n, i) => mk(n, 0, i, cols.entry.length))
  cols.landing.forEach((n, i) => mk(n, 1, i, cols.landing.length))
  cols.idle.forEach((n, i) => mk(n, 0, cols.entry.length + i, cols.entry.length + cols.idle.length))
  if (edges.some((e) => e.to === 0) || cols.landing.length) {
    nodes.push({ id: '0', position: dragged.get('0') || place(2, 0, 1), type: 'internet', data: {}, targetPosition: vertical ? 'top' : 'left' })
    // landings exit to the internet too
    for (const l of cols.landing) edges.push({ from: l.id, to: 0, routes: [], names: [], exit: true })
  }
  const es = edges.map((e, i) => {
    const down = e.down
    const color = down ? '#b4233a' : e.backup ? '#9aa0a6' : e.exit ? '#c9cdd2' : '#0f7b45'
    const label = e.exit ? '' : e.to === 0 ? `直连 · 今日 ${bytes(e.today_bytes)}` : e.backup ? '备用' : `${e.segment_ms ? Math.round(e.segment_ms) + ' ms' : '—'} · 今日 ${bytes(e.today_bytes)}`
    return {
      id: 'e' + i, source: String(e.from), target: String(e.to), label, animated: !e.backup && !e.exit && !down && e.today_bytes > 0,
      style: { stroke: color, strokeWidth: e.backup || e.exit ? 1.2 : 2, strokeDasharray: e.backup || down ? '6 4' : undefined },
      labelStyle: { fontSize: '11px', fill: down ? '#b4233a' : '#3d4248' }, labelBgStyle: { fill: '#fff' },
      markerEnd: { type: MarkerType.ArrowClosed, color }, data: e,
    }
  })
  return { nodes, edges: es }
})
function onNodeClick({ node }) { if (node.id !== '0') router.push({ name: 'server', params: { id: Number(node.id) } }) }
const roleText = (r) => (r || []).map((x) => ({ entry: '入口', landing: '落地' }[x] || x)).join(' · ')
</script>

<template>
  <div class="page topo-page">
    <div class="page-head">
      <div class="ph"><h1>拓扑</h1><p class="page-desc">所有节点和线路的连接关系，每分钟自动刷新。</p></div>
    </div>
    <section class="l-sec flow-sec">
    <div class="sec-head">
      <div class="s-head">
        <h2>线路图</h2>
        <p>入口在左，落地在中间，最右边是互联网。连线上是线路段延迟（10 分钟均值）和今日流量，点击节点查看详情，可以拖动节点调整位置。</p>
      </div>
      <span class="spacer" />
      <n-button @click="load">刷新</n-button>
    </div>
    <div class="card s-card flow-card">
      <div class="s-card-head legend small">
        <span><i class="ln ok" />线路（流动 = 今日有流量）</span>
        <span><i class="ln back" />备用落地</span>
        <span><i class="ln down" />线路段中断</span>
        <span><span class="dot bad" /> 疑似被封</span>
      </div>
      <div v-if="topo && !topo.nodes?.length" class="empty">还没有节点</div>
      <vue-flow v-else :nodes="graph.nodes" :edges="graph.edges" fit-view-on-init :min-zoom="0.3" :max-zoom="2"
        :nodes-draggable="true" :nodes-connectable="false" :elements-selectable="false" @node-click="onNodeClick" @node-drag-stop="onNodeDragStop">
        <background pattern-color="#e4e6ea" :gap="20" />
        <controls :show-interactive="false" />
        <template #node-server="{ data }">
          <div :class="['node', { blocked: data.blocked, off: data.status !== 'online' }]">
            <div class="row" style="gap: 6px; flex-wrap: nowrap"><span>{{ flag(data.country_code) }}</span><b>{{ data.name }}</b><span :class="['dot', data.blocked ? 'bad' : data.status === 'online' ? 'ok' : 'off']" /></div>
            <div class="muted small">{{ roleText(data.roles) }}</div>
            <div class="small">回程 {{ data.backhaul_ms ? Math.round(data.backhaul_ms) + ' ms' : '—' }}</div>
          </div>
        </template>
        <template #node-internet>
          <div class="node net"><b>互联网</b><div class="muted small">目标网站</div></div>
        </template>
      </vue-flow>
    </div>
    </section>
  </div>
</template>

<style scoped>
.topo-page { height: 100vh; height: 100dvh; }
.flow-sec { flex: 1; min-height: 0; }
.flow-card { flex: 1; min-height: 420px; padding: 0; overflow: hidden; display: flex; flex-direction: column; }
.flow-card > .vue-flow { flex: 1; min-height: 0; height: auto; }
.legend { gap: 8px 18px; color: var(--c-muted); padding: 12px 20px; border-bottom: 1px solid var(--c-border); }
.legend span { display: inline-flex; align-items: center; gap: 6px; }
.ln { display: inline-block; width: 22px; height: 0; border-top: 2px solid #0f7b45; }
.ln.back { border-top: 2px dashed #9aa0a6; }
.ln.down { border-top: 2px dashed #b4233a; }
.node { background: #fff; border: 1px solid var(--c-border-strong); border-radius: 8px; padding: 8px 12px; min-width: 130px; font-size: 13px; cursor: pointer; box-shadow: 0 1px 2px rgba(0, 0, 0, .04); }
.node.blocked { border-color: var(--c-danger); background: var(--c-danger-soft); }
.node.off { opacity: .65; }
.node.net { background: var(--c-surface); cursor: default; text-align: center; }
@media (max-width: 767px) { .topo-page { height: calc(100dvh - 52px - var(--safe-top)); } .flow-card { min-height: 360px; } .legend { padding: 10px 16px; gap: 6px 14px; } }
</style>
