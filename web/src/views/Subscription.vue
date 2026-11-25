<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { NButton, NInput, NSelect, NSwitch, NModal, NTabs, NTabPane, NInputNumber } from 'naive-ui'
import { api } from '../api'
import { state, loadServers, ui } from '../store'
import { run, ask } from '../helpers'
import { ago, dt, copy } from '../format'
import QrModal from '../components/QrModal.vue'

const tokens = ref([])
const subBase = ref('')
const routes = ref([])
const routesLoaded = ref(false)
const settings = ref({})
const settingsOrig = ref('{}')
const subDirty = (k) => settings.value[k] !== JSON.parse(settingsOrig.value)[k]
const subDirtyAny = computed(() => JSON.stringify(settings.value) !== settingsOrig.value)
const preview = ref({ target: 'mihomo', tokenId: 0, content: '', nodes: 0 })
const logs = ref([])
const logToken = ref(0)
const templates = ref([])
const qr = ref({ show: false, text: '', title: '' })
const adding = ref({ show: false, name: '', target: 'auto', token: '', routeIds: [] })
const editing = ref({ show: false, id: 0, name: '', target: 'auto', enabled: true, token: '', origToken: '', routeIds: [] })

const targets = [{ label: '自动识别', value: 'auto' }, { label: 'mihomo', value: 'mihomo' }, { label: 'sing-box', value: 'singbox' }, { label: 'Base64（通用）', value: 'base64' }]
const targetLabel = Object.fromEntries(targets.map((t) => [t.value, t.label]))
const urlBase = computed(() => (subBase.value || window.location.origin).replace(/\/+$/, ''))
function randSuffix() {
  const b = new Uint8Array(24)
  crypto.getRandomValues(b)
  return btoa(String.fromCharCode(...b)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

async function loadTokens() {
  const r = await run(() => api.get('/sub/tokens'))
  tokens.value = r?.tokens || []
  subBase.value = r?.base || ''
  if (preview.value.tokenId && !tokens.value.some((t) => t.id === preview.value.tokenId)) {
    preview.value.tokenId = 0
  }
}
async function loadPreview() {
  const q = new URLSearchParams({ target: preview.value.target })
  if (preview.value.tokenId) q.set('token_id', String(preview.value.tokenId))
  const r = await run(() => api.get(`/sub/preview?${q}`))
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
    sub_routes: s.sub_routes === 'all' ? '' : s.sub_routes || '',
  }
  settingsOrig.value = JSON.stringify(settings.value)
}
onMounted(() => {
  loadTokens(); loadPreview(); loadLogs(); loadTemplates(); loadSettings()
  api.get('/routes').then((r) => { routes.value = r; routesLoaded.value = true }).catch(() => {})
  if (!state.serversLoaded) loadServers().catch(() => {})
})
watch(() => [preview.value.target, preview.value.tokenId], loadPreview)
watch(logToken, loadLogs)

// sub_routes / token.routes: comma separated route IDs; '' = follow default/all.
const parseIds = (v) => String(v || '').split(',').map(Number).filter((x) => x > 0)
const pickedIds = computed(() => parseIds(settings.value.sub_routes))
const savedPick = computed(() => {
  const ids = parseIds(JSON.parse(settingsOrig.value).sub_routes)
  return ids.length ? new Set(ids) : null
})
const inSub = computed(() => routes.value.filter((r) => r.enabled && r.in_sub && (!savedPick.value || savedPick.value.has(r.id))))
function routeOptsFor(selectedIds) {
  const selected = new Set(selectedIds || [])
  const known = new Set(routes.value.map((r) => r.id))
  return [
    ...routes.value.map((r) => {
      const off = !r.enabled ? '（已停用）' : !r.in_sub ? '（未加入订阅）' : ''
      return { label: r.name + off, value: r.id, disabled: !!off && !selected.has(r.id) }
    }),
    ...(selectedIds || []).filter((id) => !known.has(id)).map((id) => ({ label: `已删除的线路 #${id}`, value: id })),
  ]
}
const routeOpts = computed(() => routeOptsFor(pickedIds.value))
const addRouteOpts = computed(() => routeOptsFor(adding.value.routeIds))
const editRouteOpts = computed(() => routeOptsFor(editing.value.routeIds))
const routeById = computed(() => new Map(routes.value.map((r) => [r.id, r])))
function tokenRouteInfo(t) {
  const ids = parseIds(t.routes)
  if (!ids.length) {
    return {
      text: '跟随全局',
      cls: '',
      title: routesLoaded.value ? `跟随全局设置（当前共 ${inSub.value.length} 条线路）` : '跟随全局设置',
    }
  }
  const names = ids.map((id) => {
    const r = routeById.value.get(id)
    if (!r) return `已删除 #${id}`
    const off = !r.enabled ? '（已停用）' : !r.in_sub ? '（未加入订阅）' : r.entry_blocked ? '（疑似被封）' : ''
    return r.name + off
  })
  if (!routesLoaded.value) {
    return { text: `${ids.length} 条指定`, cls: 'ok', title: '' }
  }
  const active = ids.filter((id) => {
    const r = routeById.value.get(id)
    return r && r.enabled && r.in_sub
  }).length
  const text = active === ids.length ? `${ids.length} 条指定` : `${active}/${ids.length} 条生效`
  const cls = active < ids.length ? 'warn' : 'ok'
  return { text, cls, title: names.join('、') }
}
const excluded = computed(() => inSub.value.filter((r) => r.entry_blocked).length)
const tokenOpts = computed(() => [{ label: '全部设备', value: 0 }, ...tokens.value.map((t) => ({ label: t.name, value: t.id }))])
const previewTokenOpts = computed(() => [{ label: '全局默认', value: 0 }, ...tokens.value.map((t) => ({ label: t.name, value: t.id }))])
const tokenName = (id) => tokens.value.find((t) => t.id === id)?.name || '#' + id
const userinfoOpts = computed(() => [
  { label: '所有节点服务器合计', value: 'all' },
  { label: '仅订阅中的节点服务器', value: 'sub' },
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

const rotating = ref(false)
const busyKey = ref('')
const saving = ref(false)

async function addToken() {
  if (saving.value) return
  const a = adding.value
  if (!a.name.trim()) { ui.message.warning('请填写设备名'); return }
  const body = { name: a.name.trim(), target: a.target === 'auto' ? '' : a.target, token: a.token.trim(), routes: a.routeIds.join(',') }
  saving.value = true
  try {
    if (await run(() => api.post('/sub/tokens', body), '已创建')) {
      adding.value = { show: false, name: '', target: 'auto', token: '', routeIds: [] }; loadTokens()
    }
  } finally { saving.value = false }
}
function openEdit(t) {
  editing.value = {
    show: true, id: t.id, name: t.name, target: t.target_default || 'auto', enabled: t.enabled,
    token: t.token || '', origToken: t.token || '', routeIds: parseIds(t.routes),
  }
}
async function saveEdit() {
  if (saving.value) return
  const e = editing.value
  if (!e.name.trim()) { ui.message.warning('请填写设备名'); return }
  const tok = e.token.trim()
  if (!tok) { ui.message.warning('请填写订阅后缀'); return }
  const body = { name: e.name.trim(), target_default: e.target === 'auto' ? '' : e.target, enabled: e.enabled, routes: e.routeIds.join(',') }
  if (tok !== e.origToken) body.token = tok
  saving.value = true
  try {
    if (await run(() => api.put(`/sub/tokens/${e.id}`, body), '已保存')) {
      editing.value.show = false; loadTokens()
      if (preview.value.tokenId === e.id) loadPreview()
    }
  } finally { saving.value = false }
}
async function updateToken(t, patch) {
  const body = { name: t.name, target_default: t.target_default, enabled: t.enabled, ...patch }
  if (await run(() => api.put(`/sub/tokens/${t.id}`, body))) Object.assign(t, patch)
}
async function resetToken(t) {
  if (busyKey.value) return
  busyKey.value = 'reset-' + t.id
  try {
    if (!(await ask('重置订阅链接', `「${t.name}」的旧链接立即失效，需要在设备上重新导入。`))) return
    if (await run(() => api.post(`/sub/tokens/${t.id}/reset`), '已重置')) loadTokens()
  } finally { busyKey.value = '' }
}
async function delToken(t) {
  if (busyKey.value) return
  busyKey.value = 'del-' + t.id
  try {
    if (!(await ask('删除设备订阅', `删除「${t.name}」？链接立即失效。`, { danger: true, okText: '删除' }))) return
    if (await run(() => api.del(`/sub/tokens/${t.id}`), '已删除')) loadTokens()
  } finally { busyKey.value = '' }
}
async function copyUrl(t) { if (await copy(t.url)) ui.message.success('已复制订阅链接') }
async function rotateAll() {
  if (rotating.value) return
  rotating.value = true
  try {
    if (!(await ask('一键轮换', '重新生成所有线路 UUID、Reality 与 SS2022 密钥并下发。订阅链接不变，设备刷新订阅即可。', { danger: true, okText: '继续' }))) return
    await run(() => api.post('/sub/rotate-all', {}, { confirm: '一键轮换需要确认身份' }), '已轮换，配置正在下发')
  } finally { rotating.value = false }
}
async function saveSettings() {
  if (saving.value) return
  const s = settings.value
  saving.value = true
  try {
    if (await run(() => api.put('/settings', { ...s, sub_update_hours: String(s.sub_update_hours) }), '已保存')) { settingsOrig.value = JSON.stringify(s); loadPreview(); loadTokens() }
  } finally { saving.value = false }
}
async function copyPreview() { if (await copy(preview.value.content)) ui.message.success('已复制') }

// templates
const tplHint = '节点会注入到模板的 proxies / outbounds；代理组里用 {{all}} 引用全部节点、{{regions}} 引用按国家动态生成的组、{{regions_ai}} 引用避开香港/大陆等 AI 受限地区的国家组、{{groups}} 引用全部分组。保存前会用当前节点渲染一次校验。'
const tpl = ref({ show: false, id: 0, target: 'mihomo', name: '', content: '', active: true })
const builtin = ref(null)
async function editTemplate(t) {
  if (!builtin.value) builtin.value = await run(() => api.get('/sub/templates/default'))
  tpl.value = t ? { show: true, ...t } : { show: true, id: 0, target: 'mihomo', name: '我的模板', content: builtin.value?.mihomo || '', active: true }
}
function loadBuiltin() { tpl.value.content = builtin.value?.[tpl.value.target] || '' }
async function saveTemplate() {
  if (saving.value) return
  const t = tpl.value
  const body = { target: t.target, name: t.name, content: t.content, active: t.active }
  saving.value = true
  try {
    const r = await run(() => (t.id ? api.put(`/sub/templates/${t.id}`, body) : api.post('/sub/templates', body)), '模板已保存')
    if (r) { tpl.value.show = false; loadTemplates(); loadPreview() }
  } finally { saving.value = false }
}
async function delTemplate(t) {
  if (busyKey.value) return
  busyKey.value = 'del-tpl-' + t.id
  try {
    if (!(await ask('删除模板', `删除「${t.name}」？会回到内置模板。`))) return
    if (await run(() => api.del(`/sub/templates/${t.id}`), '已删除')) { loadTemplates(); loadPreview() }
  } finally { busyKey.value = '' }
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
      <n-button :loading="rotating" :disabled="rotating" @click="rotateAll">一键轮换</n-button>
    </div>

    <section class="l-sec">
    <div class="sec-head">
      <div class="s-head"><h2>设备订阅</h2><p>每台设备一个独立链接，可单独设置可订阅的线路、重置或停用。未单独指定线路时跟随全局设置。</p></div>
      <span class="spacer" />
      <n-button type="primary" @click="adding.show = true">添加设备</n-button>
    </div>
    <div class="card s-card">
      <div v-if="!tokens.length" class="empty">还没有订阅链接</div>
      <template v-else>
        <div class="table-wrap hide-mobile">
          <table class="plain">
            <thead><tr><th>设备</th><th>订阅链接</th><th>线路</th><th>格式</th><th>最后拉取</th><th>7 天次数</th><th>状态</th><th>操作</th></tr></thead>
            <tbody>
              <tr v-for="t in tokens" :key="t.id">
                <td><b>{{ t.name }}</b></td>
                <td class="mono small url">{{ t.url }}</td>
                <td>
                  <button type="button" :class="['tag', 'route-btn', tokenRouteInfo(t).cls]" :title="tokenRouteInfo(t).title" @click="openEdit(t)">{{ tokenRouteInfo(t).text }}</button>
                </td>
                <td><n-select size="tiny" :value="t.target_default || 'auto'" :options="targets" style="width: 120px" @update:value="(v) => updateToken(t, { target_default: v === 'auto' ? '' : v })" /></td>
                <td class="nowrap small">{{ t.last_used_at ? ago(t.last_used_at) : '从未' }}<div class="muted mono">{{ t.last_ip }}</div></td>
                <td class="mono">{{ t.accesses_7d }}</td>
                <td><n-switch size="small" :value="t.enabled" @update:value="(v) => updateToken(t, { enabled: v })" /></td>
                <td class="nowrap">
                  <n-button size="tiny" quaternary @click="copyUrl(t)">复制</n-button>
                  <n-button size="tiny" quaternary @click="qr = { show: true, text: t.url, title: t.name }">二维码</n-button>
                  <n-button size="tiny" quaternary @click="openEdit(t)">编辑</n-button>
                  <n-button size="tiny" quaternary :loading="busyKey === 'reset-' + t.id" :disabled="!!busyKey" @click="resetToken(t)">重置</n-button>
                  <n-button size="tiny" quaternary type="error" :loading="busyKey === 'del-' + t.id" :disabled="!!busyKey" @click="delToken(t)">删除</n-button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="m-list">
          <div v-for="t in tokens" :key="t.id" class="m-item">
            <div class="row"><b>{{ t.name }}</b><span class="spacer" /><n-switch size="small" :value="t.enabled" @update:value="(v) => updateToken(t, { enabled: v })" /></div>
            <div class="mono small url">{{ t.url }}</div>
            <div class="m-row"><span>可订阅线路</span><button type="button" :class="['tag', 'route-btn', tokenRouteInfo(t).cls]" :title="tokenRouteInfo(t).title" @click="openEdit(t)">{{ tokenRouteInfo(t).text }}</button></div>
            <div class="m-row"><span>最后拉取</span><span>{{ t.last_used_at ? ago(t.last_used_at) : '从未' }} · 7 天 {{ t.accesses_7d }} 次</span></div>
            <div class="row">
              <n-button size="small" type="primary" @click="copyUrl(t)">复制</n-button>
              <n-button size="small" @click="qr = { show: true, text: t.url, title: t.name }">二维码</n-button>
              <n-button size="small" @click="openEdit(t)">编辑</n-button>
              <n-button size="small" :loading="busyKey === 'reset-' + t.id" :disabled="!!busyKey" @click="resetToken(t)">重置</n-button>
              <n-button size="small" type="error" quaternary :loading="busyKey === 'del-' + t.id" :disabled="!!busyKey" @click="delToken(t)">删除</n-button>
            </div>
          </div>
        </div>
      </template>
    </div>
    </section>

    <div class="grid-2">
      <section class="l-sec">
      <div class="sec-head"><div class="s-head"><h2>订阅设置</h2><p>节点命名、默认线路、要剔除的线路和客户端拉取方式，对所有设备生效。</p></div></div>
      <div class="card s-card">
        <div class="field">
          <div class="f-label"><span class="f-name">节点命名模板<i v-if="subDirty('sub_name_tpl')" class="f-dot" /></span><span class="f-hint">可用 {flag} {name} {entry} {landing}<template v-if="nameExample">。示例：{{ nameExample }}</template></span></div>
          <div class="f-ctl"><n-input v-model:value="settings.sub_name_tpl" placeholder="默认 {flag} {name}" /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">配置名称<i v-if="subDirty('sub_profile_name')" class="f-dot" /></span><span class="f-hint">客户端里显示的订阅名称（部分客户端仅在首次导入时读取）</span></div>
          <div class="f-ctl"><n-input v-model:value="settings.sub_profile_name" placeholder="默认 VPS Panel" clearable /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">订阅域名<i v-if="subDirty('sub_url')" class="f-dot" /></span><span class="f-hint">留空用面板地址；填写独立域名或 CDN 时需先将该域名解析并反代到面板（独立域名仅提供订阅接口，不暴露后台）</span></div>
          <div class="f-ctl"><n-input v-model:value="settings.sub_url" placeholder="https://sub.example.com" clearable /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">订阅中的线路<i v-if="subDirty('sub_routes')" class="f-dot" /></span><span class="f-hint">不选 = 全部；作为未单独指定线路的设备的默认线路。选了之后，新建的线路要在这里勾选才会下发</span></div>
          <div class="f-ctl"><n-select multiple clearable filterable placeholder="全部线路" max-tag-count="responsive" aria-label="订阅中的线路"
            :value="pickedIds" :options="routeOpts" @update:value="(v) => (settings.sub_routes = v.join(','))" /></div>
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
          <div class="f-label"><span class="f-name">流量信息<i v-if="subDirty('sub_userinfo')" class="f-dot" /></span><span class="f-hint">客户端显示的已用流量与额度（subscription-userinfo）；指定线路后默认也会按所选线路对应的节点服务器统计</span></div>
          <div class="f-ctl"><n-select v-model:value="settings.sub_userinfo" :options="userinfoOpts" /></div>
        </div>
        <div class="f-actions">
          <span class="f-hint">新 IP 拉取订阅时，按告警规则「订阅陌生 IP」通知（与该设备最近 30 天的来源都不同）。</span>
          <span class="spacer" />
          <n-button :disabled="!subDirtyAny" @click="settings = JSON.parse(settingsOrig)">撤销</n-button>
          <n-button type="primary" :loading="saving" :disabled="!subDirtyAny || saving" @click="saveSettings">保存</n-button>
        </div>
      </div>
      </section>
      <section class="l-sec">
        <div class="sec-head">
          <div class="s-head"><h2>内容预览</h2><p>按所选格式生成的订阅内容，共 {{ preview.nodes }} 个节点。</p></div>
          <span class="spacer" />
          <n-select v-if="tokens.length" v-model:value="preview.tokenId" :options="previewTokenOpts" aria-label="预览设备" style="width: 140px" />
          <n-select v-model:value="preview.target" :options="targets.slice(1)" aria-label="格式" style="width: 130px" />
          <n-button @click="copyPreview">复制</n-button>
        </div>
        <div class="card s-card">
          <div class="s-pad"><pre class="pre light preview">{{ preview.content }}</pre></div>
        </div>
      </section>
    </div>

    <section class="l-sec">
    <div class="sec-head">
      <div class="s-head"><h2>规则模板</h2><p>分流规则模板。没有启用自定义模板时，使用内置防 DNS 泄露、按国家动态分组与多场景分流模板。</p></div>
      <span class="spacer" />
      <n-button @click="editTemplate(null)">自定义模板</n-button>
    </div>
    <div class="card s-card">
      <div v-if="!templates.length" class="empty">正在使用内置模板</div>
      <div v-else class="table-wrap">
      <table class="plain">
        <thead><tr><th>名称</th><th>格式</th><th>启用</th><th /></tr></thead>
        <tbody>
          <tr v-for="t in templates" :key="t.id">
            <td>{{ t.name }}</td><td>{{ targetLabel[t.target] || t.target }}</td>
            <td><n-switch size="small" :value="t.active" @update:value="(v) => activate(t, v)" /></td>
            <td style="text-align: right"><n-button size="tiny" quaternary @click="editTemplate(t)">编辑</n-button><n-button size="tiny" quaternary type="error" :loading="busyKey === 'del-tpl-' + t.id" :disabled="!!busyKey" @click="delTemplate(t)">删除</n-button></td>
          </tr>
        </tbody>
      </table>
      </div>
    </div>
    </section>

    <section class="l-sec">
    <div class="sec-head">
      <div class="s-head"><h2>访问日志</h2><p>客户端每次拉取订阅的记录，保留 30 天。</p></div>
      <span class="spacer" />
      <n-select v-model:value="logToken" :options="tokenOpts" aria-label="设备" style="width: 140px" />
    </div>
    <div class="card s-card">
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
    </section>

    <n-modal v-model:show="adding.show" preset="card" title="添加设备" style="width: min(500px, 94vw)">
      <div class="m-form">
        <div class="field">
          <div class="f-label"><span class="f-name">设备名</span></div>
          <div class="f-ctl"><n-input v-model:value="adding.name" placeholder="如 iPhone / 笔记本" @keyup.enter="addToken" /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">默认格式</span><span class="f-hint">「自动识别」会按客户端 User-Agent 选择格式</span></div>
          <div class="f-ctl"><n-select v-model:value="adding.target" :options="targets" /></div>
        </div>
        <div class="field stacked">
          <div class="f-label"><span class="f-name">可订阅线路</span><span class="f-hint">留空跟随全局设置（当前 {{ inSub.length }} 条）；指定后仅下发所选线路，新建线路需手动勾选</span></div>
          <div class="f-ctl">
            <n-select multiple clearable filterable placeholder="跟随全局设置" max-tag-count="responsive" aria-label="可订阅线路"
              :value="adding.routeIds" :options="addRouteOpts" @update:value="(v) => (adding.routeIds = v)" />
          </div>
        </div>
        <div class="field stacked">
          <div class="f-label"><span class="f-name">自定义后缀</span><span class="f-hint">留空自动随机生成；支持字母、数字、-、_ 和 .</span></div>
          <div class="f-ctl ctl-col">
            <n-input v-model:value="adding.token" placeholder="留空自动生成，如 my-phone" clearable @keyup.enter="addToken" />
            <div class="sub-preview mono small">{{ urlBase }}/{{ adding.token.trim() || '&lt;随机后缀&gt;' }}</div>
            <div v-if="adding.token.trim() && adding.token.trim().length < 8" class="sub-warn small">后缀较短容易被猜到，建议包含不易猜测的字符</div>
          </div>
        </div>
      </div>
      <template #footer><div class="m-foot"><n-button @click="adding.show = false">取消</n-button><n-button type="primary" :loading="saving" :disabled="saving" @click="addToken">创建</n-button></div></template>
    </n-modal>

    <n-modal v-model:show="editing.show" preset="card" title="编辑设备" style="width: min(500px, 94vw)">
      <div class="m-form">
        <div class="field">
          <div class="f-label"><span class="f-name">设备名</span></div>
          <div class="f-ctl"><n-input v-model:value="editing.name" placeholder="如 iPhone / 笔记本" @keyup.enter="saveEdit" /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">默认格式</span><span class="f-hint">「自动识别」会按客户端 User-Agent 选择格式</span></div>
          <div class="f-ctl"><n-select v-model:value="editing.target" :options="targets" /></div>
        </div>
        <div class="field stacked">
          <div class="f-label"><span class="f-name">可订阅线路</span><span class="f-hint">留空跟随全局设置（当前 {{ inSub.length }} 条）；指定后仅下发所选线路，仍受停用及被封/离线过滤影响</span></div>
          <div class="f-ctl">
            <n-select multiple clearable filterable placeholder="跟随全局设置" max-tag-count="responsive" aria-label="可订阅线路"
              :value="editing.routeIds" :options="editRouteOpts" @update:value="(v) => (editing.routeIds = v)" />
          </div>
        </div>
        <div class="field stacked">
          <div class="f-label"><span class="f-name">订阅后缀</span><span class="f-hint">支持字母、数字、-、_ 和 .；修改后旧链接立即失效</span></div>
          <div class="f-ctl ctl-col">
            <div class="ctl-row">
              <n-input v-model:value="editing.token" placeholder="例如 my-phone" @keyup.enter="saveEdit" />
              <n-button @click="editing.token = randSuffix()">随机</n-button>
            </div>
            <div class="sub-preview mono small">{{ urlBase }}/{{ editing.token.trim() || '…' }}</div>
            <div v-if="editing.token.trim() && editing.token.trim().length < 8" class="sub-warn small">后缀较短容易被猜到，建议包含不易猜测的字符</div>
          </div>
        </div>
      </div>
      <template #footer><div class="m-foot"><n-button @click="editing.show = false">取消</n-button><n-button type="primary" :loading="saving" :disabled="saving" @click="saveEdit">保存</n-button></div></template>
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
      <template #footer><div class="m-foot"><n-button @click="tpl.show = false">取消</n-button><n-button type="primary" :loading="saving" :disabled="saving" @click="saveTemplate">保存</n-button></div></template>
    </n-modal>

    <qr-modal v-model:show="qr.show" :text="qr.text" :title="qr.title" hint="用客户端扫码导入订阅" />
  </div>
</template>

<style scoped>
.url { max-width: 360px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.route-btn { border: 0; font-family: inherit; cursor: pointer; transition: filter .15s; }
.route-btn:hover { filter: brightness(0.95); }
.sub-preview { padding: 6px 10px; border-radius: 4px; background: var(--c-surface); color: var(--c-text-2); overflow-wrap: anywhere; }
.sub-warn { color: var(--c-warning); line-height: 1.5; }
.sw { margin-top: 6px; font-size: 13px; }
.preview { max-height: 520px; min-height: 200px; font-size: 11.5px; }
.ua { max-width: 280px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
@media (max-width: 767px) { .url { max-width: 100%; } }
</style>
