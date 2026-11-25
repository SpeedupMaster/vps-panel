<script>
export default { name: 'Overview' }
</script>
<script setup>
import { ref, computed, onMounted, onActivated, onDeactivated, onBeforeUnmount } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NInput, NSelect, NProgress } from 'naive-ui'
import { api } from '../api'
import { state, loadServers, on } from '../store'
import { run } from '../helpers'
import { bytes, rate, pct, ago, date, daysUntil, flag, sevClass } from '../format'
import AddServer from '../components/AddServer.vue'
import Icon from '../components/Icon.vue'

const router = useRouter()
const ov = ref(null)
const q = ref('')
const group = ref('')
const sortBy = ref('status')
const adding = ref(false)
function loadDismissed() {
  try {
    const v = JSON.parse(sessionStorage.getItem('ov_dismissed') || '{}')
    return v && typeof v === 'object' ? v : {}
  } catch { return {} }
}
const dismissed = ref(loadDismissed())

async function load() {
  const r = await run(() => api.get('/overview'))
  if (r) ov.value = r
  loadServers().catch(() => {})
}
let timer = null
let off = null
let alertTimer = null
function startLive() {
  if (!timer) timer = setInterval(() => { if (!document.hidden) load() }, 60000)
  if (!off) off = on('alert', () => { clearTimeout(alertTimer); alertTimer = setTimeout(load, 500) })
}
function stopLive() {
  if (timer) { clearInterval(timer); timer = null }
  if (off) { off(); off = null }
  if (alertTimer) { clearTimeout(alertTimer); alertTimer = null }
}
onMounted(() => { load(); startLive() })
onActivated(() => { startLive(); if (ov.value) load() })
onDeactivated(stopLive)
onBeforeUnmount(stopLive)

