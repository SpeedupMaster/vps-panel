<script setup>
import { ref, computed, watch } from 'vue'
import { NButton, NInputNumber, NRadioGroup, NRadioButton } from 'naive-ui'
import { api } from '../../api'
import { loadServers } from '../../store'
import { run, ask } from '../../helpers'
import TaskLog from '../../components/TaskLog.vue'
import ToolResult from '../../components/ToolResult.vue'

const props = defineProps({
  server: { type: Number, default: null },
  sv: { type: Object, default: null },
})

const bbrTool = ref('tune_net')
const runs = ref({})
const tune = ref({ profile: 'balanced', bandwidth_mbps: null })
const tuneBusy = ref(false)
const profiles = [{ label: '保守', value: 'conservative' }, { label: '均衡', value: 'balanced' }, { label: '激进', value: 'aggressive' }]

watch(() => [props.server, props.sv?.bandwidth_mbps], () => {
  tune.value.bandwidth_mbps = props.sv?.bandwidth_mbps || null
  runs.value = {}
}, { immediate: true })

const bbrLoaded = computed(() => (props.sv?.system?.avail_cc || '').split(/\s+/).includes('bbr'))
const bbrRun = computed(() => runs.value[bbrTool.value])
const isRunning = (tool) => runs.value[tool]?.status === 'running'
const tuneArgs = (dry) => ({ profile: tune.value.profile, bandwidth_mbps: String(tune.value.bandwidth_mbps || 1000), dry_run: dry ? '1' : '0' })

async function start(tool, args) {
  if (!props.server || isRunning(tool)) return
  const r = await run(() => api.post(`/servers/${props.server}/tools/${tool}`, args || {}))
  if (r) runs.value = { ...runs.value, [tool]: { task: r.task_id, status: 'running', data: null } }
}
async function previewTune() {
  if (tuneBusy.value) return
  bbrTool.value = 'tune_net'
  tuneBusy.value = true
  try { await start('tune_net', tuneArgs(true)) } finally { tuneBusy.value = false }
}
async function applyTune() {
  if (tuneBusy.value) return
  bbrTool.value = 'tune_net'
  tuneBusy.value = true
  try {
    if (!(await ask('应用网络优化', '会写入 /etc/sysctl.d/99-vpspanel.conf 并立即生效，原值自动备份，可随时还原。'))) return
    await start('tune_net', tuneArgs(false))
  } finally { tuneBusy.value = false }
}
async function restoreTune() {
  if (tuneBusy.value) return
  bbrTool.value = 'restore_net'
  tuneBusy.value = true
  try {
    if (!(await ask('还原网络参数', '把所有项改回优化前的数值并删除 /etc/sysctl.d/99-vpspanel.conf。'))) return
    await start('restore_net')
  } finally { tuneBusy.value = false }
}
async function onDone(tool, status) {
  const cur = runs.value[tool]
  if (!cur) return
  const task = cur.task
  const t = await api.get(`/tasks/${task}`).catch(() => null)
  if (runs.value[tool]?.task !== task) return
  runs.value = { ...runs.value, [tool]: { ...cur, status, data: t?.data ?? null } }
  loadServers().catch(() => {})
}
</script>

<template>
  <div class="sections">
    <section class="s-group">
      <div class="s-head"><h2>BBR 与 TCP 缓冲区</h2><p>开启 BBR，并按带宽和回程延迟设置 TCP 缓冲区上限。改动写入 /etc/sysctl.d/99-vpspanel.conf，应用前自动备份原值。</p></div>
      <div class="card s-card">
        <div v-if="sv && !sv.switches?.allow_system_tune" class="banner warn">此机器的 Agent 关闭了系统调优：在 VPS 上执行 <code class="mono">vpsagent config set allow_system_tune true</code> 后重启 Agent。</div>
        <div v-else-if="sv?.system?.virt && ['openvz', 'lxc'].includes(sv.system.virt)" class="banner warn">此机器是 {{ sv.system.virt }} 容器，无法修改拥塞控制。</div>
        <div class="field f-inline">
          <div class="f-label">
            <span class="f-name">当前</span>
            <span class="f-hint">已加载：{{ sv?.system?.avail_cc || '—' }}<template v-if="sv?.system?.avail_cc && !bbrLoaded"><span class="warn-text"> · BBR 未加载</span>，应用优化时会先加载 tcp_bbr 模块，内核里没有时自动安装</template></span>
          </div>
          <div class="f-ctl"><span :class="['mono', sv?.system?.congestion === 'bbr' ? 'ok-text' : '']">{{ sv?.system?.congestion || '—' }} · {{ sv?.system?.qdisc || '—' }}</span></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">档位</span><span class="f-hint">TCP 缓冲区上限：保守为带宽时延积的 1 倍，均衡 2 倍，激进 4 倍</span></div>
          <div class="f-ctl"><n-radio-group v-model:value="tune.profile" class="seg"><n-radio-button v-for="p in profiles" :key="p.value" :value="p.value">{{ p.label }}</n-radio-button></n-radio-group></div>
        </div>
        <div class="field">
          <div class="f-label">
            <span class="f-name">带宽</span>
            <span class="f-hint">缓冲区按 带宽 × 回程 RTT 计算，RTT 自动取这台机器的回程延迟。<template v-if="sv?.bandwidth_mbps">已填入服务器设置的 {{ sv.bandwidth_mbps }} Mbps</template><template v-else-if="sv">这台服务器没设置带宽，按 1000 Mbps 算；可在<router-link :to="`/servers/${sv.id}`">服务器详情</router-link>里设置</template></span>
          </div>
          <div class="f-ctl">
            <n-input-number v-model:value="tune.bandwidth_mbps" :min="10" :max="100000" :step="100" :show-button="false" placeholder="1000">
              <template #suffix><span class="f-unit">Mbps</span></template>
            </n-input-number>
          </div>
        </div>
        <div class="f-actions">
          <n-button :loading="tuneBusy && bbrTool === 'restore_net'" :disabled="!sv?.online || !sv?.switches?.allow_system_tune || tuneBusy || isRunning('restore_net')" @click="restoreTune">还原到优化前</n-button>
          <span class="spacer" />
          <n-button :disabled="!sv?.online || tuneBusy || isRunning('tune_net')" @click="previewTune">预览改动</n-button>
          <n-button type="primary" :loading="tuneBusy && bbrTool === 'tune_net'" :disabled="!sv?.online || !sv?.switches?.allow_system_tune || tuneBusy || isRunning('tune_net')" @click="applyTune">应用优化</n-button>
        </div>
        <p class="f-note">「还原」把所有项改回优化前的数值，并删除上面的配置文件。</p>
      </div>
    </section>
    <section class="s-group">
      <div class="s-head"><h2>改动预览</h2><p>预览、应用或还原时的输出，列出会修改哪些参数。</p></div>
      <div class="card s-card">
        <div v-if="!bbrRun" class="empty">点「预览改动」查看会修改哪些参数</div>
        <div v-else class="out-body">
          <task-log :key="bbrRun.task" :task-id="bbrRun.task" height="260px" @done="(st) => onDone(bbrTool, st)" />
          <tool-result v-if="bbrRun.data" tool="tune_net" :data="bbrRun.data" />
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.ok-text { color: var(--c-primary); }
.warn-text { color: var(--c-warning); }
.f-hint a { color: var(--c-primary); }
.seg { display: flex; }
.seg :deep(.n-radio-button) { flex: 1; text-align: center; }
.out-body { display: flex; flex-direction: column; gap: 14px; padding-bottom: 18px; }
</style>
