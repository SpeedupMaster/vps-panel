<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { NButton, NInput, NSelect, NSwitch, NModal, NTabs, NTabPane, NInputNumber } from 'naive-ui'
import { api } from '../api'
import { state, loadServers, ui } from '../store'
import { run, ask } from '../helpers'
import { ago, dt, copy } from '../format'
import QrModal from '../components/QrModal.vue'

const tokens = ref([])
const routes = ref([])
const settings = ref({})
const settingsOrig = ref('{}')
const subDirty = (k) => settings.value[k] !== JSON.parse(settingsOrig.value)[k]
const subDirtyAny = computed(() => JSON.stringify(settings.value) !== settingsOrig.value)
const preview = ref({ target: 'mihomo', content: '', nodes: 0 })
const logs = ref([])
const logToken = ref(0)
const templates = ref([])
const qr = ref({ show: false, text: '', title: '' })
const adding = ref({ show: false, name: '', target: 'auto' })

const targets = [{ label: '自动识别', value: 'auto' }, { label: 'mihomo', value: 'mihomo' }, { label: 'sing-box', value: 'singbox' }, { label: 'Base64（通用）', value: 'base64' }]
const targetLabel = Object.fromEntries(targets.map((t) => [t.value, t.label]))

async function loadTokens() { const r = await run(() => api.get('/sub/tokens')); tokens.value = r?.tokens || [] }
async function loadPreview() {
  const r = await run(() => api.get(`/sub/preview?target=${preview.value.target}`))
  if (r) { preview.value.content = r.content; preview.value.nodes = r.nodes }
}
async function loadLogs() { logs.value = (await run(() => api.get(`/sub/logs?token_id=${logToken.value || ''}`))) || [] }
async function loadTemplates() { templates.value = (await run(() => api.get('/sub/templates'))) || [] }
async function loadSettings() {
  const s = await run(() => api.get('/settings'))
  if (!s) return
  settings.value = {
    sub_name_tpl: s.sub_name_tpl || '{flag} {name}', sub_profile_name: s.sub_profile_name || '', sub_update_hours: Number(s.sub_update_hours || 12),
    sub_userinfo: s.sub_userinfo || 'all', sub_exclude_blocked: s.sub_exclude_blocked !== '0', sub_exclude_offline: s.sub_exclude_offline === '1', sub_url: s.sub_url || '',
  }
  settingsOrig.value = JSON.stringify(settings.value)
}
onMounted(() => {
  loadTokens(); loadPreview(); loadLogs(); loadTemplates(); loadSettings()
  api.get('/routes').then((r) => { routes.value = r }).catch(() => {})
  if (!state.serversLoaded) loadServers().catch(() => {})
})
watch(() => preview.value.target, loadPreview)
watch(logToken, loadLogs)

const inSub = computed(() => routes.value.filter((r) => r.enabled && r.in_sub))
const excluded = computed(() => inSub.value.filter((r) => r.entry_blocked).length)
const tokenOpts = computed(() => [{ label: '全部设备', value: 0 }, ...tokens.value.map((t) => ({ label: t.name, value: t.id }))])
const tokenName = (id) => tokens.value.find((t) => t.id === id)?.name || '#' + id
const userinfoOpts = computed(() => [
  { label: '所有服务器合计', value: 'all' },
  ...state.servers.filter((s) => s.mode === 'node').map((s) => ({ label: `${s.name}${s.cap_landing ? '（落地）' : ''}`, value: String(s.id) })),
  { label: '不显示', value: 'none' },
])
const hours = [6, 12, 24].map((h) => ({ label: h + ' 小时', value: h }))
const nameExample = computed(() => {
  const tpl = settings.value.sub_name_tpl || '{flag} {name}'
  const r = inSub.value[0]
  if (!r) return ''
  return inSub.value.slice(0, 2).map((x) => tpl.replace('{flag}', '🇭🇰').replace('{name}', x.name).replace('{entry}', x.entry_name).replace('{landing}', x.landing_name || x.entry_name)).join(' · ')
})

