<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NSelect, NTabs, NTabPane, NInput, NInputNumber, NModal, NSwitch, NCheckbox, NRadioGroup, NRadioButton } from 'naive-ui'
import { api } from '../api'
import { state, loadServers, ui } from '../store'
import { run, ask } from '../helpers'
import { dt, ago, flag } from '../format'
import TaskLog from '../components/TaskLog.vue'
import ToolResult from '../components/ToolResult.vue'
import WebShell from '../components/WebShell.vue'

const route = useRoute()
const router = useRouter()
const server = ref(Number(route.query.server) || null)
const tab = ref(route.query.tab || 'check')
const sv = computed(() => state.servers.find((s) => s.id === server.value))
const serverOpts = computed(() => state.servers.filter((s) => s.status !== 'pending').map((s) => ({ label: `${s.name}${s.online ? '' : '（离线）'}`, value: s.id })))
onMounted(async () => {
  if (!state.serversLoaded) await loadServers().catch(() => {})
  if (!server.value && state.servers.length) server.value = (state.servers.find((s) => s.online) || state.servers[0]).id
  loadLatest()
  // Links may ask to run right away (?run=1); otherwise just show the last result.
  if (route.query.run && sv.value?.online && selected.value !== 'iperf') runTool(selected.value)
})
watch(server, (v) => { router.replace({ query: { ...route.query, server: v, run: undefined } }); runs.value = {}; loadLatest(); loadHistory() })
watch(tab, (t) => {
  router.replace({ query: { ...route.query, tab: t, run: undefined } })
  if (t === 'history') loadHistory()
  if (t === 'scripts') loadScripts()
  if (t === 'schedules') loadSchedules()
})

// ---------- checks ----------
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
const selected = ref(toolDefs.some((t) => t.id === route.query.tool) ? String(route.query.tool) : 'diagnose')
watch(selected, (t) => router.replace({ query: { ...route.query, tool: t, run: undefined } }))
// Runs started on this page, by tool: { task, status, data }. Several tools can run at once.
const runs = ref({})
const latest = ref({})
async function loadLatest() { if (server.value) latest.value = (await api.get(`/servers/${server.value}/tool-results`).catch(() => ({}))) || {} }
const isRunning = (tool) => runs.value[tool]?.status === 'running'
function startRun(tool, task) { runs.value = { ...runs.value, [tool]: { task, status: 'running', data: null } } }
async function runTool(tool, args) {
  if (!server.value || isRunning(tool)) return
  const r = await run(() => api.post(`/servers/${server.value}/tools/${tool}`, args || {}))
  if (r) startRun(tool, r.task_id)
}
async function onDone(tool, status) {
  const cur = runs.value[tool]
  if (!cur) return
  const task = cur.task
  const t = await api.get(`/tasks/${task}`).catch(() => null)
  if (runs.value[tool]?.task !== task) return
  runs.value = { ...runs.value, [tool]: { ...cur, status, data: t?.data ?? null } }
  if (status === 'success' || status === 'finished') loadLatest()
}
const toolName = (id) => toolDefs.find((t) => t.id === id)?.name || { tune_net: '网络优化', restore_net: '还原网络参数' }[id] || id
const iperfTo = ref(null)
async function runIperf() {
  if (!iperfTo.value) { ui.message.warning('选择对端'); return }
  if (isRunning('iperf')) return
  const r = await run(() => api.post('/tools/iperf', { from: server.value, to: iperfTo.value }))
  if (r) startRun('iperf', r.task_id)
}
function execSelected() { selected.value === 'iperf' ? runIperf() : runTool(selected.value) }
// What the result area shows: this page's finished run, or else the last saved result.
const sel = computed(() => runs.value[selected.value])
const shown = computed(() => {
  const r = sel.value
  if (r?.status === 'running') return null
  if (r?.data) return { data: r.data, fresh: true }
  const l = latest.value[selected.value]
  return l ? { data: l.data, ts: l.ts, failed: !!r } : null
})

