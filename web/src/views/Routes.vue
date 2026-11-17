<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NSelect, NSwitch, NDropdown } from 'naive-ui'
import { api } from '../api'
import { state, loadServers, on, ui } from '../store'
import { run, ask } from '../helpers'
import { bytes, flag, copy } from '../format'
import QrModal from '../components/QrModal.vue'

const router = useRouter()
const routes = ref([])
const loaded = ref(false)
const entry = ref(0)
const status = ref('')
const landings = ref([])
const qr = ref({ show: false, text: '', title: '' })

async function load() {
  routes.value = (await run(() => api.get('/routes'))) || []
  loaded.value = true
}
async function loadLandings() {
  if (!state.serversLoaded) await loadServers().catch(() => {})
  const ls = state.servers.filter((s) => s.mode === 'node' && s.cap_landing)
  landings.value = (await Promise.all(ls.map((s) => api.get(`/servers/${s.id}/xray`).catch(() => null)))).filter(Boolean)
}
onMounted(() => { load(); loadLandings() })
const off = on('route', load)
const t = setInterval(() => { if (!document.hidden) load() }, 60000)
onBeforeUnmount(() => { off(); clearInterval(t) })

const entries = computed(() => [{ label: '全部入口', value: 0 }, ...[...new Map(routes.value.map((r) => [r.entry_server_id, r.entry_name])).entries()].map(([value, label]) => ({ label, value }))])
const statuses = [{ label: '全部状态', value: '' }, { label: '正常', value: 'ok' }, { label: '异常', value: 'bad' }, { label: '已停用', value: 'disabled' }]
const list = computed(() => routes.value.filter((r) => (!entry.value || r.entry_server_id === entry.value) &&
  (!status.value || (status.value === 'ok' ? r.status === 'ok' : status.value === 'bad' ? ['degraded', 'down'].includes(r.status) || r.entry_blocked : r.status === 'disabled'))))
const inSub = computed(() => routes.value.filter((r) => r.in_sub && r.enabled).length)
const stText = { ok: '正常', degraded: '降级', down: '中断', disabled: '已停用' }
const stClass = { ok: 'ok', degraded: 'warn', down: 'bad', disabled: '' }

