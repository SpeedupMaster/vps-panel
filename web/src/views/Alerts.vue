<script setup>
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { NButton, NSelect, NSwitch, NModal, NInput, NInputNumber, NDropdown, NCheckbox, NTabs, NTabPane, NTimePicker } from 'naive-ui'
import { api } from '../api'
import { state, loadServers, on, refreshAlertCount, ui } from '../store'
import { run, ask } from '../helpers'
import { ago, dt, dur, sevLabel, sevClass } from '../format'

const tab = ref('events')
const events = ref([])
const firing = ref(0)
const fStatus = ref('firing')
const fSev = ref('')
const fServer = ref(0)
const rules = ref([])
const kinds = ref({})
const channels = ref([])
const fields = ref({})
const glob = ref({ quiet_on: false, quiet_from: '23:00', quiet_to: '08:00', recovery: true })
const globOrig = ref(JSON.stringify(glob.value))
const globDirty = computed(() => JSON.stringify(glob.value) !== globOrig.value)

async function loadEvents() {
  const q = new URLSearchParams({ status: fStatus.value || 'all', limit: '300' })
  if (fSev.value) q.set('severity', fSev.value)
  if (fServer.value) q.set('server_id', fServer.value)
  const r = await run(() => api.get('/alerts/events?' + q))
  if (r) { events.value = r.items; firing.value = r.firing; state.alertsFiring = r.firing }
}
async function loadRules() { rules.value = (await run(() => api.get('/alerts/rules'))) || [] }
async function loadChannels() { channels.value = (await run(() => api.get('/channels'))) || [] }
async function loadGlobal() {
  const s = await run(() => api.get('/settings'))
  if (!s) return
  const [a, b] = String(s.alert_quiet || '').split('-')
  glob.value = { quiet_on: !!s.alert_quiet, quiet_from: a || '23:00', quiet_to: b || '08:00', recovery: s.alert_recovery !== '0' }
  globOrig.value = JSON.stringify(glob.value)
}
onMounted(() => {
  loadEvents(); loadRules(); loadChannels(); loadGlobal()
  api.get('/alerts/kinds').then((k) => { kinds.value = k }).catch(() => {})
  api.get('/channels/fields').then((f) => { fields.value = f }).catch(() => {})
  if (!state.serversLoaded) loadServers().catch(() => {})
})
watch([fStatus, fSev, fServer], loadEvents)
const off = on('alert', () => loadEvents())
onBeforeUnmount(off)

const statusOpts = [{ label: '进行中', value: 'firing' }, { label: '已恢复', value: 'resolved' }, { label: '全部', value: 'all' }]
const sevOpts = [{ label: '全部级别', value: '' }, { label: '严重', value: 'critical' }, { label: '警告', value: 'warning' }, { label: '提醒', value: 'info' }]
const serverOpts = computed(() => [{ label: '全部服务器', value: 0 }, ...state.servers.map((s) => ({ label: s.name, value: s.id }))])
const sevOptsRule = sevOpts.slice(1)
function lasting(e) {
  const end = e.resolved_at ? new Date(e.resolved_at).getTime() : Date.now()
  return dur((end - new Date(e.started_at).getTime()) / 1000)
}
const silenced = (e) => e.silenced_to && new Date(e.silenced_to) > new Date()
async function ack(e) { if (await run(() => api.post(`/alerts/events/${e.id}/ack`))) { e.acked = true; refreshAlertCount(); loadEvents() } }
async function silence(e, minutes) { if (await run(() => api.post(`/alerts/events/${e.id}/silence`, { minutes }), '已静默')) loadEvents() }
async function ackAll() { if (await run(() => api.post('/alerts/events/ack-all'), '已全部确认')) { loadEvents(); refreshAlertCount() } }
const silenceOpts = [{ label: '静默 1 小时', key: 60 }, { label: '静默 6 小时', key: 360 }, { label: '静默 24 小时', key: 1440 }, { label: '静默 7 天', key: 10080 }]

