<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { NButton, NInput, NSelect, NSwitch } from 'naive-ui'
import { api } from '../api'
import { state, loadServers, ui } from '../store'
import { run, ask, idsCsv, rotateAllKeys } from '../helpers'
import { ago, dt, copy } from '../format'
import { useDirtyForm } from '../composables/useDirtyForm'
import QrModal from '../components/QrModal.vue'
import SubDeviceModal from './sub/SubDeviceModal.vue'
import SubTemplatesSection from './sub/SubTemplatesSection.vue'

const tokens = ref([])
const subBase = ref('')
const routes = ref([])
const routesLoaded = ref(false)
const settings = ref({})
const settingsOrig = ref('{}')
const settingsOrigObj = computed(() => { try { return JSON.parse(settingsOrig.value) } catch { return {} } })
const subDirty = (k) => settings.value[k] !== settingsOrigObj.value[k]
const subDirtyAny = computed(() => JSON.stringify(settings.value) !== settingsOrig.value)
useDirtyForm(subDirtyAny)

const preview = ref({ target: 'mihomo', tokenId: 0, content: '', nodes: 0 })
const logs = ref([])
const logToken = ref(0)
const qr = ref({ show: false, text: '', title: '' })
const deviceModal = ref({ show: false, device: null })
const rotating = ref(false)
const busyKey = ref('')
const saving = ref(false)

const targets = [{ label: '自动识别', value: 'auto' }, { label: 'mihomo', value: 'mihomo' }, { label: 'sing-box', value: 'singbox' }, { label: 'Base64（通用）', value: 'base64' }]
const targetLabel = Object.fromEntries(targets.map((t) => [t.value, t.label]))
const urlBase = computed(() => (subBase.value || window.location.origin).replace(/\/+$/, ''))

async function loadTokens() {
  const r = await run(() => api.get('/sub/tokens'))
  tokens.value = r?.tokens || []
  subBase.value = r?.base || ''
  if (preview.value.tokenId && !tokens.value.some((t) => t.id === preview.value.tokenId)) preview.value.tokenId = 0
}
async function loadPreview() {
  const q = new URLSearchParams({ target: preview.value.target })
  if (preview.value.tokenId) q.set('token_id', String(preview.value.tokenId))
  const r = await run(() => api.get(`/sub/preview?${q}`))
  if (r) { preview.value.content = r.content; preview.value.nodes = r.nodes }
}
async function loadLogs() { logs.value = (await run(() => api.get(`/sub/logs?token_id=${logToken.value || ''}`))) || [] }
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
  loadTokens(); loadPreview(); loadLogs(); loadSettings()
  api.get('/routes').then((r) => { routes.value = r; routesLoaded.value = true }).catch(() => {})
  if (!state.serversLoaded) loadServers().catch(() => {})
})
watch(() => [preview.value.target, preview.value.tokenId], loadPreview)
watch(logToken, loadLogs)

const pickedIds = computed(() => idsCsv(settings.value.sub_routes))
const savedPick = computed(() => { const ids = idsCsv(settingsOrigObj.value.sub_routes); return ids.length ? new Set(ids) : null })
const inSub = computed(() => routes.value.filter((r) => r.enabled && r.in_sub && (!savedPick.value || savedPick.value.has(r.id))))
const routeOpts = computed(() => {
  const selected = new Set(pickedIds.value)
  const known = new Set(routes.value.map((r) => r.id))
  return [
    ...routes.value.map((r) => {
      const off = !r.enabled ? '（已停用）' : !r.in_sub ? '（未加入订阅）' : ''
      return { label: r.name + off, value: r.id, disabled: !!off && !selected.has(r.id) }
    }),
    ...pickedIds.value.filter((id) => !known.has(id)).map((id) => ({ label: `已删除的线路 #${id}`, value: id })),
  ]
})
const routeById = computed(() => new Map(routes.value.map((r) => [r.id, r])))
function tokenRouteInfo(t) {
  const ids = idsCsv(t.routes)
  if (!ids.length) return { text: '跟随全局', cls: '', title: routesLoaded.value ? `跟随全局设置（当前共 ${inSub.value.length} 条线路）` : '跟随全局设置' }
  const names = ids.map((id) => {
    const r = routeById.value.get(id)
    if (!r) return `已删除 #${id}`
    return r.name + (!r.enabled ? '（已停用）' : !r.in_sub ? '（未加入订阅）' : r.entry_blocked ? '（疑似被封）' : '')
  })
  if (!routesLoaded.value) return { text: `${ids.length} 条指定`, cls: 'ok', title: '' }
  const active = ids.filter((id) => { const r = routeById.value.get(id); return r && r.enabled && r.in_sub }).length
  return { text: active === ids.length ? `${ids.length} 条指定` : `${active}/${ids.length} 条生效`, cls: active < ids.length ? 'warn' : 'ok', title: names.join('、') }
}
const excluded = computed(() => inSub.value.filter((r) => r.entry_blocked).length)
const tokenOpts = computed(() => [{ label: '全部设备', value: 0 }, ...tokens.value.map((t) => ({ label: t.name, value: t.id }))])
const previewTokenOpts = computed(() => [{ label: '全局默认', value: 0 }, ...tokens.value.map((t) => ({ label: t.name, value: t.id }))])
const tokenName = (id) => tokens.value.find((t) => t.id === id)?.name || '#' + id
const userinfoOpts = computed(() => [
  { label: '所有节点服务器合计', value: 'all' }, { label: '仅订阅中的节点服务器', value: 'sub' },
  ...state.servers.filter((s) => s.mode === 'node').map((s) => ({ label: `${s.name}${s.cap_landing ? '（落地）' : ''}`, value: String(s.id) })),
  { label: '不显示', value: 'none' },
])
const hours = [6, 12, 24].map((h) => ({ label: h + ' 小时', value: h }))
const nameExample = computed(() => {
  const tpl = settings.value.sub_name_tpl || '{flag} {name}'
  return inSub.value.slice(0, 2).map((x) => tpl.replace('{flag}', '🇭🇰').replace('{name}', x.name).replace('{entry}', x.entry_name).replace('{landing}', x.landing_name || x.entry_name)).join(' · ')
})

