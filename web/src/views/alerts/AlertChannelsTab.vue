<script setup>
import { ref, computed, onMounted } from 'vue'
import { NButton, NSelect, NSwitch, NModal, NInput, NTimePicker } from 'naive-ui'
import { api } from '../../api'
import { run, ask } from '../../helpers'
import { sevLabel } from '../../format'
import { useDirtyForm } from '../../composables/useDirtyForm'

const props = defineProps({ channels: { type: Array, required: true } })
const emit = defineEmits(['reload-channels'])

const fields = ref({})
const glob = ref({ quiet_on: false, quiet_from: '23:00', quiet_to: '08:00', recovery: true })
const globOrig = ref(JSON.stringify(glob.value))
const globDirty = computed(() => JSON.stringify(glob.value) !== globOrig.value)
useDirtyForm(globDirty)

const chTypes = { telegram: 'Telegram', bark: 'Bark', wecom: '企业微信', feishu: '飞书', email: '邮件', webhook: 'Webhook' }
const fieldLabel = { bot_token: 'Bot Token', chat_id: 'Chat ID', api_base: 'API 地址', bot_commands: '响应 Bot 命令', server: '服务器', device_key: 'Device Key', webhook: 'Webhook 地址', secret: '签名密钥', host: 'SMTP 主机', port: '端口', username: '用户名', password: '密码', from: '发件人', to: '收件人', url: 'URL' }
const fieldHint = { api_base: '可选，通过反代访问 Telegram 时填写', bot_commands: '开启后可以在 Telegram 里发命令查询状态', secret: '可选', to: '多个收件人用逗号分隔' }
const fieldPh = { api_base: '默认 https://api.telegram.org', server: '默认 https://api.day.app', port: '默认 465' }
const secretFields = ['bot_token', 'password', 'secret', 'device_key', 'webhook']
const sevOptsRule = [{ label: '严重', value: 'critical' }, { label: '警告', value: 'warning' }, { label: '提醒', value: 'info' }]
const chEdit = ref({ show: false, c: {}, savedSecrets: {} })

function editChannel(c) {
  if (!c) {
    chEdit.value = { show: true, c: { type: 'telegram', name: '', min_level: 'info', enabled: true, config: {} }, savedSecrets: {} }
    return
  }
  const cleanCfg = {}
  const savedSecrets = {}
  for (const [k, v] of Object.entries(c.config || {})) {
    // Strip backend mask sentinel ("••••••") so password inputs never reveal literal mask characters.
    if (secretFields.includes(k) && v) {
      savedSecrets[k] = true
      cleanCfg[k] = ''
    } else {
      cleanCfg[k] = v
    }
  }
  chEdit.value = { show: true, c: { ...c, config: cleanCfg }, savedSecrets }
}
async function saveChannel() {
  const c = chEdit.value.c
  const cfg = {}
  for (const [k, v] of Object.entries(c.config || {})) {
    if (secretFields.includes(k) && !v && chEdit.value.savedSecrets[k]) continue
    cfg[k] = v
  }
  const body = { type: c.type, name: c.name || chTypes[c.type], min_level: c.min_level, enabled: c.enabled, config: cfg }
  const r = await run(() => (c.id ? api.put(`/channels/${c.id}`, body) : api.post('/channels', body)), '已保存')
  if (r) { chEdit.value.show = false; emit('reload-channels') }
}
async function testChannel(c) { await run(() => api.post(`/channels/${c.id}/test`), '测试消息已发送') }
async function delChannel(c) {
  if (!(await ask('删除渠道', `删除「${c.name}」？`, { danger: true }))) return
  if (await run(() => api.del(`/channels/${c.id}`), '已删除')) emit('reload-channels')
}
async function toggleChannel(c, v) {
  if (await run(() => api.put(`/channels/${c.id}`, { type: c.type, name: c.name, min_level: c.min_level, enabled: v, config: {} }))) c.enabled = v
}
async function loadGlobal() {
  const s = await run(() => api.get('/settings'))
  if (!s) return
  const [a, b] = String(s.alert_quiet || '').split('-')
  glob.value = { quiet_on: !!s.alert_quiet, quiet_from: a || '23:00', quiet_to: b || '08:00', recovery: s.alert_recovery !== '0' }
  globOrig.value = JSON.stringify(glob.value)
}
async function saveGlobal() {
  const g = glob.value
  if (await run(() => api.put('/settings', { alert_quiet: g.quiet_on ? `${g.quiet_from}-${g.quiet_to}` : '', alert_recovery: g.recovery }), '已保存')) globOrig.value = JSON.stringify(g)
}
const botCmds = [
  ['/status', '所有服务器在线状态与告警概况'], ['/vps', '每台的 CPU、内存、网速'], ['/traffic', '本周期流量与额度'],
  ['/routes', '线路状态与延迟'], ['/sub', '订阅链接（仅发给绑定的 chat）'], ['/help', '命令列表'],
]

