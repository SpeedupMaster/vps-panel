<script setup>
import { ref, computed, onBeforeUnmount, watch } from 'vue'
import { useRouter } from 'vue-router'
import { NDrawer, NDrawerContent, NInput, NButton, NRadioGroup, NRadio, NSwitch, NTabs, NTabPane, NInputNumber, NAutoComplete } from 'naive-ui'
import { api } from '../api'
import { state, loadServers, ui } from '../store'
import { run } from '../helpers'
import { copy } from '../format'
import TaskLog from './TaskLog.vue'
import { drawerWidth } from '../mobile'

const props = defineProps({ show: Boolean })
const emit = defineEmits(['update:show'])
const router = useRouter()

const blankForm = () => ({ name: '', group: '', mode: 'node', cap_reality: true, cap_landing: false, reality_target: '', reality_server_names: '' })
const form = ref(blankForm())
// Reality target / SNI: empty fields use the default target from Settings.
const defaultTarget = ref('www.microsoft.com')
const targetHolder = computed(() => defaultTarget.value + (defaultTarget.value.includes(':') ? '' : ':443'))
const sniHolder = computed(() => (form.value.reality_target.trim() || defaultTarget.value).split(':')[0])
const created = ref(null) // {server, token, command, expires_at}
const busy = ref(false)
const now = ref(Date.now())
const timer = setInterval(() => { now.value = Date.now() }, 1000)
onBeforeUnmount(() => clearInterval(timer))
const ssh = ref({ host: '', port: 22, user: 'root', password: '', key: '' })
const sshTask = ref(null)

const groups = computed(() => [...new Set(state.servers.map((s) => s.group).filter(Boolean))].map((g) => ({ label: g, value: g })))
const left = computed(() => {
  if (!created.value) return ''
  const s = Math.max(0, Math.floor((new Date(created.value.expires_at).getTime() - now.value) / 1000))
  return `${String(Math.floor(s / 60)).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`
})
const live = computed(() => created.value && state.servers.find((s) => s.id === created.value.server.id))
const connected = computed(() => live.value && (live.value.online || live.value.status === 'online'))

watch(() => props.show, (v) => {
  if (v && !created.value) {
    form.value = blankForm()
    api.get('/settings').then((st) => { if (st?.reality_default_target) defaultTarget.value = st.reality_default_target }).catch(() => {})
  }
})
// Poll the list until the agent connects (SSE also updates it).
let poll = null
watch(created, (c) => {
  clearInterval(poll)
  if (c) poll = setInterval(() => { if (!connected.value) loadServers().catch(() => {}) }, 5000)
})
watch(connected, (v) => { if (v) clearInterval(poll) })
onBeforeUnmount(() => clearInterval(poll))

async function create() {
  if (!form.value.name.trim()) { ui.message.warning('请填写名称'); return }
  if (form.value.mode === 'node' && !form.value.cap_reality && !form.value.cap_landing) { ui.message.warning('节点至少选择一种能力'); return }
  busy.value = true
  const body = { ...form.value, name: form.value.name.trim() }
  if (body.mode === 'probe') { body.cap_reality = false; body.cap_landing = false }
  if (!body.cap_reality) { delete body.reality_target; delete body.reality_server_names }
  const r = await run(() => api.post('/servers', body))
  busy.value = false
  if (r) { created.value = r; loadServers() }
}
async function regen() {
  const r = await run(() => api.post(`/servers/${created.value.server.id}/token`), '已生成新命令，旧命令失效')
  if (r) created.value = { ...created.value, token: r.token, command: r.command, expires_at: new Date(Date.now() + 30 * 60000).toISOString() }
}
async function sshInstall() {
  const s = ssh.value
  if (!s.host || (!s.password && !s.key)) { ui.message.warning('请填写地址和密码或私钥'); return }
  const r = await run(() => api.post(`/servers/${created.value.server.id}/ssh-install`, { ...s, mode: created.value.server.mode }))
  if (r) { sshTask.value = r.task_id; ssh.value.password = ''; ssh.value.key = '' }
}
async function doCopy() { if (await copy(created.value.command)) ui.message.success('已复制') }
function close() {
  emit('update:show', false)
  setTimeout(() => { created.value = null; sshTask.value = null }, 300)
}
function newRoute() { const id = created.value.server.id; close(); router.push({ name: 'route-new', query: { entry: id } }) }
function openDetail() { const id = created.value.server.id; close(); router.push({ name: 'server', params: { id } }) }
</script>