// ---------- bbr ----------
const bbrTool = ref('tune_net')
const bbrRun = computed(() => runs.value[bbrTool.value])
const tune = ref({ profile: 'balanced', bandwidth_mbps: 1000 })
const profiles = [{ label: '保守', value: 'conservative' }, { label: '均衡', value: 'balanced' }, { label: '激进', value: 'aggressive' }]
function tuneArgs(dry) { return { profile: tune.value.profile, bandwidth_mbps: String(tune.value.bandwidth_mbps || 1000), dry_run: dry ? '1' : '0' } }
async function previewTune() { bbrTool.value = 'tune_net'; await runTool('tune_net', tuneArgs(true)) }
async function applyTune() {
  if (!(await ask('应用网络优化', '会写入 /etc/sysctl.d/99-vpspanel.conf 并立即生效，原值自动备份，可随时还原。'))) return
  bbrTool.value = 'tune_net'
  runTool('tune_net', tuneArgs(false))
}
async function restoreTune() {
  if (!(await ask('还原网络参数', '把所有项改回优化前的数值并删除 /etc/sysctl.d/99-vpspanel.conf。'))) return
  bbrTool.value = 'restore_net'
  runTool('restore_net')
}

// ---------- history ----------
const history = ref([])
const hTool = ref('')
const compare = ref(new Set())
const cmp = ref({ show: false, items: [] })
async function loadHistory() {
  const q = new URLSearchParams()
  if (server.value) q.set('server_id', server.value)
  if (hTool.value) q.set('tool', hTool.value)
  history.value = (await run(() => api.get('/tool-results?' + q))) || []
  compare.value = new Set()
}
watch(hTool, loadHistory)
function toggleCmp(id, v) { const s = new Set(compare.value); v ? s.add(id) : s.delete(id); compare.value = s }
function showCompare() { cmp.value = { show: true, items: history.value.filter((h) => compare.value.has(h.id)) } }
const hToolOpts = [{ label: '全部工具', value: '' }, ...['diagnose', 'unlock', 'ip_quality', 'trace', 'speedtest', 'reality_scan', 'iperf', 'tune_net'].map((v) => ({ label: toolName(v), value: v }))]

// ---------- scripts ----------
const scripts = ref([])
const script = ref({ id: 0, name: '', content: '' })
const targets = ref([])
const timeout = ref(300)
const scriptRun = ref({ tasks: [] })
async function loadScripts() { scripts.value = (await run(() => api.get('/scripts'))) || [] }
function pickScript(s) { script.value = s ? { ...s } : { id: 0, name: '', content: '#!/bin/sh\n' } }
async function saveScript() {
  const s = script.value
  if (!s.name || !s.content) { ui.message.warning('填写名称和内容'); return }
  const r = await run(() => (s.id ? api.put(`/scripts/${s.id}`, s) : api.post('/scripts', s)), '已保存')
  if (r) { script.value = { ...r }; loadScripts() }
}
async function delScript(s) {
  if (!(await ask('删除脚本', `删除「${s.name}」？`, { danger: true }))) return
  if (await run(() => api.del(`/scripts/${s.id}`), '已删除')) { if (script.value.id === s.id) pickScript(null); loadScripts() }
}
const execServers = computed(() => state.servers.filter((s) => s.online))
async function runScript() {
  if (!targets.value.length || !script.value.content) { ui.message.warning('选择服务器并填写脚本'); return }
  const names = targets.value.map((id) => state.servers.find((s) => s.id === id)?.name).join('、')
  if (!(await ask('执行脚本', `在 ${names} 上以 root 执行「${script.value.name || '临时脚本'}」，超时 ${timeout.value} 秒。`))) return
  const r = await run(() => api.post('/scripts/run', { script_id: script.value.id, content: script.value.id ? '' : script.value.content, server_ids: targets.value, timeout: timeout.value }, { confirm: '远程执行需要确认身份' }))
  if (r) scriptRun.value = { tasks: (r.task_ids || []).map((id, i) => ({ id, server: targets.value[i] })) }
}
const dirty = computed(() => { const o = scripts.value.find((s) => s.id === script.value.id); return o && o.content !== script.value.content })