function openDevice(d) { deviceModal.value = { show: true, device: d || null } }
function onDeviceSaved(id) { loadTokens(); if (id && preview.value.tokenId === id) loadPreview() }
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
  try { await rotateAllKeys({ title: '一键轮换', confirm: '一键轮换需要确认身份' }) } finally { rotating.value = false }
}
async function saveSettings() {
  if (saving.value) return
  const s = settings.value
  saving.value = true
  try {
    if (await run(() => api.put('/settings', { ...s, sub_update_hours: String(s.sub_update_hours) }), '已保存')) {
      settingsOrig.value = JSON.stringify(s); loadPreview(); loadTokens()
    }
  } finally { saving.value = false }
}
async function copyPreview() { if (await copy(preview.value.content)) ui.message.success('已复制') }
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
        <n-button type="primary" @click="openDevice(null)">添加设备</n-button>
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
                  <td><button type="button" :class="['tag', 'route-btn', tokenRouteInfo(t).cls]" :title="tokenRouteInfo(t).title" @click="openDevice(t)">{{ tokenRouteInfo(t).text }}</button></td>
                  <td><n-select size="tiny" :value="t.target_default || 'auto'" :options="targets" style="width: 120px" @update:value="(v) => updateToken(t, { target_default: v === 'auto' ? '' : v })" /></td>
                  <td class="nowrap small">{{ t.last_used_at ? ago(t.last_used_at) : '从未' }}<div class="muted mono">{{ t.last_ip }}</div></td>
                  <td class="mono">{{ t.accesses_7d }}</td>
                  <td><n-switch size="small" :value="t.enabled" @update:value="(v) => updateToken(t, { enabled: v })" /></td>
                  <td class="nowrap">
                    <n-button size="tiny" quaternary @click="copyUrl(t)">复制</n-button>
                    <n-button size="tiny" quaternary @click="qr = { show: true, text: t.url, title: t.name }">二维码</n-button>
                    <n-button size="tiny" quaternary @click="openDevice(t)">编辑</n-button>
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
              <div class="m-row"><span>可订阅线路</span><button type="button" :class="['tag', 'route-btn', tokenRouteInfo(t).cls]" :title="tokenRouteInfo(t).title" @click="openDevice(t)">{{ tokenRouteInfo(t).text }}</button></div>
              <div class="m-row"><span>最后拉取</span><span>{{ t.last_used_at ? ago(t.last_used_at) : '从未' }} · 7 天 {{ t.accesses_7d }} 次</span></div>
              <div class="row">
                <n-button size="small" type="primary" @click="copyUrl(t)">复制</n-button>
                <n-button size="small" @click="qr = { show: true, text: t.url, title: t.name }">二维码</n-button>
                <n-button size="small" @click="openDevice(t)">编辑</n-button>
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
          <div class="field"><div class="f-label"><span class="f-name">节点命名模板<i v-if="subDirty('sub_name_tpl')" class="f-dot" /></span><span class="f-hint">可用 {flag} {name} {entry} {landing}<template v-if="nameExample">。示例：{{ nameExample }}</template></span></div><div class="f-ctl"><n-input v-model:value="settings.sub_name_tpl" placeholder="默认 {flag} {name}" /></div></div>
          <div class="field"><div class="f-label"><span class="f-name">配置名称<i v-if="subDirty('sub_profile_name')" class="f-dot" /></span><span class="f-hint">客户端里显示的订阅名称（部分客户端仅在首次导入时读取）</span></div><div class="f-ctl"><n-input v-model:value="settings.sub_profile_name" placeholder="默认 VPS Panel" clearable /></div></div>
          <div class="field"><div class="f-label"><span class="f-name">订阅域名<i v-if="subDirty('sub_url')" class="f-dot" /></span><span class="f-hint">留空用面板地址；填写独立域名或 CDN 时需先将该域名解析并反代到面板（独立域名仅提供订阅接口，不暴露后台）</span></div><div class="f-ctl"><n-input v-model:value="settings.sub_url" placeholder="https://sub.example.com" clearable /></div></div>
          <div class="field"><div class="f-label"><span class="f-name">订阅中的线路<i v-if="subDirty('sub_routes')" class="f-dot" /></span><span class="f-hint">不选 = 全部；作为未单独指定线路的设备的默认线路。选了之后，新建的线路要在这里勾选才会下发</span></div><div class="f-ctl"><n-select multiple clearable filterable placeholder="全部线路" max-tag-count="responsive" aria-label="订阅中的线路" :value="pickedIds" :options="routeOpts" @update:value="(v) => (settings.sub_routes = v.join(','))" /></div></div>
          <div class="field f-bool"><div class="f-label"><span class="f-name">移出疑似被封的线路<i v-if="subDirty('sub_exclude_blocked')" class="f-dot" /></span><span class="f-hint">恢复后自动加回</span></div><div class="f-ctl"><n-switch v-model:value="settings.sub_exclude_blocked" aria-label="移出疑似被封的线路" /></div></div>
          <div class="field f-bool"><div class="f-label"><span class="f-name">移出离线入口的线路<i v-if="subDirty('sub_exclude_offline')" class="f-dot" /></span><span class="f-hint">入口机离线时，经过它的线路暂不下发</span></div><div class="f-ctl"><n-switch v-model:value="settings.sub_exclude_offline" aria-label="移出离线入口的线路" /></div></div>
          <div class="field"><div class="f-label"><span class="f-name">客户端更新间隔<i v-if="subDirty('sub_update_hours')" class="f-dot" /></span><span class="f-hint">客户端自动拉取订阅的周期</span></div><div class="f-ctl"><n-select v-model:value="settings.sub_update_hours" :options="hours" /></div></div>
          <div class="field"><div class="f-label"><span class="f-name">流量信息<i v-if="subDirty('sub_userinfo')" class="f-dot" /></span><span class="f-hint">客户端显示的已用流量与额度（subscription-userinfo）；指定线路后默认也会按所选线路对应的节点服务器统计</span></div><div class="f-ctl"><n-select v-model:value="settings.sub_userinfo" :options="userinfoOpts" /></div></div>
          <div class="f-actions">
            <span class="f-hint">新 IP 拉取订阅时，按告警规则「订阅陌生 IP」通知（与该设备最近 30 天的来源都不同）。</span>
            <span class="spacer" />
            <n-button :disabled="!subDirtyAny" @click="settings = { ...settingsOrigObj }">撤销</n-button>
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
        <div class="card s-card"><div class="s-pad"><pre class="pre light preview">{{ preview.content }}</pre></div></div>
      </section>
    </div>

    <sub-templates-section :targets="targets" :target-label="targetLabel" @changed="loadPreview" />

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

    <sub-device-modal v-model:show="deviceModal.show" :device="deviceModal.device" :routes="routes"
      :in-sub-count="inSub.length" :url-base="urlBase" :targets="targets" @saved="onDeviceSaved" />
    <qr-modal v-model:show="qr.show" :text="qr.text" :title="qr.title" hint="用客户端扫码导入订阅" />
  </div>
</template>

<style scoped>
.url { max-width: 360px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.route-btn { border: 0; font-family: inherit; cursor: pointer; transition: filter .15s; }
.route-btn:hover { filter: brightness(0.95); }
.preview { max-height: 520px; min-height: 200px; font-size: 11.5px; }
.ua { max-width: 280px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
@media (max-width: 767px) { .url { max-width: 100%; } }
</style>