<template>
  <n-drawer :show="show" :width="drawerWidth(560)" placement="right" class="add-drawer" @update:show="(v) => !v && close()">
    <n-drawer-content title="添加服务器" closable :native-scrollbar="false">
      <template v-if="!created">
        <div class="step"><span class="step-n">1</span><div class="s-head"><h2>基本信息</h2><p>起个名字就行，其他信息在 Agent 连上后自动识别。</p></div></div>
        <div class="m-form">
          <div class="field">
            <div class="f-label"><span class="f-name">名称</span><span class="f-hint">必填</span></div>
            <div class="f-ctl"><n-input v-model:value="form.name" placeholder="如 HK-01" maxlength="40" /></div>
          </div>
          <div class="field">
            <div class="f-label"><span class="f-name">分组</span><span class="f-hint">可选，总览里可以按分组筛选</span></div>
            <div class="f-ctl"><n-auto-complete v-model:value="form.group" :options="groups" placeholder="未分组" /></div>
          </div>
        </div>

        <div class="step"><span class="step-n">2</span><div class="s-head"><h2>安装模式</h2><p>只做监控选探针；要承载代理流量或做中转选节点。</p></div></div>
        <n-radio-group v-model:value="form.mode" style="width: 100%">
          <div class="mode-grid">
            <label :class="['mode', { on: form.mode === 'probe' }]">
              <n-radio value="probe">仅探针</n-radio>
              <span class="muted small">只做监控，不安装 Xray。以后可在详情页一键升级为节点。</span>
            </label>
            <label :class="['mode', { on: form.mode === 'node' }]">
              <n-radio value="node">节点</n-radio>
              <span class="muted small">探针 + Xray。安装后由面板统一生成和下发配置。</span>
            </label>
          </div>
        </n-radio-group>
        <div v-if="form.mode === 'node'" class="m-form caps">
          <div class="field f-bool">
            <div class="f-label"><span class="f-name">Reality 入站</span><span class="f-hint">直连或入口：客户端连接的节点，默认端口 443。创建后可在「线路」里添加线路</span></div>
            <div class="f-ctl"><n-switch v-model:value="form.cap_reality" aria-label="Reality 入站" /></div>
          </div>
          <template v-if="form.cap_reality">
            <div class="field">
              <div class="f-label"><span class="f-name">目标站</span><span class="f-hint">Reality 伪装的网站，host:port。留空用设置里的默认目标；可在「工具 → Reality 目标扫描」里挑选</span></div>
              <div class="f-ctl"><n-input v-model:value="form.reality_target" :placeholder="targetHolder" aria-label="目标站" /></div>
            </div>
            <div class="field">
              <div class="f-label"><span class="f-name">SNI</span><span class="f-hint">客户端握手用的域名，多个用逗号分隔，订阅里用第一个。留空跟随目标站</span></div>
              <div class="f-ctl"><n-input v-model:value="form.reality_server_names" :placeholder="sniHolder" aria-label="SNI" /></div>
            </div>
          </template>
          <div class="field f-bool">
            <div class="f-label"><span class="f-name">SS2022 落地</span><span class="f-hint">只接收入口机流量，端口随机，防火墙只放行入口机 IP</span></div>
            <div class="f-ctl"><n-switch v-model:value="form.cap_landing" aria-label="SS2022 落地" /></div>
          </div>
        </div>
        <n-button type="primary" block size="large" class="go" :loading="busy" @click="create">创建并获取安装命令</n-button>
      </template>

      <template v-else>
        <div class="created">
          <span class="dot ok" />
          <span>已创建 <b>{{ created.server.name }}</b> · {{ created.server.mode === 'probe' ? '探针' : [created.server.cap_reality && 'Reality', created.server.cap_landing && 'SS2022 落地'].filter(Boolean).join(' + ') }}</span>
        </div>
        <div class="step"><span class="step-n">3</span><div class="s-head"><h2>安装</h2><p>在 VPS 上执行一键命令，或让面板通过 SSH 代为安装。</p></div></div>
        <n-tabs type="segment" animated>
          <n-tab-pane name="cmd" tab="一键命令">
            <div class="m-form">
              <div class="field stacked">
                <div class="f-label"><span class="f-name">安装命令</span><span class="f-hint">用 root 登录 VPS 执行。{{ left }} 后失效，只能使用一次</span></div>
                <div class="f-ctl"><pre class="pre cmd">{{ created.command }}</pre></div>
              </div>
              <div class="f-actions">
                <n-button type="primary" @click="doCopy">复制命令</n-button>
                <span class="spacer" />
                <n-button quaternary @click="regen">重新生成</n-button>
              </div>
              <p class="f-note">支持 Debian 11+ / Ubuntu 20.04+ / Rocky·Alma 9 / Alpine 3.18+ · amd64 / arm64</p>
            </div>
          </n-tab-pane>
          <n-tab-pane name="ssh" tab="SSH 一键安装">
            <div class="m-form">
              <div class="field">
                <div class="f-label"><span class="f-name">地址</span><span class="f-hint">IP 或域名，后面是 SSH 端口</span></div>
                <div class="f-ctl ctl-row"><n-input v-model:value="ssh.host" placeholder="IP 或域名" aria-label="地址" />
                <n-input-number v-model:value="ssh.port" :min="1" :max="65535" :show-button="false" class="port" aria-label="端口" /></div>
              </div>
              <div class="field">
                <div class="f-label"><span class="f-name">用户</span></div>
                <div class="f-ctl"><n-input v-model:value="ssh.user" placeholder="默认 root" /></div>
              </div>
              <div class="field">
                <div class="f-label"><span class="f-name">密码</span><span class="f-hint">密码和私钥填一个</span></div>
                <div class="f-ctl"><n-input v-model:value="ssh.password" type="password" show-password-on="click" placeholder="未设置" :input-props="{ autocomplete: 'off' }" /></div>
              </div>
              <div class="field stacked">
                <div class="f-label"><span class="f-name">私钥（PEM）</span></div>
                <div class="f-ctl"><n-input v-model:value="ssh.key" type="textarea" :rows="3" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" aria-label="私钥" /></div>
              </div>
              <p class="f-note">凭据只在本次安装时使用，安装结束后立即丢弃，不写入数据库或日志。</p>
            </div>
            <n-button type="primary" block class="go" :disabled="!!sshTask" @click="sshInstall">连接并安装</n-button>
            <task-log v-if="sshTask" :task-id="sshTask" height="260px" style="margin-top: 12px" />
          </n-tab-pane>
        </n-tabs>

        <div class="step"><span class="step-n">4</span><div class="s-head"><h2>安装进度</h2><p>这里会自动刷新，装好后就能为它新建线路。</p></div></div>
        <div class="m-form">
          <div class="field f-inline">
            <div class="f-label">
              <span class="f-name">Agent</span>
              <span class="f-hint">{{ connected ? [live.ipv4, live.agent_version].filter(Boolean).join(' · ') || '已连接' : '执行安装命令后会自动连上' }}</span>
            </div>
            <div class="f-ctl"><span :class="['tag', connected ? 'ok' : 'warn']"><span :class="['dot', connected ? 'ok' : 'warn']" />{{ connected ? '已连接' : '等待连接' }}</span></div>
          </div>
          <div v-if="created.server.mode === 'node'" class="field f-inline">
            <div class="f-label">
              <span class="f-name">Xray</span>
              <span class="f-hint">{{ live?.xray_running ? (live.xray_version || '运行中') : 'Agent 连上后自动安装并下发配置' }}</span>
            </div>
            <div class="f-ctl"><span :class="['tag', live?.xray_running ? 'ok' : '']"><span :class="['dot', live?.xray_running ? 'ok' : 'off']" />{{ live?.xray_running ? '运行中' : '未运行' }}</span></div>
          </div>
          <div class="f-actions">
            <n-button @click="close">稍后再说</n-button>
            <n-button v-if="connected" @click="openDetail">查看详情</n-button>
            <span class="spacer" />
            <n-button v-if="created.server.mode === 'node' && created.server.cap_reality" type="primary" :disabled="!connected" @click="newRoute">为它新建线路</n-button>
          </div>
        </div>
      </template>
    </n-drawer-content>
  </n-drawer>