onMounted(() => {
  loadGlobal()
  api.get('/channels/fields').then((f) => { fields.value = f }).catch(() => {})
})
</script>

<template>
  <div class="sections">
    <section class="s-group">
      <div class="s-head"><h2>通知渠道</h2><p>告警通过这些渠道推送。没有渠道时，告警只显示在面板里。</p></div>
      <div class="card s-card">
        <div class="s-card-head">
          <span class="f-hint">{{ channels.length }} 个渠道</span>
          <span class="spacer" />
          <n-button size="small" type="primary" @click="editChannel(null)">添加渠道</n-button>
        </div>
        <div v-if="!channels.length" class="empty">还没有通知渠道</div>
        <div v-for="c in channels" :key="c.id" class="ch-row">
          <div class="ch-main">
            <span class="f-name">{{ c.name }} <span class="tag">{{ chTypes[c.type] || c.type }}</span></span>
            <span class="f-hint">最低级别：{{ sevLabel[c.min_level] || '提醒' }}{{ c.type === 'telegram' && c.config?.bot_commands === '1' ? ' · 响应 Bot 命令' : '' }}</span>
          </div>
          <div class="row ch-act">
            <n-button size="tiny" quaternary @click="testChannel(c)">发送测试</n-button>
            <n-button size="tiny" quaternary @click="editChannel(c)">编辑</n-button>
            <n-button size="tiny" quaternary type="error" @click="delChannel(c)">删除</n-button>
            <n-switch size="small" :value="c.enabled" :aria-label="'启用 ' + c.name" @update:value="(v) => toggleChannel(c, v)" />
          </div>
        </div>
      </div>
    </section>

    <section class="s-group">
      <div class="s-head"><h2>推送设置</h2><p>对所有渠道生效。</p></div>
      <div class="card s-card">
        <div class="field f-bool">
          <div class="f-label"><span class="f-name">静默时段</span><span class="f-hint">时段内只推送「严重」告警，其他级别只记录在面板里</span></div>
          <div class="f-ctl"><n-switch v-model:value="glob.quiet_on" aria-label="静默时段" /></div>
        </div>
        <div v-if="glob.quiet_on" class="field">
          <div class="f-label"><span class="f-name">时段</span><span class="f-hint">可以跨午夜，例如 23:00 – 08:00</span></div>
          <div class="f-ctl ctl-row">
            <n-time-picker v-model:formatted-value="glob.quiet_from" value-format="HH:mm" format="HH:mm" aria-label="开始时间" />
            <span class="f-unit">–</span>
            <n-time-picker v-model:formatted-value="glob.quiet_to" value-format="HH:mm" format="HH:mm" aria-label="结束时间" />
          </div>
        </div>
        <div class="field f-bool">
          <div class="f-label"><span class="f-name">合并发送</span><span class="f-hint">10 秒内的多条告警合并成一条消息，始终开启</span></div>
          <div class="f-ctl"><n-switch :value="true" disabled aria-label="合并发送" /></div>
        </div>
        <div class="field f-bool">
          <div class="f-label"><span class="f-name">恢复通知</span><span class="f-hint">告警恢复时发送一条，附持续时长</span></div>
          <div class="f-ctl"><n-switch v-model:value="glob.recovery" aria-label="恢复通知" /></div>
        </div>
        <div class="f-actions">
          <span v-if="globDirty" class="small dirty-note"><i class="f-dot" />有未保存的修改</span>
          <span class="spacer" />
          <n-button :disabled="!globDirty" @click="glob = JSON.parse(globOrig)">撤销</n-button>
          <n-button type="primary" :disabled="!globDirty" @click="saveGlobal">保存</n-button>
        </div>
      </div>
    </section>

    <section class="s-group">
      <div class="s-head">
        <h2>Telegram Bot 命令</h2>
        <p>在 Telegram 渠道里开启「响应 Bot 命令」后可用。只响应已绑定的 chat_id，其他人发消息不会有任何回应。</p>
      </div>
      <div class="card s-card">
        <div v-for="[c, d] in botCmds" :key="c" class="cmd-row"><code class="mono cmd">{{ c }}</code><span class="f-hint">{{ d }}</span></div>
      </div>
    </section>
  </div>

  <n-modal v-model:show="chEdit.show" preset="card" :title="chEdit.c.id ? '编辑渠道' : '添加渠道'" style="width: min(520px, 96vw)">
    <div class="m-form">
      <div class="field">
        <div class="f-label"><span class="f-name">类型</span><span v-if="chEdit.c.id" class="f-hint">创建后不能修改</span></div>
        <div class="f-ctl"><n-select v-model:value="chEdit.c.type" :options="Object.entries(chTypes).map(([value, label]) => ({ label, value }))" :disabled="!!chEdit.c.id" /></div>
      </div>
      <div class="field">
        <div class="f-label"><span class="f-name">名称</span></div>
        <div class="f-ctl"><n-input v-model:value="chEdit.c.name" :placeholder="'默认 ' + (chTypes[chEdit.c.type] || '')" /></div>
      </div>
      <template v-for="f in fields[chEdit.c.type] || []" :key="f">
        <div :class="['field', { 'f-bool': f === 'bot_commands' }]">
          <div class="f-label">
            <span class="f-name">{{ fieldLabel[f] || f }}</span>
            <span v-if="fieldHint[f] || (chEdit.c.id && chEdit.savedSecrets[f])" class="f-hint">{{ chEdit.c.id && chEdit.savedSecrets[f] ? '已配置（留空不修改）' : fieldHint[f] }}</span>
          </div>
          <div class="f-ctl">
            <n-switch v-if="f === 'bot_commands'" :value="chEdit.c.config[f] === '1'" :aria-label="fieldLabel[f]" @update:value="(v) => chEdit.c.config[f] = v ? '1' : '0'" />
            <n-input v-else v-model:value="chEdit.c.config[f]" :type="secretFields.includes(f) ? 'password' : 'text'" show-password-on="click"
              :placeholder="chEdit.c.id && chEdit.savedSecrets[f] ? '已配置（留空不修改）' : fieldPh[f] || ''" :input-props="{ autocomplete: 'off' }" />
          </div>
        </div>
      </template>
      <div class="field"><div class="f-label"><span class="f-name">最低级别</span><span class="f-hint">低于这个级别的告警不发到这里</span></div><div class="f-ctl"><n-select v-model:value="chEdit.c.min_level" :options="sevOptsRule" /></div></div>
      <div class="field f-bool"><div class="f-label"><span class="f-name">启用</span></div><div class="f-ctl"><n-switch v-model:value="chEdit.c.enabled" aria-label="启用" /></div></div>
    </div>
    <template #footer><div class="m-foot"><n-button @click="chEdit.show = false">取消</n-button><n-button type="primary" @click="saveChannel">保存</n-button></div></template>
  </n-modal>
</template>

<style scoped>
.ch-row { display: flex; align-items: center; gap: 8px 12px; padding: 12px 0; border-top: 1px solid var(--c-border); flex-wrap: wrap; }
.ch-main { flex: 1; min-width: 200px; display: flex; flex-direction: column; gap: 3px; }
.ch-act { gap: 4px; flex-wrap: nowrap; }
.cmd-row { display: flex; align-items: baseline; gap: 12px; padding: 10px 0; border-top: 1px solid var(--c-border); }
.cmd-row:first-child { border-top: 0; }
.cmd { background: var(--c-surface); padding: 2px 6px; border-radius: 4px; min-width: 76px; flex: none; }
</style>