// ---- rules ----
const thresholdKinds = { cpu: '%', mem: '%', disk: '%', steal: '%', load: '（每核）', backhaul_loss: '%', backhaul_latency: 'ms', traffic: '%' }
const kindOpts = computed(() => Object.entries(kinds.value).map(([value, label]) => ({ label, value })))
const scopeOpts = [{ label: '全部服务器', value: 'all' }, { label: '仅节点', value: 'node' }, { label: '仅探针', value: 'probe' }, { label: '入口机', value: 'entry' }, { label: '落地机', value: 'landing' }, { label: '指定服务器', value: 'ids' }]
const editing = ref({ show: false, r: {} })
function editRule(r) {
  const base = r ? { ...r } : { name: '', kind: 'cpu', threshold: 90, duration: 300, cooldown: 3600, severity: 'warning', scope: 'all', channels: '', enabled: true }
  const ids = String(base.scope).startsWith('ids:') ? base.scope.slice(4).split(',').map(Number).filter(Boolean) : []
  editing.value = { show: true, r: { ...base, scopeKind: ids.length ? 'ids' : base.scope, ids, chIds: String(base.channels || '').split(',').map(Number).filter(Boolean) } }
}
async function saveRule() {
  const r = editing.value.r
  const body = { id: r.id, name: r.name || kinds.value[r.kind], kind: r.kind, threshold: r.threshold || 0, duration: r.duration || 0, cooldown: r.cooldown || 0, severity: r.severity, enabled: r.enabled,
    scope: r.scopeKind === 'ids' ? 'ids:' + r.ids.join(',') : r.scopeKind, channels: r.chIds.join(',') }
  const res = await run(() => (r.id ? api.put(`/alerts/rules/${r.id}`, body) : api.post('/alerts/rules', body)), '已保存')
  if (res) { editing.value.show = false; loadRules() }
}
async function toggleRule(r, v) { if (await run(() => api.put(`/alerts/rules/${r.id}`, { ...r, enabled: v }))) r.enabled = v }
async function delRule(r) {
  if (!(await ask('删除规则', `删除「${r.name}」？`, { danger: true }))) return
  if (await run(() => api.del(`/alerts/rules/${r.id}`), '已删除')) loadRules()
}
function cond(r) {
  const u = thresholdKinds[r.kind]
  const parts = []
  if (u !== undefined) parts.push(`> ${r.threshold}${u}`)
  if (r.duration) parts.push(`持续 ${dur(r.duration)}`)
  return parts.join(' · ') || '发生即告警'
}
function scopeText(s) {
  if (String(s).startsWith('ids:')) return s.slice(4).split(',').map((id) => state.servers.find((x) => x.id === Number(id))?.name || '#' + id).join('、')
  return scopeOpts.find((o) => o.value === s)?.label || s
}
const chName = (ids) => (ids ? ids.split(',').map((id) => channels.value.find((c) => c.id === Number(id))?.name || '#' + id).join('、') : '全部渠道')

