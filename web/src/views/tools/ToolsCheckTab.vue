<script setup>
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NSelect, NInput } from 'naive-ui'
import { api } from '../../api'
import { loadServers, ui } from '../../store'
import { run } from '../../helpers'
import { dt, ago } from '../../format'
import TaskLog from '../../components/TaskLog.vue'
import ToolResult from '../../components/ToolResult.vue'

const props = defineProps({
  server: { type: Number, default: null },
  sv: { type: Object, default: null },
  serverOpts: { type: Array, required: true },
})
const route = useRoute()
const router = useRouter()

const toolDefs = [
  { id: 'diagnose', name: '一键诊断', desc: 'Xray、端口、防火墙、回程、时钟、DNS 与最近日志' },
  { id: 'unlock', name: '解锁检测', desc: 'Netflix、Disney+、YouTube、ChatGPT、Claude 等' },
  { id: 'ip_quality', name: 'IP 质量', desc: 'ASN、IP 类型、风险分与黑名单' },
  { id: 'trace', name: '回程路由', desc: '三网回程线路识别（CN2 / 9929 / CMI …）' },
  { id: 'speedtest', name: '测速', desc: '就近测速点上下行带宽' },
  { id: 'reality_scan', name: 'Reality 目标扫描', desc: '找握手快、支持 TLS 1.3 + H2 的目标站' },
  { id: 'net_status', name: '网络状态', desc: '拥塞控制、队列与 sysctl 当前值' },
  { id: 'iperf', name: 'iperf3 互测', desc: '测到另一台 VPS 的带宽' },
]
const toolName = (id) => toolDefs.find((t) => t.id === id)?.name || id
const selected = ref(toolDefs.some((t) => t.id === route.query.tool) ? String(route.query.tool) : 'diagnose')
watch(selected, (t) => router.replace({ query: { ...route.query, tool: t, run: undefined } }))

const runs = ref({})
const latest = ref({})
const iperfTo = ref(null)
const realityCands = ref('')
let latestAbort = null

async function loadLatest() {
  latestAbort?.abort()
  if (!props.server) { latest.value = {}; return }
  const ac = new AbortController()
  latestAbort = ac
  const sid = props.server
  const r = await api.get(`/servers/${sid}/tool-results`, { signal: ac.signal }).catch(() => null)
  if (ac.signal.aborted || props.server !== sid) return
  latest.value = r || {}
}
watch(() => props.server, () => { runs.value = {}; loadLatest() }, { immediate: true })
onMounted(() => {
  if (route.query.run && props.sv?.online && selected.value !== 'iperf') runTool(selected.value)
})
onBeforeUnmount(() => latestAbort?.abort())

const isRunning = (tool) => runs.value[tool]?.status === 'running'
function startRun(tool, task) { runs.value = { ...runs.value, [tool]: { task, status: 'running', data: null } } }
async function runTool(tool, args) {
  if (!props.server || isRunning(tool)) return
  const r = await run(() => api.post(`/servers/${props.server}/tools/${tool}`, args || {}))
  if (r) startRun(tool, r.task_id)
}
async function runIperf() {
  if (!iperfTo.value) { ui.message.warning('选择对端'); return }
  if (isRunning('iperf')) return
  const r = await run(() => api.post('/tools/iperf', { from: props.server, to: iperfTo.value }))
  if (r) startRun('iperf', r.task_id)
}
function execSelected() {
  if (selected.value === 'iperf') runIperf()
  else if (selected.value === 'reality_scan') runTool('reality_scan', realityCands.value.trim() ? { candidates: realityCands.value.trim() } : {})
  else runTool(selected.value)
}
async function onDone(tool, status) {
  const cur = runs.value[tool]
  if (!cur) return
  const task = cur.task
  const t = await api.get(`/tasks/${task}`).catch(() => null)
  if (runs.value[tool]?.task !== task) return
  runs.value = { ...runs.value, [tool]: { ...cur, status, data: t?.data ?? null } }
  if (status === 'success' || status === 'finished') loadLatest()
  if (tool === 'net_status') loadServers().catch(() => {})
}
const sel = computed(() => runs.value[selected.value])
const shown = computed(() => {
  const r = sel.value
  if (r?.status === 'running') return null
  if (r?.data) return { data: r.data, fresh: true }
  const l = latest.value[selected.value]
  return l ? { data: l.data, ts: l.ts, failed: !!r } : null
})
</script>

