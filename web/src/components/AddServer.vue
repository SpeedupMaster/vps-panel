<script setup>
import { ref, computed, onBeforeUnmount, watch } from 'vue'
import { useRouter } from 'vue-router'
import { NDrawer, NDrawerContent, NInput, NButton, NForm, NFormItem, NRadioGroup, NRadio, NCheckbox, NTabs, NTabPane, NInputNumber, NAutoComplete } from 'naive-ui'
import { api } from '../api'
import { state, loadServers, ui } from '../store'
import { run } from '../helpers'
import { copy } from '../format'
import TaskLog from './TaskLog.vue'
import { drawerWidth } from '../mobile'

const props = defineProps({ show: Boolean })
const emit = defineEmits(['update:show'])
const router = useRouter()

const form = ref({ name: '', group: '', mode: 'node', cap_reality: true, cap_landing: false })
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
  if (v && !created.value) { form.value = { name: '', group: '', mode: 'node', cap_reality: true, cap_landing: false } }
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
        <h3 class="step">1 · 基本信息</h3>
        <n-form @submit.prevent label-placement="top">
          <n-form-item label="名称" required>
            <n-input v-model:value="form.name" placeholder="如 HK-01" maxlength="40" />
          </n-form-item>
          <n-form-item label="分组">
            <n-auto-complete v-model:value="form.group" :options="groups" placeholder="可选，例如 亚洲入口" />
          </n-form-item>
        </n-form>
        <p class="muted small" style="margin-top: -8px">地区、IP、系统信息会在 Agent 连上后自动识别，无需填写。</p>

        <h3 class="step">2 · 安装模式</h3>
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
        <div v-if="form.mode === 'node'" class="caps">
          <div class="muted small" style="margin-bottom: 6px">节点能力（可多选）</div>
          <n-checkbox v-model:checked="form.cap_reality">Reality 入站（直连 / 入口）</n-checkbox>
          <p class="muted small cap-note">客户端连接的节点，默认端口 443。创建后可在「线路」里添加直连或链式线路。</p>
          <n-checkbox v-model:checked="form.cap_landing">SS2022 落地</n-checkbox>
          <p class="muted small cap-note">只接收入口机流量，端口随机，防火墙只放行入口机 IP。</p>
        </div>
        <n-button type="primary" block size="large" style="margin-top: 20px" :loading="busy" @click="create">创建并获取安装命令</n-button>
      </template>

      <template v-else>
        <h3 class="step">3 · 安装</h3>
        <n-tabs type="segment" animated>
          <n-tab-pane name="cmd" tab="一键命令">
            <p class="small">用 root 登录 VPS，执行下面的命令：</p>
            <pre class="pre cmd">{{ created.command }}</pre>
            <div class="row" style="margin-top: 8px">
              <n-button type="primary" size="small" @click="doCopy">复制命令</n-button>
              <span class="muted small">{{ left }} 后失效 · 只能使用一次</span>
              <span class="spacer" />
              <n-button size="small" quaternary @click="regen">重新生成</n-button>
            </div>
            <p class="muted small">支持 Debian 11+ / Ubuntu 20.04+ / Rocky·Alma 9 / Alpine 3.18+ · amd64 / arm64</p>
          </n-tab-pane>
          <n-tab-pane name="ssh" tab="SSH 一键安装">
            <n-form @submit.prevent label-placement="top" size="small">
              <div class="ssh-grid">
                <n-form-item label="地址"><n-input v-model:value="ssh.host" placeholder="IP 或域名" /></n-form-item>
                <n-form-item label="端口"><n-input-number v-model:value="ssh.port" :min="1" :max="65535" :show-button="false" /></n-form-item>
              </div>
              <n-form-item label="用户"><n-input v-model:value="ssh.user" /></n-form-item>
              <n-form-item label="密码"><n-input v-model:value="ssh.password" type="password" show-password-on="click" :input-props="{ autocomplete: 'off' }" /></n-form-item>
              <n-form-item label="或私钥（PEM）"><n-input v-model:value="ssh.key" type="textarea" :rows="3" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" /></n-form-item>
            </n-form>
            <p class="muted small">凭据只在本次安装时使用，安装结束后立即丢弃，不写入数据库或日志。</p>
            <n-button type="primary" block :disabled="!!sshTask" @click="sshInstall">连接并安装</n-button>
            <task-log v-if="sshTask" :task-id="sshTask" height="260px" style="margin-top: 12px" />
          </n-tab-pane>
        </n-tabs>

        <h3 class="step">4 · 安装进度</h3>
        <div class="progress">
          <div class="row"><span :class="['dot', connected ? 'ok' : 'warn']" />
            <span v-if="connected">Agent 已连接{{ live.ipv4 ? ' · ' + live.ipv4 : '' }}{{ live.agent_version ? ' · ' + live.agent_version : '' }}</span>
            <span v-else>等待 Agent 连接…</span>
          </div>
          <div v-if="created.server.mode === 'node'" class="row"><span :class="['dot', live?.xray_running ? 'ok' : 'off']" />
            <span>{{ live?.xray_running ? 'Xray 已运行' : 'Xray 将在 Agent 连接后自动安装并下发配置' }}</span>
          </div>
        </div>
        <div class="row" style="margin-top: 20px">
          <n-button @click="close">稍后再说</n-button>
          <n-button v-if="connected" @click="openDetail">查看详情</n-button>
          <span class="spacer" />
          <n-button v-if="created.server.mode === 'node' && created.server.cap_reality" type="primary" :disabled="!connected" @click="newRoute">为它新建线路</n-button>
        </div>
      </template>
    </n-drawer-content>
  </n-drawer>
</template>

<style scoped>
.step { font-size: 15px; margin: 4px 0 12px; }
.step:not(:first-child) { margin-top: 24px; }
.mode-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
.mode { border: 1px solid var(--c-border-strong); border-radius: 6px; padding: 12px; display: flex; flex-direction: column; gap: 6px; cursor: pointer; }
.mode.on { border-color: var(--c-primary); background: var(--c-primary-soft); }
.caps { margin-top: 14px; display: flex; flex-direction: column; }
.cap-note { margin: 2px 0 10px 24px; }
.cmd { white-space: pre-wrap; word-break: break-all; user-select: all; }
.ssh-grid { display: grid; grid-template-columns: 1fr 110px; gap: 10px; }
.progress { display: flex; flex-direction: column; gap: 8px; font-size: 13px; }
@media (max-width: 767px) { .mode-grid { grid-template-columns: 1fr; } }
</style>