// ---- channels ----
const chTypes = { telegram: 'Telegram', bark: 'Bark', wecom: '企业微信', feishu: '飞书', email: '邮件', webhook: 'Webhook' }
const fieldLabel = { bot_token: 'Bot Token', chat_id: 'Chat ID', api_base: 'API 地址（可选，反代用）', bot_commands: '响应 Bot 命令（1 = 开启）', server: '服务器（默认 https://api.day.app）', device_key: 'Device Key', webhook: 'Webhook 地址', secret: '签名密钥（可选）', host: 'SMTP 主机', port: '端口', username: '用户名', password: '密码', from: '发件人', to: '收件人', url: 'URL' }
const secretFields = ['bot_token', 'password', 'secret', 'device_key', 'webhook']
const chEdit = ref({ show: false, c: {} })
function editChannel(c) {
  chEdit.value = { show: true, c: c ? { ...c, config: { ...c.config } } : { type: 'telegram', name: '', min_level: 'info', enabled: true, config: {} } }
}
async function saveChannel() {
  const c = chEdit.value.c
  const body = { type: c.type, name: c.name || chTypes[c.type], min_level: c.min_level, enabled: c.enabled, config: c.config }
  const r = await run(() => (c.id ? api.put(`/channels/${c.id}`, body) : api.post('/channels', body)), '已保存')
  if (r) { chEdit.value.show = false; loadChannels() }
}
async function testChannel(c) { await run(() => api.post(`/channels/${c.id}/test`), '测试消息已发送') }
async function delChannel(c) {
  if (!(await ask('删除渠道', `删除「${c.name}」？`, { danger: true }))) return
  if (await run(() => api.del(`/channels/${c.id}`), '已删除')) loadChannels()
}
async function toggleChannel(c, v) {
  if (await run(() => api.put(`/channels/${c.id}`, { type: c.type, name: c.name, min_level: c.min_level, enabled: v, config: {} }))) c.enabled = v
}
async function saveGlobal() {
  const g = glob.value
  if (await run(() => api.put('/settings', { alert_quiet: g.quiet_on ? `${g.quiet_from}-${g.quiet_to}` : '', alert_recovery: g.recovery }), '已保存')) globOrig.value = JSON.stringify(g)
}
const botCmds = [['/status', '所有服务器在线状态与告警概况'], ['/vps', '每台的 CPU、内存、网速'], ['/traffic', '本周期流量与额度'], ['/routes', '线路状态与延迟'], ['/sub', '订阅链接（仅发给绑定的 chat）'], ['/help', '命令列表']]
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div class="ph"><h1>告警</h1><p class="page-desc"><template v-if="firing">{{ firing }} 条告警进行中。</template><template v-else>没有进行中的告警。</template>在这里管理触发规则和通知渠道。</p></div>
      <span class="spacer" />
      <n-button @click="tab = 'rules'; editRule(null)">新建规则</n-button>
    </div>

    <n-tabs v-model:value="tab" type="line" animated>
      <n-tab-pane name="events" tab="事件">
        <div class="card">
          <div class="card-title">
            <n-select v-model:value="fStatus" :options="statusOpts" size="small" style="width: 100px" />
            <n-select v-model:value="fSev" :options="sevOpts" size="small" style="width: 110px" />
            <n-select v-model:value="fServer" :options="serverOpts" size="small" style="width: 140px" filterable />
            <span class="spacer" />
            <n-button size="small" :disabled="!firing" @click="ackAll">全部确认</n-button>
          </div>
          <div v-if="!events.length" class="empty">没有告警</div>
          <template v-else>
            <div class="table-wrap hide-mobile">
              <table class="plain">
                <thead><tr><th>严重度</th><th>服务器</th><th>内容</th><th>开始时间</th><th>持续</th><th>状态</th><th>操作</th></tr></thead>
                <tbody>
                  <tr v-for="e in events" :key="e.id">
                    <td><span :class="['tag', sevClass[e.severity]]">{{ sevLabel[e.severity] }}</span></td>
                    <td class="nowrap">{{ e.server_name || '面板' }}</td>
                    <td><div class="small muted">{{ e.kind_label }}</div>{{ e.text }}</td>
                    <td class="nowrap">{{ dt(e.started_at) }}</td>
                    <td class="nowrap">{{ lasting(e) }}</td>
                    <td class="nowrap">
                      <span v-if="e.status === 'resolved'" class="tag ok">已恢复</span>
                      <span v-else-if="silenced(e)" class="tag">静默至 {{ dt(e.silenced_to) }}</span>
                      <span v-else-if="e.acked" class="tag info">已确认</span>
                      <span v-else :class="['tag', sevClass[e.severity]]">进行中</span>
                    </td>
                    <td class="nowrap">
                      <template v-if="e.status === 'firing'">
                        <n-button v-if="!e.acked" size="tiny" quaternary @click="ack(e)">确认</n-button>
                        <n-dropdown :options="silenceOpts" trigger="click" @select="(m) => silence(e, m)"><n-button size="tiny" quaternary>静默</n-button></n-dropdown>
                      </template>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div class="m-list">
              <div v-for="e in events" :key="e.id" class="m-item">
                <div class="row">
                  <span :class="['tag', sevClass[e.severity]]">{{ sevLabel[e.severity] }}</span>
                  <b>{{ e.server_name || '面板' }}</b><span class="spacer" />
                  <span class="small muted">{{ ago(e.started_at) }}</span>
                </div>
                <div class="small">{{ e.text }}</div>
                <div class="row">
                  <span v-if="e.status === 'resolved'" class="tag ok">已恢复 · 持续 {{ lasting(e) }}</span>
                  <span v-else-if="e.acked" class="tag info">已确认</span>
                  <span class="spacer" />
                  <template v-if="e.status === 'firing'">
                    <n-button v-if="!e.acked" size="small" @click="ack(e)">确认</n-button>
                    <n-button size="small" @click="silence(e, 60)">静默 1 小时</n-button>
                  </template>
                </div>
              </div>
            </div>
          </template>
        </div>
      </n-tab-pane>

      <n-tab-pane name="rules" tab="规则">
        <div class="card">
          <div class="table-wrap">
            <table class="plain">
              <thead><tr><th>规则</th><th>条件</th><th>范围</th><th>冷却</th><th>严重度</th><th>渠道</th><th>启用</th><th /></tr></thead>
              <tbody>
                <tr v-for="r in rules" :key="r.id">
                  <td><b>{{ r.name }}</b><div class="muted small">{{ kinds[r.kind] || r.kind }}</div></td>
                  <td class="small">{{ cond(r) }}</td>
                  <td class="small">{{ scopeText(r.scope) }}</td>
                  <td class="small nowrap">{{ r.cooldown ? dur(r.cooldown) : '—' }}</td>
                  <td><span :class="['tag', sevClass[r.severity]]">{{ sevLabel[r.severity] }}</span></td>
                  <td class="small">{{ chName(r.channels) }}</td>
                  <td><n-switch size="small" :value="r.enabled" @update:value="(v) => toggleRule(r, v)" /></td>
                  <td class="nowrap" style="text-align: right"><n-button size="tiny" quaternary @click="editRule(r)">编辑</n-button><n-button size="tiny" quaternary type="error" @click="delRule(r)">删除</n-button></td>
                </tr>
              </tbody>
            </table>
          </div>
          <n-button size="small" style="margin-top: 12px" @click="editRule(null)">新建规则</n-button>
        </div>
      </n-tab-pane>

      <n-tab-pane name="channels" tab="通知渠道">
        <div class="sections">
          <section class="s-group">
            <div class="s-head">
              <h2>通知渠道</h2>
              <p>告警通过这些渠道推送。没有渠道时，告警只显示在面板里。</p>
            </div>
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
            <div class="s-head">
              <h2>推送设置</h2>
              <p>对所有渠道生效。</p>
            </div>
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
      </n-tab-pane>
    </n-tabs>

    <n-modal v-model:show="editing.show" preset="card" :title="editing.r.id ? '编辑规则' : '新建规则'" style="width: min(560px, 96vw)">
      <div class="form">
        <label>类型</label><n-select v-model:value="editing.r.kind" :options="kindOpts" filterable />
        <label>名称</label><n-input v-model:value="editing.r.name" :placeholder="kinds[editing.r.kind]" />
        <div class="form-2">
          <div v-if="thresholdKinds[editing.r.kind] !== undefined"><label>阈值 {{ thresholdKinds[editing.r.kind] }}</label><n-input-number v-model:value="editing.r.threshold" :min="0" /></div>
          <div><label>持续（秒）</label><n-input-number v-model:value="editing.r.duration" :min="0" :step="60" /></div>
          <div><label>重复提醒间隔（秒）</label><n-input-number v-model:value="editing.r.cooldown" :min="0" :step="600" /></div>
          <div><label>严重度</label><n-select v-model:value="editing.r.severity" :options="sevOptsRule" /></div>
        </div>
        <label>范围</label><n-select v-model:value="editing.r.scopeKind" :options="scopeOpts" />
        <n-select v-if="editing.r.scopeKind === 'ids'" v-model:value="editing.r.ids" multiple :options="serverOpts.slice(1)" placeholder="选择服务器" />
        <label>渠道（不选 = 全部启用的渠道）</label>
        <n-select v-model:value="editing.r.chIds" multiple :options="channels.map((c) => ({ label: c.name, value: c.id }))" placeholder="全部渠道" />
        <n-checkbox v-model:checked="editing.r.enabled">启用</n-checkbox>
      </div>
      <template #footer><div class="row" style="justify-content: flex-end"><n-button @click="editing.show = false">取消</n-button><n-button type="primary" @click="saveRule">保存</n-button></div></template>
    </n-modal>

    <n-modal v-model:show="chEdit.show" preset="card" :title="chEdit.c.id ? '编辑渠道' : '添加渠道'" style="width: min(520px, 96vw)">
      <div class="form">
        <label>类型</label>
        <n-select v-model:value="chEdit.c.type" :options="Object.entries(chTypes).map(([value, label]) => ({ label, value }))" :disabled="!!chEdit.c.id" />
        <label>名称</label><n-input v-model:value="chEdit.c.name" :placeholder="chTypes[chEdit.c.type]" />
        <template v-for="f in fields[chEdit.c.type] || []" :key="f">
          <label>{{ fieldLabel[f] || f }}</label>
          <n-switch v-if="f === 'bot_commands'" :value="chEdit.c.config[f] === '1'" @update:value="(v) => chEdit.c.config[f] = v ? '1' : '0'" />
          <n-input v-else v-model:value="chEdit.c.config[f]" :type="secretFields.includes(f) ? 'password' : 'text'" show-password-on="click"
            :placeholder="chEdit.c.id && secretFields.includes(f) ? '留空 = 不修改' : ''" :input-props="{ autocomplete: 'off' }" />
        </template>
        <label>最低级别</label><n-select v-model:value="chEdit.c.min_level" :options="sevOptsRule" />
        <n-checkbox v-model:checked="chEdit.c.enabled">启用</n-checkbox>
      </div>
      <template #footer><div class="row" style="justify-content: flex-end"><n-button @click="chEdit.show = false">取消</n-button><n-button type="primary" @click="saveChannel">保存</n-button></div></template>
    </n-modal>
  </div>
</template>

<style scoped>
.form { display: flex; flex-direction: column; gap: 6px; }
.form label { font-size: 13px; color: var(--c-text-2); margin-top: 6px; display: block; margin-bottom: 4px; }
.form-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 0 12px; }
.ch-row { display: flex; align-items: center; gap: 8px 12px; padding: 12px 0; border-top: 1px solid var(--c-border); flex-wrap: wrap; }
.ch-main { flex: 1; min-width: 200px; display: flex; flex-direction: column; gap: 3px; }
.ch-act { gap: 4px; flex-wrap: nowrap; }
.cmd-row { display: flex; align-items: baseline; gap: 12px; padding: 10px 0; border-top: 1px solid var(--c-border); }
.cmd-row:first-child { border-top: 0; }
.cmd { background: var(--c-surface); padding: 2px 6px; border-radius: 4px; min-width: 76px; flex: none; }
@media (max-width: 767px) { .form-2 { grid-template-columns: 1fr; } }
</style>