// ---------- schedules ----------
const schedules = ref([])
const actions = ref({})
const sched = ref({ show: false, s: {} })
async function loadSchedules() {
  schedules.value = (await run(() => api.get('/schedules'))) || []
  if (!Object.keys(actions.value).length) actions.value = (await api.get('/tools').catch(() => ({})))?.schedule_actions || {}
}
const serverSpec = [{ label: '全部服务器', value: 'all' }, { label: '全部节点', value: 'nodes' }, { label: '入口机', value: 'entries' }, { label: '落地机', value: 'landings' }, { label: '指定服务器', value: 'ids' }]
function editSched(s) {
  const base = s ? { ...s } : { name: '', cron: '04:00', action: 'update_geo', servers: 'nodes', enabled: true }
  const isIds = !['all', 'nodes', 'entries', 'landings', ''].includes(base.servers)
  sched.value = { show: true, s: { ...base, spec: isIds ? 'ids' : base.servers || 'all', ids: isIds ? base.servers.split(',').map(Number).filter(Boolean) : [] } }
}
async function saveSched() {
  const s = sched.value.s
  const body = { id: s.id, name: s.name || actions.value[s.action], cron: s.cron, action: s.action, servers: s.spec === 'ids' ? s.ids.join(',') : s.spec, enabled: s.enabled }
  if (await run(() => (s.id ? api.put(`/schedules/${s.id}`, body) : api.post('/schedules', body)), '已保存')) { sched.value.show = false; loadSchedules() }
}
async function delSched(s) {
  if (!(await ask('删除定时任务', `删除「${s.name}」？`, { danger: true }))) return
  if (await run(() => api.del(`/schedules/${s.id}`), '已删除')) loadSchedules()
}
async function runSchedNow(s) { if (await run(() => api.post(`/schedules/${s.id}/run`), '已开始执行')) setTimeout(loadSchedules, 1500) }
async function toggleSched(s, v) { if (await run(() => api.put(`/schedules/${s.id}`, { ...s, enabled: v }))) s.enabled = v }
function specText(v) {
  const o = serverSpec.find((x) => x.value === v)
  if (o) return o.label
  return String(v).split(',').map((id) => state.servers.find((s) => s.id === Number(id))?.name || '#' + id).join('、')
}
function cronText(c) {
  if (c.startsWith('@every')) return '每 ' + c.slice(7).replace('h', ' 小时').replace('m', ' 分钟')
  const [t, d] = c.split('@')
  if (!d) return '每天 ' + t
  return '每周' + d.split(',').map((x) => '日一二三四五六'[Number(x) % 7]).join('、') + ' ' + t
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div class="ph"><h1>工具</h1><p class="page-desc">在选定的服务器上执行检测、网络优化、终端和脚本。</p></div>
      <span class="spacer" />
      <n-select v-model:value="server" :options="serverOpts" filterable style="width: 200px" placeholder="选择服务器" />
    </div>
    <div v-if="sv?.blocked" class="banner">
      <span>{{ sv.name }} 疑似 IP 被封。</span><span class="spacer" />
      <n-button size="small" @click="router.push({ name: 'latency' })">查看回程延迟</n-button>
      <n-button size="small" @click="router.push({ name: 'routes' })">管理线路</n-button>
    </div>

    <n-tabs v-model:value="tab" type="line" animated>
      <n-tab-pane name="check" tab="检测">
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
      </n-tab-pane>

      <n-tab-pane name="bbr" tab="网络优化">
        <div class="sections">
          <section class="s-group">
          <div class="s-head"><h2>BBR 与 TCP 缓冲区</h2><p>开启 BBR，并按带宽和回程延迟设置 TCP 缓冲区上限。改动写入 /etc/sysctl.d/99-vpspanel.conf，应用前自动备份原值。</p></div>
          <div class="card s-card">
            <div v-if="sv && !sv.switches?.allow_system_tune" class="banner warn">此机器的 Agent 关闭了系统调优：在 VPS 上执行 <code class="mono">vpsagent config set allow_system_tune true</code> 后重启 Agent。</div>
            <div v-else-if="sv?.system?.virt && ['openvz', 'lxc'].includes(sv.system.virt)" class="banner warn">此机器是 {{ sv.system.virt }} 容器，无法修改拥塞控制。</div>
            <div class="field f-inline">
              <div class="f-label"><span class="f-name">当前</span><span class="f-hint">可用：{{ sv?.system?.avail_cc || '—' }}</span></div>
              <div class="f-ctl"><span class="mono">{{ sv?.system?.congestion || '—' }} · {{ sv?.system?.qdisc || '—' }}</span></div>
            </div>
            <div class="field">
              <div class="f-label"><span class="f-name">档位</span><span class="f-hint">TCP 缓冲区上限：保守为带宽时延积的 1 倍，均衡 2 倍，激进 4 倍</span></div>
              <div class="f-ctl"><n-radio-group v-model:value="tune.profile" class="seg"><n-radio-button v-for="p in profiles" :key="p.value" :value="p.value">{{ p.label }}</n-radio-button></n-radio-group></div>
            </div>
            <div class="field">
              <div class="f-label"><span class="f-name">带宽</span><span class="f-hint">缓冲区按 带宽 × 回程 RTT 计算，RTT 自动取这台机器的回程延迟</span></div>
              <div class="f-ctl">
                <n-input-number v-model:value="tune.bandwidth_mbps" :min="10" :max="100000" :step="100" :show-button="false" placeholder="默认 1000">
                  <template #suffix><span class="f-unit">Mbps</span></template>
                </n-input-number>
              </div>
            </div>
            <div class="f-actions">
              <n-button :disabled="!sv?.online || !sv?.switches?.allow_system_tune" @click="restoreTune">还原到优化前</n-button>
              <span class="spacer" />
              <n-button :disabled="!sv?.online" @click="previewTune">预览改动</n-button>
              <n-button type="primary" :disabled="!sv?.online || !sv?.switches?.allow_system_tune" @click="applyTune">应用优化</n-button>
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
      </n-tab-pane>

      <n-tab-pane name="history" tab="历史结果">
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
      </n-tab-pane>

      <n-tab-pane name="shell" tab="终端">
        <div class="sec-head"><div class="s-head"><h2>终端</h2><p>打开终端需要二次验证。每次会话写入审计日志（不记录终端内容），关闭页面即断开。</p></div></div>
        <div class="card s-card">
          <div v-if="!sv" class="empty">选择服务器</div>
          <template v-else-if="!sv.switches?.allow_remote_exec">
            <div class="field stacked">
              <div class="f-label"><span class="f-name">远程命令未开启</span><span class="f-hint">为了防止面板被攻破后控制所有 VPS，远程命令默认关闭，只能在 VPS 本机上开启。SSH 登录这台机器后执行：</span></div>
              <div class="f-ctl"><pre class="pre">vpsagent config set allow_remote_exec true &amp;&amp; systemctl restart vpsagent</pre></div>
            </div>
            <p class="f-note">开启后刷新本页即可使用；关闭方法相同，把 true 改为 false。</p>
          </template>
          <div v-else class="s-pad"><web-shell :key="sv.id" :server-id="sv.id" /></div>
        </div>
      </n-tab-pane>

      <n-tab-pane name="scripts" tab="脚本">
        <div class="sec-head"><div class="s-head"><h2>脚本</h2><p>在多台服务器上批量执行脚本。脚本按 sha256 校验后执行，输出按机器分栏显示。</p></div></div>
        <div class="scripts">
          <div class="card s-card">
            <div class="s-card-head"><h3>脚本库</h3><span class="spacer" /><n-button size="small" @click="pickScript(null)">新建</n-button></div>
            <div v-if="!scripts.length" class="empty">还没有脚本</div>
            <div v-else class="s-list">
              <div v-for="s in scripts" :key="s.id" :class="['s-item', { on: script.id === s.id }]" @click="pickScript(s)">
                <span>{{ s.name }}</span><span class="spacer" />
                <n-button size="tiny" quaternary type="error" @click.stop="delScript(s)">删除</n-button>
              </div>
            </div>
          </div>
          <div class="card s-card">
            <div class="field">
              <div class="f-label"><span class="f-name">名称<i v-if="dirty" class="f-dot" /></span><span v-if="dirty" class="f-hint">有未保存的修改，执行时用脚本库里的版本</span></div>
              <div class="f-ctl"><n-input v-model:value="script.name" placeholder="脚本名称" /></div>
            </div>
            <div class="field stacked">
              <div class="f-label"><span class="f-name">内容</span></div>
              <div class="f-ctl"><n-input v-model:value="script.content" type="textarea" :rows="12" class="mono" placeholder="#!/bin/sh" aria-label="脚本内容" /></div>
            </div>
            <div class="f-actions"><span class="spacer" /><n-button @click="saveScript">保存到脚本库</n-button></div>
            <div class="field">
              <div class="f-label"><span class="f-name">执行的服务器</span><span class="f-hint">只列出在线的；未开启远程执行的不能选</span></div>
              <div class="f-ctl"><n-select v-model:value="targets" multiple placeholder="选择服务器" :options="execServers.map((s) => ({ label: s.name + (s.switches?.allow_remote_exec ? '' : '（未开启远程执行）'), value: s.id, disabled: !s.switches?.allow_remote_exec }))" /></div>
            </div>
            <div class="field">
              <div class="f-label"><span class="f-name">超时</span><span class="f-hint">10–3600 秒</span></div>
              <div class="f-ctl"><n-input-number v-model:value="timeout" :min="10" :max="3600" :show-button="false"><template #suffix><span class="f-unit">秒</span></template></n-input-number></div>
            </div>
            <div class="f-actions"><span class="spacer" /><n-button type="primary" :disabled="!targets.length" @click="runScript">执行</n-button></div>
          </div>
        </div>
        <div v-if="scriptRun.tasks.length" class="outs">
          <div v-for="t in scriptRun.tasks" :key="t.id" class="card s-card">
            <div class="s-card-head"><h3>{{ state.servers.find((s) => s.id === t.server)?.name }}</h3></div>
            <div class="out-body"><task-log :task-id="t.id" height="300px" /></div>
          </div>
        </div>
      </n-tab-pane>

      <n-tab-pane name="schedules" tab="定时任务">
        <div class="sec-head">
          <div class="s-head"><h2>定时任务</h2><p>按面板所在时区定时执行检测、更新或脚本。</p></div>
          <span class="spacer" />
          <n-button type="primary" @click="editSched(null)">新建定时任务</n-button>
        </div>
        <div class="card s-card">
          <div v-if="!schedules.length" class="empty">没有定时任务</div>
          <div v-else class="table-wrap">
            <table class="plain">
              <thead><tr><th>名称</th><th>时间</th><th>动作</th><th>服务器</th><th>上次执行</th><th>启用</th><th /></tr></thead>
              <tbody>
                <tr v-for="s in schedules" :key="s.id">
                  <td>{{ s.name }}</td><td class="nowrap">{{ cronText(s.cron) }}</td><td>{{ actions[s.action] || s.action }}</td>
                  <td class="small">{{ specText(s.servers) }}</td><td class="nowrap small">{{ s.last_run && !s.last_run.startsWith('0001') ? dt(s.last_run) : '—' }}</td>
                  <td><n-switch size="small" :value="s.enabled" @update:value="(v) => toggleSched(s, v)" /></td>
                  <td class="nowrap" style="text-align: right">
                    <n-button size="tiny" quaternary @click="runSchedNow(s)">立即执行</n-button>
                    <n-button size="tiny" quaternary @click="editSched(s)">编辑</n-button>
                    <n-button size="tiny" quaternary type="error" @click="delSched(s)">删除</n-button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </n-tab-pane>
    </n-tabs>

    <n-modal v-model:show="cmp.show" preset="card" title="结果对比" style="width: min(1100px, 96vw)">
      <div class="cmp">
        <div v-for="h in cmp.items" :key="h.id" class="card">
          <div class="small muted">{{ dt(h.ts, true) }} · {{ toolName(h.tool) }}</div>
          <div class="small" style="margin: 4px 0 10px">{{ h.summary }}</div>
          <tool-result :tool="h.tool" :data="h.data" />
        </div>
      </div>
    </n-modal>

    <n-modal v-model:show="sched.show" preset="card" :title="sched.s.id ? '编辑定时任务' : '新建定时任务'" style="width: min(480px, 96vw)">
      <div class="m-form">
        <div class="field">
          <div class="f-label"><span class="f-name">动作</span></div>
          <div class="f-ctl"><n-select v-model:value="sched.s.action" :options="Object.entries(actions).map(([value, label]) => ({ label, value }))" /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">名称</span></div>
          <div class="f-ctl"><n-input v-model:value="sched.s.name" :placeholder="'默认 ' + (actions[sched.s.action] || '')" /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">时间</span><span class="f-hint">「HH:MM」每天；「HH:MM@1,3」每周一、三；「@every 6h」每 6 小时。按面板所在时区</span></div>
          <div class="f-ctl"><n-input v-model:value="sched.s.cron" placeholder="04:00" /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">服务器</span></div>
          <div class="f-ctl ctl-col"><n-select v-model:value="sched.s.spec" :options="serverSpec" />
          <n-select v-if="sched.s.spec === 'ids'" v-model:value="sched.s.ids" multiple :options="serverOpts" placeholder="选择服务器" /></div>
        </div>
        <div class="field f-bool">
          <div class="f-label"><span class="f-name">启用</span></div>
          <div class="f-ctl"><n-switch v-model:value="sched.s.enabled" aria-label="启用" /></div>
        </div>
      </div>
      <template #footer><div class="m-foot"><n-button @click="sched.show = false">取消</n-button><n-button type="primary" @click="saveSched">保存</n-button></div></template>
    </n-modal>
  </div>
</template>

<style scoped>
.tools-grid { display: grid; grid-template-columns: 320px minmax(0, 1fr); gap: 16px; }
.tool-list { display: flex; flex-direction: column; gap: 8px; }
.tool { text-align: left; font: inherit; color: inherit; background: #fff; border: 1px solid var(--c-border); border-radius: 6px; padding: 10px 12px; display: flex; flex-direction: column; gap: 3px; cursor: pointer; }
.tool:hover:not(:disabled) { border-color: var(--c-primary); }
.tool.on { border-color: var(--c-primary); background: var(--c-primary-soft); }
.tool:disabled { opacity: .55; cursor: not-allowed; }
.t-title { display: flex; align-items: center; gap: 8px; }
.t-run { display: inline-flex; align-items: center; gap: 5px; font-size: 12px; color: var(--c-warning); }
.tool .last { color: var(--c-text-2); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.seg { display: flex; }
.seg :deep(.n-radio-button) { flex: 1; text-align: center; }
.out-body { display: flex; flex-direction: column; gap: 14px; padding-bottom: 18px; }
.log > summary { cursor: pointer; user-select: none; padding: 2px 0; }
.log[open] > summary { margin-bottom: 8px; }
.scripts { display: grid; grid-template-columns: 260px minmax(0, 1fr); gap: 16px; align-items: start; }
.s-list { display: flex; flex-direction: column; gap: 2px; padding-bottom: 12px; margin: 0 -8px; }
.s-item { display: flex; align-items: center; padding: 8px 10px; border-radius: 6px; cursor: pointer; font-size: 13px; min-height: 40px; }
.s-item:hover { background: var(--c-surface); }
.s-item.on { background: var(--c-primary-soft); color: var(--c-primary); }
.outs { display: grid; grid-template-columns: repeat(auto-fit, minmax(360px, 1fr)); gap: 16px; margin-top: 16px; }
.cmp { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 12px; }
p { line-height: 1.6; }
@media (max-width: 900px) { .tools-grid, .scripts { grid-template-columns: 1fr; } .outs { grid-template-columns: 1fr; } }
</style>