async function addToken() {
  const a = adding.value
  if (!a.name.trim()) { ui.message.warning('请填写设备名'); return }
  if (await run(() => api.post('/sub/tokens', { name: a.name.trim(), target: a.target === 'auto' ? '' : a.target }), '已创建')) {
    adding.value = { show: false, name: '', target: 'auto' }; loadTokens()
  }
}
async function updateToken(t, patch) {
  const body = { name: t.name, target_default: t.target_default, enabled: t.enabled, ...patch }
  if (await run(() => api.put(`/sub/tokens/${t.id}`, body))) Object.assign(t, patch)
}
async function resetToken(t) {
  if (!(await ask('重置订阅链接', `「${t.name}」的旧链接立即失效，需要在设备上重新导入。`))) return
  if (await run(() => api.post(`/sub/tokens/${t.id}/reset`), '已重置')) loadTokens()
}
async function delToken(t) {
  if (!(await ask('删除设备订阅', `删除「${t.name}」？链接立即失效。`, { danger: true, okText: '删除' }))) return
  if (await run(() => api.del(`/sub/tokens/${t.id}`), '已删除')) loadTokens()
}
async function copyUrl(t) { if (await copy(t.url)) ui.message.success('已复制订阅链接') }
async function rotateAll() {
  if (!(await ask('一键轮换', '重新生成所有线路 UUID、Reality 与 SS2022 密钥并下发。订阅链接不变，设备刷新订阅即可。', { danger: true, okText: '继续' }))) return
  await run(() => api.post('/sub/rotate-all', {}, { confirm: '一键轮换需要确认身份' }), '已轮换，配置正在下发')
}
async function saveSettings() {
  const s = settings.value
  if (await run(() => api.put('/settings', { ...s, sub_update_hours: String(s.sub_update_hours) }), '已保存')) { settingsOrig.value = JSON.stringify(s); loadPreview() }
}
async function copyPreview() { if (await copy(preview.value.content)) ui.message.success('已复制') }