<template>
  <div class="tools-grid">
    <div class="tool-list">
      <button v-for="t in toolDefs" :key="t.id" type="button" :class="['tool', { on: selected === t.id }]" @click="selected = t.id">
        <span class="t-title"><b>{{ t.name }}</b><span v-if="isRunning(t.id)" class="t-run"><span class="dot warn" />运行中</span></span>
        <span class="muted small">{{ t.id === 'iperf' ? `测 ${sv?.name || '本机'} 到另一台 VPS 的带宽` : t.desc }}</span>
        <span v-if="latest[t.id]" class="small last">上次 {{ ago(latest[t.id].ts) }}：{{ latest[t.id].summary }}</span>
      </button>
    </div>
    <div class="card s-card out">
      <div class="s-card-head">
        <h3>{{ toolName(selected) }}</h3><span class="f-hint">{{ sv?.name }}</span>
        <span class="spacer" />
        <n-select v-if="selected === 'iperf'" v-model:value="iperfTo" size="small" :options="serverOpts.filter((o) => o.value !== server)" placeholder="选择对端" style="width: 180px" />
        <n-input v-if="selected === 'reality_scan'" v-model:value="realityCands" size="small" clearable placeholder="自定义候选域名，逗号分隔；留空用默认列表" style="width: 300px" @keyup.enter="execSelected" />
        <n-button type="primary" size="small" :loading="isRunning(selected)" :disabled="!sv?.online || isRunning(selected)" @click="execSelected">{{ isRunning(selected) ? '执行中' : shown ? '重新执行' : '执行' }}</n-button>
      </div>
      <div class="out-body">
        <template v-for="(r, t) in runs" :key="r.task">
          <details v-if="toolDefs.some((d) => d.id === t)" v-show="t === selected" class="log" :open="r.status === 'running' || !r.data">
            <summary class="small muted">执行输出</summary>
            <task-log :task-id="r.task" height="300px" @done="(st) => onDone(t, st)" />
          </details>
        </template>
        <template v-if="shown">
          <div v-if="!shown.fresh" class="small muted">{{ shown.failed ? '本次执行失败，下面是上次成功的结果' : '上次结果' }} · {{ dt(shown.ts) }}（{{ ago(shown.ts) }}）</div>
          <tool-result :tool="selected" :data="shown.data" />
        </template>
        <div v-else-if="!sel" class="empty">{{ !sv?.online ? '服务器离线' : selected === 'iperf' ? '选择对端后点「执行」' : selected === 'net_status' ? '点「执行」读取当前网络参数' : '还没有结果，点「执行」开始检测' }}</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.tools-grid { display: grid; grid-template-columns: 320px minmax(0, 1fr); gap: 16px; }
.tool-list { display: flex; flex-direction: column; gap: 8px; }
.tool { text-align: left; font: inherit; color: inherit; background: #fff; border: 1px solid var(--c-border); border-radius: 6px; padding: 10px 12px; display: flex; flex-direction: column; gap: 3px; cursor: pointer; }
.tool:hover:not(:disabled) { border-color: var(--c-primary); }
.tool.on { border-color: var(--c-primary); background: var(--c-primary-soft); }
.t-title { display: flex; align-items: center; gap: 8px; }
.t-run { display: inline-flex; align-items: center; gap: 5px; font-size: 12px; color: var(--c-warning); }
.tool .last { color: var(--c-text-2); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.out-body { display: flex; flex-direction: column; gap: 14px; padding-bottom: 18px; }
.log > summary { cursor: pointer; user-select: none; padding: 2px 0; }
.log[open] > summary { margin-bottom: 8px; }
@media (max-width: 900px) { .tools-grid { grid-template-columns: 1fr; } }
</style>
