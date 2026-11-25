<script setup>
import { ref, watch, onBeforeUnmount } from 'vue'
import { NButton, NSelect, NCheckbox, NModal } from 'naive-ui'
import { api } from '../../api'
import { run } from '../../helpers'
import { dt } from '../../format'
import ToolResult from '../../components/ToolResult.vue'

const props = defineProps({
  server: { type: Number, default: null },
  sv: { type: Object, default: null },
})

const toolLabels = {
  diagnose: '一键诊断', unlock: '解锁检测', ip_quality: 'IP 质量', trace: '回程路由',
  speedtest: '测速', reality_scan: 'Reality 目标扫描', net_status: '网络状态',
  iperf: 'iperf3 互测', tune_net: '网络优化', restore_net: '还原网络参数',
}
const toolName = (id) => toolLabels[id] || id
const hToolOpts = [{ label: '全部工具', value: '' }, ...['diagnose', 'unlock', 'ip_quality', 'trace', 'speedtest', 'reality_scan', 'iperf', 'tune_net'].map((v) => ({ label: toolName(v), value: v }))]

const history = ref([])
const hTool = ref('')
const compare = ref(new Set())
const cmp = ref({ show: false, items: [] })
let historyAbort = null

async function loadHistory() {
  historyAbort?.abort()
  const ac = new AbortController()
  historyAbort = ac
  const q = new URLSearchParams()
  if (props.server) q.set('server_id', props.server)
  if (hTool.value) q.set('tool', hTool.value)
  const r = await run(() => api.get('/tool-results?' + q, { signal: ac.signal }))
  if (ac.signal.aborted) return
  history.value = r || []
  compare.value = new Set()
}
watch(() => [props.server, hTool.value], loadHistory, { immediate: true })
onBeforeUnmount(() => historyAbort?.abort())

function toggleCmp(id, v) { const s = new Set(compare.value); v ? s.add(id) : s.delete(id); compare.value = s }
function showCompare() { cmp.value = { show: true, items: history.value.filter((h) => compare.value.has(h.id)) } }
</script>

<template>
  <div class="sec-head">
    <div class="s-head"><h2>历史结果</h2><p>{{ sv?.name || '所选服务器' }} 的检测记录。勾选两条以上可以并排对比。</p></div>
    <span class="spacer" />
    <n-select v-model:value="hTool" :options="hToolOpts" aria-label="工具" style="width: 140px" />
    <n-button :disabled="compare.size < 2" @click="showCompare">对比所选（{{ compare.size }}）</n-button>
  </div>
  <div class="card s-card">
    <div v-if="!history.length" class="empty">暂无结果</div>
    <div v-else class="table-wrap">
      <table class="plain">
        <thead><tr><th>选择</th><th>时间</th><th>工具</th><th>摘要</th></tr></thead>
        <tbody>
          <tr v-for="h in history" :key="h.id">
            <td><n-checkbox :checked="compare.has(h.id)" @update:checked="(v) => toggleCmp(h.id, v)" /></td>
            <td class="nowrap">{{ dt(h.ts) }}</td><td class="nowrap">{{ toolName(h.tool) }}</td><td class="small">{{ h.summary }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>

  <n-modal v-model:show="cmp.show" preset="card" title="结果对比" style="width: min(1100px, 96vw)">
    <div class="cmp">
      <div v-for="h in cmp.items" :key="h.id" class="card">
        <div class="small muted">{{ dt(h.ts, true) }} · {{ toolName(h.tool) }}</div>
        <div class="small" style="margin: 4px 0 10px">{{ h.summary }}</div>
        <tool-result :tool="h.tool" :data="h.data" />
      </div>
    </div>
  </n-modal>
</template>

<style scoped>
.cmp { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 12px; }
</style>