</template>

<style scoped>
.step { display: flex; gap: 12px; align-items: flex-start; margin: 2px 0 12px; }
.step:not(:first-child) { margin-top: 28px; }
.step .s-head { padding-top: 0; gap: 2px; }
.step-n { flex: none; width: 22px; height: 22px; border-radius: 50%; background: var(--c-primary-soft); color: var(--c-primary); font-size: 12px; font-weight: 600; display: inline-flex; align-items: center; justify-content: center; margin-top: 1px; }
.created { display: flex; align-items: center; gap: 8px; padding: 10px 12px; border-radius: 6px; background: var(--c-primary-soft); font-size: 13px; margin-bottom: 4px; }
.go { margin-top: 20px; }
.cmd { margin: 0; }
.mode-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
.mode { border: 1px solid var(--c-border-strong); border-radius: 6px; padding: 12px; display: flex; flex-direction: column; gap: 6px; cursor: pointer; }
.mode.on { border-color: var(--c-primary); background: var(--c-primary-soft); }
.caps { margin-top: 14px; }
.cmd { white-space: pre-wrap; word-break: break-all; user-select: all; }
.f-ctl .tag { gap: 6px; }
.ctl-row .port { flex: none; width: 88px; }
@media (max-width: 767px) { .mode-grid { grid-template-columns: 1fr; } }
</style>