// templates
const tplHint = '节点会注入到模板的 proxies / outbounds；代理组里用 {{all}} 引用全部节点、{{regions}} 引用按地区生成的组、{{groups}} 引用全部分组。保存前会用当前节点渲染一次校验。'
const tpl = ref({ show: false, id: 0, target: 'mihomo', name: '', content: '', active: true })
const builtin = ref(null)
async function editTemplate(t) {
  if (!builtin.value) builtin.value = await run(() => api.get('/sub/templates/default'))
  tpl.value = t ? { show: true, ...t } : { show: true, id: 0, target: 'mihomo', name: '我的模板', content: builtin.value?.mihomo || '', active: true }
}
function loadBuiltin() { tpl.value.content = builtin.value?.[tpl.value.target] || '' }
async function saveTemplate() {
  const t = tpl.value
  const body = { target: t.target, name: t.name, content: t.content, active: t.active }
  const r = await run(() => (t.id ? api.put(`/sub/templates/${t.id}`, body) : api.post('/sub/templates', body)), '模板已保存')
  if (r) { tpl.value.show = false; loadTemplates(); loadPreview() }
}
async function delTemplate(t) {
  if (!(await ask('删除模板', `删除「${t.name}」？会回到内置模板。`))) return
  if (await run(() => api.del(`/sub/templates/${t.id}`), '已删除')) { loadTemplates(); loadPreview() }
}
async function activate(t, v) {
  if (await run(() => api.put(`/sub/templates/${t.id}`, { ...t, active: v }))) { loadTemplates(); loadPreview() }
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div class="ph"><h1>订阅</h1><p class="page-desc">{{ inSub.length }} 条线路在订阅中<template v-if="excluded">，{{ excluded }} 条因疑似被封已自动移出</template>。每台设备使用独立的订阅链接。</p></div>
      <span class="spacer" />
      <n-button @click="rotateAll">一键轮换</n-button>
    </div>

    <div class="card">
      <div class="card-title"><h2>设备订阅</h2><span class="spacer" /><n-button size="small" type="primary" @click="adding.show = true">添加设备</n-button></div>
      <p class="muted small" style="margin-top: -6px">每台设备一个独立链接，可单独重置或停用。所有设备看到的线路相同。</p>
      <div v-if="!tokens.length" class="empty">还没有订阅链接</div>
      <template v-else>
        <div class="table-wrap hide-mobile">
          <table class="plain">
            <thead><tr><th>设备</th><th>订阅链接</th><th>格式</th><th>最后拉取</th><th>7 天次数</th><th>状态</th><th>操作</th></tr></thead>
            <tbody>
              <tr v-for="t in tokens" :key="t.id">
                <td><b>{{ t.name }}</b></td>
                <td class="mono small url">{{ t.url }}</td>
                <td><n-select size="tiny" :value="t.target_default || 'auto'" :options="targets" style="width: 120px" @update:value="(v) => updateToken(t, { target_default: v === 'auto' ? '' : v })" /></td>
                <td class="nowrap small">{{ t.last_used_at ? ago(t.last_used_at) : '从未' }}<div class="muted mono">{{ t.last_ip }}</div></td>
                <td class="mono">{{ t.accesses_7d }}</td>
                <td><n-switch size="small" :value="t.enabled" @update:value="(v) => updateToken(t, { enabled: v })" /></td>
                <td class="nowrap">
                  <n-button size="tiny" quaternary @click="copyUrl(t)">复制</n-button>
                  <n-button size="tiny" quaternary @click="qr = { show: true, text: t.url, title: t.name }">二维码</n-button>
                  <n-button size="tiny" quaternary @click="resetToken(t)">重置</n-button>
                  <n-button size="tiny" quaternary type="error" @click="delToken(t)">删除</n-button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="m-list">
          <div v-for="t in tokens" :key="t.id" class="m-item">
            <div class="row"><b>{{ t.name }}</b><span class="spacer" /><n-switch size="small" :value="t.enabled" @update:value="(v) => updateToken(t, { enabled: v })" /></div>
            <div class="mono small url">{{ t.url }}</div>
            <div class="m-row"><span>最后拉取</span><span>{{ t.last_used_at ? ago(t.last_used_at) : '从未' }} · 7 天 {{ t.accesses_7d }} 次</span></div>
            <div class="row">
              <n-button size="small" type="primary" @click="copyUrl(t)">复制</n-button>
              <n-button size="small" @click="qr = { show: true, text: t.url, title: t.name }">二维码</n-button>
              <n-button size="small" @click="resetToken(t)">重置</n-button>
              <n-button size="small" type="error" quaternary @click="delToken(t)">删除</n-button>
            </div>
          </div>
        </div>
      </template>
    </div>

    <div class="grid-2">
      <div class="card s-card">
        <div class="s-card-head"><h2>订阅设置</h2></div>
        <div class="field">
          <div class="f-label"><span class="f-name">节点命名模板<i v-if="subDirty('sub_name_tpl')" class="f-dot" /></span><span class="f-hint">可用 {flag} {name} {entry} {landing}<template v-if="nameExample">。示例：{{ nameExample }}</template></span></div>
          <div class="f-ctl"><n-input v-model:value="settings.sub_name_tpl" placeholder="默认 {flag} {name}" /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">配置名称<i v-if="subDirty('sub_profile_name')" class="f-dot" /></span><span class="f-hint">客户端里显示的订阅名称</span></div>
          <div class="f-ctl"><n-input v-model:value="settings.sub_profile_name" placeholder="默认 VPS Panel" clearable /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">订阅域名<i v-if="subDirty('sub_url')" class="f-dot" /></span><span class="f-hint">留空用面板地址；可填 CDN 或另一个域名</span></div>
          <div class="f-ctl"><n-input v-model:value="settings.sub_url" placeholder="https://sub.example.com" clearable /></div>
        </div>
        <div class="field f-bool">
          <div class="f-label"><span class="f-name">移出疑似被封的线路<i v-if="subDirty('sub_exclude_blocked')" class="f-dot" /></span><span class="f-hint">恢复后自动加回</span></div>
          <div class="f-ctl"><n-switch v-model:value="settings.sub_exclude_blocked" aria-label="移出疑似被封的线路" /></div>
        </div>
        <div class="field f-bool">
          <div class="f-label"><span class="f-name">移出离线入口的线路<i v-if="subDirty('sub_exclude_offline')" class="f-dot" /></span><span class="f-hint">入口机离线时，经过它的线路暂不下发</span></div>
          <div class="f-ctl"><n-switch v-model:value="settings.sub_exclude_offline" aria-label="移出离线入口的线路" /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">客户端更新间隔<i v-if="subDirty('sub_update_hours')" class="f-dot" /></span><span class="f-hint">客户端自动拉取订阅的周期</span></div>
          <div class="f-ctl"><n-select v-model:value="settings.sub_update_hours" :options="hours" /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">流量信息<i v-if="subDirty('sub_userinfo')" class="f-dot" /></span><span class="f-hint">客户端显示的已用流量与额度（subscription-userinfo）</span></div>
          <div class="f-ctl"><n-select v-model:value="settings.sub_userinfo" :options="userinfoOpts" /></div>
        </div>
        <div class="f-actions">
          <span class="f-hint">新 IP 拉取订阅时，按告警规则「订阅陌生 IP」通知（与该设备最近 30 天的来源都不同）。</span>
          <span class="spacer" />
          <n-button :disabled="!subDirtyAny" @click="settings = JSON.parse(settingsOrig)">撤销</n-button>
          <n-button type="primary" :disabled="!subDirtyAny" @click="saveSettings">保存</n-button>
        </div>
      </div>
      <div class="card">
        <div class="card-title"><h2>内容预览</h2>
          <n-select v-model:value="preview.target" :options="targets.slice(1)" size="small" style="width: 130px" />
          <span class="muted small">{{ preview.nodes }} 个节点</span>
          <span class="spacer" /><n-button size="small" @click="copyPreview">复制</n-button>
        </div>
        <pre class="pre light preview">{{ preview.content }}</pre>
      </div>
    </div>

    <div class="card">
      <div class="card-title"><h2>规则模板</h2><span class="muted small">内置：国内直连 + AI / 流媒体分组</span><span class="spacer" /><n-button size="small" @click="editTemplate(null)">自定义模板</n-button></div>
      <div v-if="!templates.length" class="muted small">正在使用内置模板。</div>
      <table v-else class="plain">
        <thead><tr><th>名称</th><th>格式</th><th>启用</th><th /></tr></thead>
        <tbody>
          <tr v-for="t in templates" :key="t.id">
            <td>{{ t.name }}</td><td>{{ targetLabel[t.target] || t.target }}</td>
            <td><n-switch size="small" :value="t.active" @update:value="(v) => activate(t, v)" /></td>
            <td style="text-align: right"><n-button size="tiny" quaternary @click="editTemplate(t)">编辑</n-button><n-button size="tiny" quaternary type="error" @click="delTemplate(t)">删除</n-button></td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="card">
      <div class="card-title"><h2>访问日志</h2><span class="muted small">保留 30 天</span><span class="spacer" /><n-select v-model:value="logToken" :options="tokenOpts" size="small" style="width: 140px" /></div>
      <div v-if="!logs.length" class="empty">暂无记录</div>
      <div v-else class="table-wrap">
        <table class="plain">
          <thead><tr><th>时间</th><th>设备</th><th>IP</th><th>格式</th><th>客户端</th></tr></thead>
          <tbody>
            <tr v-for="l in logs" :key="l.id">
              <td class="nowrap">{{ dt(l.ts) }}</td><td>{{ tokenName(l.token_id) }}</td>
              <td class="mono small">{{ l.ip }} <span v-if="l.stranger" class="tag warn">陌生 IP</span></td>
              <td>{{ l.target }}</td><td class="small muted ua">{{ l.ua }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <n-modal v-model:show="adding.show" preset="card" title="添加设备" style="width: min(420px, 94vw)">
      <div class="m-form">
        <div class="field">
          <div class="f-label"><span class="f-name">设备名</span></div>
          <div class="f-ctl"><n-input v-model:value="adding.name" placeholder="如 iPhone / 笔记本" @keyup.enter="addToken" /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">默认格式</span><span class="f-hint">「自动识别」会按客户端 User-Agent 选择格式</span></div>
          <div class="f-ctl"><n-select v-model:value="adding.target" :options="targets" /></div>
        </div>
      </div>
      <template #footer><div class="m-foot"><n-button @click="adding.show = false">取消</n-button><n-button type="primary" @click="addToken">创建</n-button></div></template>
    </n-modal>

    <n-modal v-model:show="tpl.show" preset="card" :title="tpl.id ? '编辑模板' : '自定义模板'" style="width: min(860px, 96vw)">
      <div class="m-form">
        <div class="field">
          <div class="f-label"><span class="f-name">名称</span></div>
          <div class="f-ctl"><n-input v-model:value="tpl.name" placeholder="例如 我的分流规则" /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">格式</span><span v-if="tpl.id" class="f-hint">创建后不能修改</span></div>
          <div class="f-ctl ctl-row"><n-select v-model:value="tpl.target" :options="targets.slice(1, 3)" :disabled="!!tpl.id" aria-label="格式" />
          <n-button @click="loadBuiltin">载入内置模板</n-button></div>
        </div>
        <div class="field stacked">
          <div class="f-label"><span class="f-name">模板内容</span><span class="f-hint">{{ tplHint }}</span></div>
          <div class="f-ctl"><n-input v-model:value="tpl.content" type="textarea" :rows="16" class="mono" placeholder="模板内容" aria-label="模板内容" /></div>
        </div>
        <div class="field f-bool">
          <div class="f-label"><span class="f-name">保存后启用</span><span class="f-hint">同一格式只能启用一个模板</span></div>
          <div class="f-ctl"><n-switch v-model:value="tpl.active" aria-label="保存后启用" /></div>
        </div>
      </div>
      <template #footer><div class="m-foot"><n-button @click="tpl.show = false">取消</n-button><n-button type="primary" @click="saveTemplate">保存</n-button></div></template>
    </n-modal>

    <qr-modal v-model:show="qr.show" :text="qr.text" :title="qr.title" hint="用客户端扫码导入订阅" />
  </div>
</template>

<style scoped>
.url { max-width: 360px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sw { margin-top: 6px; font-size: 13px; }
.preview { max-height: 520px; min-height: 200px; font-size: 11.5px; }
.ua { max-width: 280px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
@media (max-width: 767px) { .url { max-width: 100%; } }
</style>