function input(r, patch = {}) {
  return {
    name: r.name, type: r.type, entry_server_id: r.entry_server_id, landing_server_id: r.landing_server_id,
    backup_ids: String(r.backup_ids || '').split(',').map(Number).filter(Boolean), enabled: r.enabled, in_sub: r.in_sub, sort: r.sort, tags: r.tags, ...patch,
  }
}
async function setSub(r, v) {
  const res = await run(() => api.put(`/routes/${r.id}`, input(r, { in_sub: v })))
  if (res) { r.in_sub = v; if (res.warning) ui.message.warning(res.warning) }
}
async function toggle(r) {
  if (await run(() => api.post(`/routes/${r.id}/toggle`), r.enabled ? '已停用' : '已启用')) load()
}
function actions(r) {
  return [
    { label: '编辑', key: 'edit' },
    { label: '复制分享链接', key: 'copy', disabled: !r.share_link },
    { label: '二维码', key: 'qr', disabled: !r.share_link },
    { label: r.enabled ? '停用' : '启用', key: 'toggle' },
    { label: '重置 UUID', key: 'regen' },
    { type: 'divider', key: 'd' },
    { label: '删除', key: 'delete', props: { style: 'color: #b4233a' } },
  ]
}
async function onAction(k, r) {
  if (k === 'edit') router.push({ name: 'route-edit', params: { id: r.id } })
  else if (k === 'copy') { if (await copy(r.share_link)) ui.message.success('已复制分享链接') }
  else if (k === 'qr') qr.value = { show: true, text: r.share_link, title: r.name }
  else if (k === 'toggle') toggle(r)
  else if (k === 'regen') {
    if (!(await ask('重置 UUID', `「${r.name}」的旧链接会立即失效，客户端需要刷新订阅。`))) return
    if (await run(() => api.post(`/routes/${r.id}/regen-uuid`), '已重置')) load()
  } else if (k === 'delete') {
    if (!(await ask('删除线路', `删除「${r.name}」？入口机上对应的用户会被移除。`, { danger: true, okText: '删除' }))) return
    if (await run(() => api.del(`/routes/${r.id}`), '已删除')) load()
  }
}
async function rotateAll() {
  if (!(await ask('一键轮换密钥', '会重新生成所有线路 UUID、Reality 密钥与 SS2022 密钥并下发（订阅链接本身不变）。所有客户端都要刷新订阅后才能继续使用。', { danger: true, okText: '继续' }))) return
  if (await run(() => api.post('/sub/rotate-all', {}, { confirm: '轮换全部密钥需要确认身份' }), '已轮换，配置正在下发')) load()
}
function latency(r) {
  if (!r.latency_ms) return '—'
  return Math.round(r.latency_ms) + ' ms'
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1>线路</h1>
      <span class="sub">{{ routes.length }} 条 · {{ inSub }} 条在订阅中</span>
      <span class="spacer" />
      <n-button @click="rotateAll">一键轮换密钥</n-button>
      <n-button type="primary" @click="router.push({ name: 'route-new' })">新建线路</n-button>
    </div>

    <div class="card">
      <div class="card-title">
        <n-select v-model:value="entry" :options="entries" size="small" style="width: 140px" />
        <n-select v-model:value="status" :options="statuses" size="small" style="width: 120px" />
      </div>
      <div v-if="!loaded" class="empty">加载中…</div>
      <div v-else-if="!routes.length" class="empty">还没有线路。先添加一台开启 Reality 的节点，再<router-link :to="{ name: 'route-new' }">新建线路</router-link>。</div>
      <template v-else>
        <div class="table-wrap hide-mobile">
          <table class="plain">
            <thead><tr><th>名称</th><th>类型</th><th>入口</th><th>落地</th><th>估算延迟</th><th>今日</th><th>本月</th><th>状态</th><th>订阅</th><th /></tr></thead>
            <tbody>
              <tr v-for="r in list" :key="r.id" :class="{ dim: !r.enabled }">
                <td><b>{{ r.name }}</b><div v-if="r.tags" class="muted small">{{ r.tags }}</div></td>
                <td><span class="tag">{{ r.type === 'direct' ? '直连' : '链式' }}</span></td>
                <td class="nowrap">{{ flag(r.entry_cc) }} {{ r.entry_name }}<span v-if="r.entry_blocked" class="tag bad" style="margin-left: 4px">疑似被封</span></td>
                <td class="nowrap">
                  <template v-if="r.type === 'chain'">{{ flag(r.landing_cc) }} {{ r.landing_name }}
                    <div v-if="r.backup_names?.length" class="muted small">备用 {{ r.backup_names.join('、') }}</div>
                  </template>
                  <span v-else class="muted">—</span>
                </td>
                <td class="mono small nowrap" :title="r.type === 'chain' ? `回程 ${Math.round(r.backhaul_ms)} ms + 线路段 ${Math.round(r.segment_ms)} ms` : ''">{{ latency(r) }}</td>
                <td class="mono">{{ bytes(r.today_bytes) }}</td>
                <td class="mono">{{ bytes(r.month_bytes) }}</td>
                <td><span :class="['tag', stClass[r.status]]">{{ stText[r.status] || r.status }}</span></td>
                <td><n-switch size="small" :value="r.in_sub" :disabled="!r.enabled" @update:value="(v) => setSub(r, v)" /></td>
                <td style="text-align: right"><n-dropdown :options="actions(r)" trigger="click" @select="(k) => onAction(k, r)"><n-button size="small" quaternary>操作</n-button></n-dropdown></td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="m-list">
          <div v-for="r in list" :key="r.id" :class="['m-item', { dim: !r.enabled }]">
            <div class="row">
              <b>{{ r.name }}</b><span class="tag">{{ r.type === 'direct' ? '直连' : '链式' }}</span>
              <span class="spacer" /><span :class="['tag', stClass[r.status]]">{{ stText[r.status] }}</span>
            </div>
            <div class="m-row"><span>路径</span><span>{{ r.entry_name }}{{ r.type === 'chain' ? ' → ' + r.landing_name : ' → 直连' }}</span></div>
            <div class="m-row"><span>估算延迟</span><span class="mono">{{ latency(r) }}</span></div>
            <div class="m-row"><span>今日 / 本月</span><span class="mono">{{ bytes(r.today_bytes) }} / {{ bytes(r.month_bytes) }}</span></div>
            <div class="row">
              <span class="small muted">订阅</span><n-switch size="small" :value="r.in_sub" :disabled="!r.enabled" @update:value="(v) => setSub(r, v)" />
              <span class="spacer" />
              <n-button size="small" :disabled="!r.share_link" @click="onAction('qr', r)">二维码</n-button>
              <n-dropdown :options="actions(r)" trigger="click" @select="(k) => onAction(k, r)"><n-button size="small">操作</n-button></n-dropdown>
            </div>
          </div>
        </div>
      </template>
      <p class="small muted foot">估算延迟 = 入口回程（三网平均）+ 入口到落地线路段。疑似被封的线路会自动移出订阅，恢复后自动加回。</p>
    </div>

    <div v-for="l in landings" :key="l.server.id" class="card">
      <div class="card-title"><h2>落地 {{ l.server.name }} · SS2022 入站</h2></div>
      <p v-if="l.landing" class="small muted" style="margin-top: -6px">端口 {{ l.landing.port }} · {{ l.landing.method }} · tcp,udp · 防火墙白名单已同步（{{ l.landing_users?.length || 0 }} 个入口）</p>
      <div v-if="!l.landing_users?.length" class="muted small">还没有入口机使用这台落地</div>
      <div class="table-wrap" v-else>
        <table class="plain">
          <thead><tr><th>用户（每个入口一个）</th><th>来源</th><th>本月上行</th><th>本月下行</th><th>白名单</th></tr></thead>
          <tbody>
            <tr v-for="u in l.landing_users" :key="u.id">
              <td class="mono small">{{ u.email }}</td>
              <td>{{ u.entry_name }}</td>
              <td class="mono">{{ bytes(u.month_up) }}</td>
              <td class="mono">{{ bytes(u.month_down) }}</td>
              <td><span class="tag ok">已放行</span></td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <qr-modal v-model:show="qr.show" :text="qr.text" :title="qr.title" hint="用客户端扫码导入（单条线路）" />
  </div>
</template>

<style scoped>
.dim { opacity: .55; }
.foot { margin: 12px 0 0; }
@media (max-width: 767px) { .page-head .n-button { flex: 1; } }
</style>