const groups = computed(() => [{ label: '全部分组', value: '' }, ...[...new Set(state.servers.map((s) => s.group).filter(Boolean))].map((g) => ({ label: g, value: g }))])
const sorts = [{ label: '状态优先', value: 'status' }, { label: '名称', value: 'name' }, { label: '本月流量', value: 'traffic' }, { label: '到期时间', value: 'expire' }]
const rank = { offline: 0, suspect: 1, pending: 2, online: 3 }
const list = computed(() => {
  let l = state.servers.filter((s) => (!group.value || s.group === group.value) &&
    (!q.value || [s.name, s.ipv4, s.region, s.tags, s.provider].join(' ').toLowerCase().includes(q.value.toLowerCase())))
  const by = {
    status: (a, b) => (rank[a.status] ?? 9) - (rank[b.status] ?? 9) || (b.blocked - a.blocked) || a.sort - b.sort,
    name: (a, b) => a.name.localeCompare(b.name),
    traffic: (a, b) => (b.usage?.counted || 0) - (a.usage?.counted || 0),
    expire: (a, b) => (a.expire_at ? new Date(a.expire_at) : 9e15) - (b.expire_at ? new Date(b.expire_at) : 9e15),
  }
  return [...l].sort(by[sortBy.value])
})
const offline = computed(() => state.servers.filter((s) => s.status === 'offline'))
const blocked = computed(() => state.servers.filter((s) => s.blocked && !dismissed.value['b' + s.id]))
const online = computed(() => state.servers.filter((s) => s.online).length)
const sevCount = computed(() => {
  const c = { critical: 0, warning: 0 }
  for (const a of ov.value?.recent_alerts || []) if (a.status === 'firing' && c[a.severity] !== undefined) c[a.severity]++
  return c
})
function dismiss(id) {
  dismissed.value['b' + id] = Date.now()
  try { sessionStorage.setItem('ov_dismissed', JSON.stringify(dismissed.value)) } catch { /* ignore */ }
}
function memPct(s) { const t = s.system?.mem_total; return t ? (s.latest?.mem_used || 0) / t * 100 : null }
function diskPct(s) { const t = s.system?.disk_total; return t ? (s.latest?.disk_used || 0) / t * 100 : null }
function statusText(s) { return { online: '在线', offline: '离线', suspect: '不稳定', pending: '待安装' }[s.status] || s.status }
function statusClass(s) { return s.blocked ? 'bad' : { online: 'ok', offline: 'bad', suspect: 'warn' }[s.status] || 'off' }
function barColor(p) { return p >= 90 ? '#b4233a' : p >= 75 ? '#b8650a' : '#0f7b45' }
const money = (v, cur) => (cur === 'CNY' ? '¥' : cur === 'USD' ? '$' : cur === 'EUR' ? '€' : cur === 'GBP' ? '£' : cur === 'CAD' ? 'C$' : '') + Number(v || 0).toFixed(0)
function open(s) { router.push({ name: 'server', params: { id: s.id } }) }
const shareTotal = computed(() => (ov.value?.traffic_share || []).reduce((a, b) => a + b.used, 0))
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div class="ph"><h1>总览</h1><p class="page-desc">所有服务器的在线状态、流量、费用和告警。</p></div>
      <span class="spacer" />
      <n-input v-model:value="q" placeholder="搜索名称 / IP / 标签" clearable class="hide-mobile" style="width: 220px" />
      <n-button type="primary" @click="adding = true">添加服务器</n-button>
    </div>

    <div class="stats">
      <div class="card stat">
        <span class="muted small">在线</span>
        <b>{{ online }}<small> / {{ state.servers.length }}</small></b>
        <span v-if="offline.length" class="small bad-text">{{ offline[0].name }} 离线 {{ ago(offline[0].last_seen_at).replace('前', '') }}{{ offline.length > 1 ? ` 等 ${offline.length} 台` : '' }}</span>
        <span v-else class="muted small">{{ ov ? `${ov.nodes} 节点 · ${ov.probes} 探针` : '' }}</span>
      </div>
      <div class="card stat">
        <span class="muted small">本月流量（网卡合计）</span>
        <b>{{ ov ? bytes(ov.traffic_up + ov.traffic_down) : '—' }}</b>
        <span class="muted small" v-if="ov">↑ {{ bytes(ov.traffic_up) }} · ↓ {{ bytes(ov.traffic_down) }}</span>
      </div>
      <div class="card stat">
        <span class="muted small">本月花费（折合）</span>
        <b>{{ ov ? money(ov.monthly_cost, ov.currency) : '—' }}</b>
        <span class="muted small">{{ state.servers.length }} 台 · 按汇率折算</span>
      </div>
      <div class="card stat clickable" role="button" tabindex="0" @click="router.push({ name: 'alerts' })" @keydown.enter="router.push({ name: 'alerts' })" @keydown.space.prevent="router.push({ name: 'alerts' })">
        <span class="muted small">未处理告警</span>
        <b :class="{ 'bad-text': ov?.alerts_firing }">{{ ov?.alerts_firing ?? '—' }}</b>
        <span class="muted small">{{ sevCount.critical }} 严重 · {{ sevCount.warning }} 警告</span>
      </div>
      <div class="card stat">
        <span class="muted small">30 天内到期</span>
        <b>{{ ov?.expiring?.length ?? '—' }}</b>
        <span class="muted small">{{ (ov?.expiring || []).slice(0, 2).map((e) => `${e.name} ${e.days} 天`).join(' · ') || '无' }}</span>
      </div>
    </div>

    <div v-for="s in blocked" :key="s.id" class="banner" role="alert">
      <span><b>{{ s.name }} 疑似 IP 被封</b>：对国内回程目标连续多轮不可达，同时段其他 VPS 正常。相关线路已从订阅中剔除（如已开启）。</span>
      <span class="spacer" />
      <n-button size="small" @click="router.push({ name: 'tools', query: { server: s.id, tool: 'diagnose' } })">查看诊断</n-button>
      <n-button size="small" quaternary @click="dismiss(s.id)">忽略</n-button>
    </div>

    <section class="l-sec">
    <div class="sec-head">
      <div class="s-head">
        <h2>服务器</h2>
        <p>{{ state.serversLoaded ? (q || group ? `筛选出 ${list.length} 台，共 ${state.servers.length} 台。` : `共 ${state.servers.length} 台。`) : '' }}点击一行查看监控、Xray 和流量详情。</p>
      </div>
      <span class="spacer" />
      <n-input v-model:value="q" placeholder="搜索" clearable class="only-mobile" style="width: 120px" />
      <n-select v-model:value="group" :options="groups" aria-label="分组" style="width: 130px" />
      <n-select v-model:value="sortBy" :options="sorts" aria-label="排序" style="width: 110px" />
    </div>
    <div class="card s-card">
      <div v-if="!state.serversLoaded" class="empty">加载中…</div>
      <div v-else-if="!state.servers.length" class="empty">
        还没有服务器。<button class="link-btn" @click="adding = true">添加第一台</button>
      </div>
      <template v-else>
        <div class="table-wrap hide-mobile">
          <table class="plain hover">
            <thead><tr><th>名称</th><th>状态</th><th>CPU</th><th>内存</th><th>磁盘</th><th>网速 ↑/↓</th><th>本周期流量</th><th>Xray</th><th>到期</th></tr></thead>
            <tbody>
              <tr v-for="s in list" :key="s.id" tabindex="0" @click="open(s)" @keydown.enter="open(s)">
                <td>
                  <div class="row nowrap" style="gap: 8px">
                    <span class="flag">{{ flag(s.country_code) }}</span>
                    <div>
                      <a :href="$router.resolve({ name: 'server', params: { id: s.id } }).href" @click.prevent="open(s)"><b>{{ s.name }}</b></a>
                      <div class="muted small">{{ s.mode === 'probe' ? '探针' : [s.cap_reality && 'Reality', s.cap_landing && 'SS 落地'].filter(Boolean).join(' + ') }}{{ s.group ? ' · ' + s.group : '' }}</div>
                    </div>
                  </div>
                </td>
                <td><span :class="['tag', statusClass(s)]">{{ s.blocked ? '疑似被封' : statusText(s) }}</span></td>
                <td class="mono">{{ s.online ? pct(s.latest?.cpu) : '—' }}<span v-if="s.online && s.latest?.steal > 5" class="warn-text small"> st {{ pct(s.latest.steal) }}</span></td>
                <td class="mono">{{ s.online ? pct(memPct(s)) : '—' }}</td>
                <td class="mono">{{ pct(diskPct(s)) }}</td>
                <td class="mono nowrap">{{ s.online ? rate(s.latest?.net_out) + ' / ' + rate(s.latest?.net_in) : '—' }}</td>
                <td style="min-width: 150px">
                  <div class="small mono">{{ bytes(s.usage?.counted) }}{{ s.quota_bytes ? ' / ' + bytes(s.quota_bytes, 0) : '' }}</div>
                  <n-progress v-if="s.quota_bytes" type="line" :percentage="Math.min(100, s.usage?.percent || 0)" :show-indicator="false" :height="4" :color="barColor(s.usage?.percent || 0)" />
                </td>
                <td>
                  <span v-if="s.mode !== 'node'" class="muted">—</span>
                  <span v-else :class="['tag', s.xray_running ? 'ok' : 'bad']">{{ s.xray_running ? (s.xray_version || '运行中') : '未运行' }}</span>
                  <span v-if="s.xray_drift" class="tag warn" style="margin-left: 4px">漂移</span>
                </td>
                <td class="nowrap"><span :class="{ 'bad-text': daysUntil(s.expire_at) !== null && daysUntil(s.expire_at) <= 7 }">{{ date(s.expire_at) }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="m-list">
          <a v-for="s in list" :key="s.id" class="m-item" :href="$router.resolve({ name: 'server', params: { id: s.id } }).href" @click.prevent="open(s)">
            <div class="row" style="gap: 8px">
              <span class="flag">{{ flag(s.country_code) }}</span><b style="color: var(--c-text)">{{ s.name }}</b>
              <span class="spacer" />
              <span :class="['tag', statusClass(s)]">{{ s.blocked ? '疑似被封' : statusText(s) }}</span>
            </div>
            <div class="m-stats">
              <span><i>CPU</i>{{ s.online ? pct(s.latest?.cpu) : '—' }}</span>
              <span><i>内存</i>{{ s.online ? pct(memPct(s)) : '—' }}</span>
              <span><i>↑</i>{{ s.online ? rate(s.latest?.net_out) : '—' }}</span>
              <span><i>↓</i>{{ s.online ? rate(s.latest?.net_in) : '—' }}</span>
            </div>
            <div v-if="s.quota_bytes" class="small muted">流量 {{ bytes(s.usage?.counted) }} / {{ bytes(s.quota_bytes, 0) }}
              <n-progress type="line" :percentage="Math.min(100, s.usage?.percent || 0)" :show-indicator="false" :height="4" :color="barColor(s.usage?.percent || 0)" />
            </div>
          </a>
        </div>
      </template>
    </div>
    </section>

    <div class="grid-2">
      <section class="l-sec">
        <div class="sec-head">
          <div class="s-head"><h2>最近告警</h2><p>最近触发和恢复的告警。</p></div>
          <span class="spacer" />
          <router-link :to="{ name: 'alerts' }" class="small">全部告警</router-link>
        </div>
        <div class="card s-card">
          <div v-if="!ov?.recent_alerts?.length" class="empty">暂无告警</div>
          <div v-for="a in ov?.recent_alerts || []" :key="a.id" class="alert-row">
            <span :class="['tag', a.status === 'firing' ? (sevClass[a.severity] || 'bad') : a.status === 'info' ? 'info' : 'ok']">{{ a.status === 'firing' ? '告警中' : a.status === 'info' ? '通知' : '已恢复' }}</span>
            <span class="alert-text">{{ a.text }}</span>
            <span class="muted small nowrap">{{ ago(a.started_at) }}</span>
          </div>
        </div>
      </section>
      <div class="l-col">
        <section class="l-sec">
          <div class="sec-head"><div class="s-head"><h2>本周期流量占比</h2><p>按各台的计费口径统计，显示用量最多的 8 台。</p></div></div>
          <div class="card s-card">
            <div v-if="!shareTotal" class="empty">暂无流量数据</div>
            <div v-else class="s-pad share">
              <div v-for="t in (ov?.traffic_share || []).filter((x) => x.used > 0).slice(0, 8)" :key="t.id" class="share-row">
                <span class="share-name">{{ t.name }}</span>
                <div class="share-bar"><div :style="{ width: (t.used / shareTotal * 100) + '%' }" /></div>
                <span class="mono small nowrap">{{ bytes(t.used) }}</span>
              </div>
            </div>
          </div>
        </section>
        <section v-if="ov?.expiring?.length" class="l-sec">
          <div class="sec-head"><div class="s-head"><h2>即将到期</h2><p>30 天内到期的服务器，到期日在「资产」里修改。</p></div></div>
          <div class="card s-card">
            <div v-for="e in ov.expiring" :key="e.id" class="field f-inline">
              <div class="f-label"><router-link class="f-name" :to="{ name: 'server', params: { id: e.id } }">{{ e.name }}</router-link></div>
              <div class="f-ctl"><span :class="['small', e.days <= 7 ? 'bad-text' : 'muted']">{{ date(e.at) }} · {{ e.days < 0 ? '已过期' : e.days + ' 天' }}</span></div>
            </div>
          </div>
        </section>
      </div>
    </div>

    <add-server v-model:show="adding" />
    <button class="fab only-mobile" aria-label="添加服务器" @click="adding = true"><Icon d="M12 5v14 M5 12h14" :size="24" :width="2.2" /></button>
  </div>
</template>

<style scoped>
.stats { display: grid; grid-template-columns: repeat(5, 1fr); gap: 16px; }
.stat { display: flex; flex-direction: column; gap: 6px; }
.stat b { font-size: 26px; font-weight: 700; font-variant-numeric: tabular-nums; }
.stat b small { font-size: 15px; color: var(--c-muted); font-weight: 500; }
.clickable { cursor: pointer; }
.bad-text { color: var(--c-danger); }
.warn-text { color: var(--c-warning); }
.flag { font-size: 18px; }
table.hover tbody tr { cursor: pointer; }
table.hover tbody tr:hover td { background: #fafbfc; }
.alert-row { display: flex; align-items: center; gap: 10px; padding: 12px 0; border-top: 1px solid var(--c-border); font-size: 13px; }
.alert-row:first-child { border-top: none; }
.alert-text { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.share { display: flex; flex-direction: column; gap: 4px; }
.share-row { display: grid; grid-template-columns: 110px 1fr 80px; gap: 10px; align-items: center; padding: 5px 0; font-size: 13px; }
.share-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.share-bar { height: 8px; background: var(--c-surface); border-radius: 4px; overflow: hidden; }
.share-bar div { height: 100%; background: var(--c-primary); border-radius: 4px; }
.m-stats { display: grid; grid-template-columns: repeat(4, 1fr); gap: 6px; font-size: 13px; font-family: var(--mono); color: var(--c-text); }
.m-stats i { display: block; font-style: normal; font-size: 11px; color: var(--c-muted); font-family: var(--font); }
.fab { position: fixed; right: 16px; bottom: calc(72px + var(--safe-bottom)); width: 52px; height: 52px; border-radius: 50%; border: none; background: var(--c-primary); color: #fff; display: flex; align-items: center; justify-content: center; box-shadow: 0 4px 14px rgba(15, 123, 69, .35); z-index: 15; }
@media (max-width: 1200px) { .stats { grid-template-columns: repeat(3, 1fr); } }
@media (max-width: 767px) {
  .stats { grid-template-columns: 1fr 1fr; gap: 10px; }
  .stat b { font-size: 20px; }
  .stats .stat:last-child { grid-column: span 2; }
  .page-head .n-button { display: none; }
}
</style>
