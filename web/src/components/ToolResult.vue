<script setup>
// Renders a saved tool result (store.ToolResult) by tool type.
import { computed } from 'vue'
import { bytes } from '../format'
import ServiceIcon from './ServiceIcon.vue'

const props = defineProps({ tool: { type: String, required: true }, data: { type: [String, Object, Array], default: null } })
const d = computed(() => {
  if (props.data == null) return null
  if (typeof props.data !== 'string') return props.data
  try { return JSON.parse(props.data) } catch { return props.data }
})
const arr = computed(() => (Array.isArray(d.value) ? d.value : []))
const num = (v, digits = 0) => (v == null || isNaN(v) ? '—' : Number(v).toFixed(digits))

// ---------- diagnose ----------
const lvl = { ok: 'ok', warn: 'warn', fail: 'bad' }
const lvlText = { ok: '正常', warn: '注意', fail: '异常' }
const diagCount = computed(() => {
  const c = { ok: 0, warn: 0, fail: 0 }
  for (const it of arr.value) c[it.level] = (c[it.level] || 0) + 1
  return c
})
// Backhaul detail is "[prefix：]江苏 联通 149ms，广东 电信 超时，…"; split it into chips.
function diagList(it) {
  if (it.name !== '回程' || !it.detail?.includes('，')) return null
  const i = it.detail.indexOf('：')
  const head = i >= 0 ? it.detail.slice(0, i) : ''
  const items = (i >= 0 ? it.detail.slice(i + 1) : it.detail).split('，').map((s) => {
    const m = s.match(/^(.*?)\s*(\d+)ms$/)
    return m ? { name: m[1], ms: Number(m[2]) } : { name: s.replace(/\s*超时$/, ''), ms: null }
  })
  return { head, items }
}
const msClass = (v) => (v == null ? 'bad' : v >= 250 ? 'warn' : 'ok')

// ---------- unlock ----------
const unlockClass = { yes: 'ok', partial: 'warn', no: 'bad', unknown: '' }
const unlockText = { yes: '解锁', partial: '部分', no: '未解锁', unknown: '未知' }
const unlockCount = computed(() => arr.value.filter((it) => it.status === 'yes').length)

// ---------- ip quality ----------
// "机房" is expected for a VPS; these flags are what services actually block on.
const riskyFlags = new Set(['代理', 'VPN', 'Tor', '滥用'])
const sourceRisky = (s) => s.flags?.some((f) => riskyFlags.has(f)) || (s.name === 'proxycheck.io' && d.value.risk >= 66)
const riskKnown = computed(() => !d.value?.sources?.length || d.value.sources.some((s) => s.name === 'proxycheck.io' && !s.skipped && !s.error))
const riskClass = (r) => (r >= 66 ? 'bad' : r >= 33 ? 'warn' : 'ok')
const ipFlags = computed(() => [...new Set((d.value?.sources || []).flatMap((s) => s.flags || []))])

// ---------- trace ----------
const carrier = { telecom: '电信', unicom: '联通', mobile: '移动' }
const lineGrade = { 'CN2 GIA': ['ok', '精品'], 9929: ['ok', '精品'], CMI: ['ok', '精品'], 'CN2 GT': ['info', '优化'], 163: ['', '普通'], 4837: ['', '普通'], CMNET: ['', '普通'] }
const hopTags = [
  [/^59\.43\./, 'CN2', 'ok'],
  [/^202\.97\./, '163 骨干', 'info'],
  [/^(218\.105|210\.51)\./, '9929', 'ok'],
  [/^219\.158\./, '4837 骨干', 'info'],
  [/^223\.(118|119|120|121)\./, 'CMI', 'ok'],
  [/^(221\.183|221\.176)\./, 'CMNET 骨干', 'info'],
  [/^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|100\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.)/, '内网', ''],
]
const hopTag = (ip) => hopTags.find(([re]) => re.test(ip))

// ---------- reality ----------
const realitySorted = computed(() => [...arr.value].sort((a, b) => (b.ok - a.ok) || (a.handshake_ms || 1e9) - (b.handshake_ms || 1e9)))
const realityOk = computed(() => arr.value.filter((c) => c.ok).length)
const realityCdn = computed(() => arr.value.filter((c) => c.cdn).length)
// Results from agents before CDN detection have no "cdn" key at all.
const cdnChecked = computed(() => arr.value.some((c) => 'cdn' in c))
const realityVerdict = (c) => (c.ok ? '可用' : c.cdn ? '走 CDN' : c.redirect ? '有跳转' : '不可用')
const realityReason = (c) => (c.ok ? '' : c.cdn ? c.cdn_by || '' : c.redirect ? '→ ' + c.redirect.replace(/^跳转到\s*/, '') : c.error || '')
const hsClass = (v) => (!v ? '' : v < 50 ? 'ok-text' : v < 200 ? '' : 'warn-text')

// ---------- net status ----------
const byteKeys = new Set(['net.core.rmem_max', 'net.core.wmem_max'])
const tripleKeys = new Set(['net.ipv4.tcp_rmem', 'net.ipv4.tcp_wmem'])
function sysctlHint(k, v) {
  if (byteKeys.has(k) && /^\d+$/.test(v)) return bytes(Number(v), 0)
  if (tripleKeys.has(k)) return v.split(' ').map((x) => bytes(Number(x), 0)).join(' / ')
  return ''
}
</script>

<template>
  <div v-if="d == null" class="muted small">暂无结果</div>

  <!-- 一键诊断 -->
  <div v-else-if="tool === 'diagnose' && Array.isArray(d)" class="res">
    <div class="sum">
      <span class="tag ok">{{ diagCount.ok }} 项正常</span>
      <span v-if="diagCount.warn" class="tag warn">{{ diagCount.warn }} 项注意</span>
      <span v-if="diagCount.fail" class="tag bad">{{ diagCount.fail }} 项异常</span>
    </div>
    <div class="diag">
      <div v-for="(it, i) in d" :key="i" class="d-row">
        <span :class="['dot', lvl[it.level] || 'off']" :title="lvlText[it.level]" />
        <b class="d-name">{{ it.name }}</b>
        <div v-if="diagList(it)" class="d-detail">
          <div v-if="diagList(it).head" :class="['small', it.level === 'fail' ? 'bad-text' : 'warn-text']">{{ diagList(it).head }}</div>
          <div class="chips">
            <span v-for="p in diagList(it).items" :key="p.name" :class="['chip', msClass(p.ms)]">{{ p.name }} <b class="mono">{{ p.ms == null ? '超时' : p.ms + ' ms' }}</b></span>
          </div>
        </div>
        <span v-else :class="['d-detail', it.level === 'fail' ? 'bad-text' : it.level === 'warn' ? 'warn-text' : '']">{{ it.detail }}</span>
      </div>
    </div>
  </div>

  <!-- 解锁检测 -->
  <div v-else-if="tool === 'unlock' && Array.isArray(d)" class="res">
    <div class="sum"><span class="small muted">{{ unlockCount }} / {{ d.length }} 个服务解锁</span></div>
    <div class="tiles">
      <div v-for="it in d" :key="it.name" :class="['tile', 'u-' + (unlockClass[it.status] || 'none')]" :title="[it.name, it.region, it.detail].filter(Boolean).join(' · ')">
        <service-icon :name="it.name" />
        <div class="t-body">
          <b class="t-name">{{ it.name }}</b>
          <span class="small muted t-sub">{{ [it.region, it.detail].filter(Boolean).join(' · ') || '—' }}</span>
        </div>
        <span :class="['tag', unlockClass[it.status]]">{{ unlockText[it.status] || it.status }}</span>
      </div>
    </div>
  </div>

  <!-- IP 质量 -->
  <div v-else-if="tool === 'ip_quality' && d.ip" class="res">
    <div class="stats">
      <div class="stat"><span class="s-label">IP 类型</span><span class="s-val">{{ d.type || '—' }}</span></div>
      <div class="stat"><span class="s-label">风险分</span><span v-if="riskKnown" :class="['s-val', riskClass(d.risk) + '-text']">{{ d.risk }}<small> / 100</small></span><span v-else class="s-val muted">未知</span></div>
      <div class="stat"><span class="s-label">黑名单</span>
        <span v-if="d.checked" :class="['s-val', d.blacklist?.length ? 'bad-text' : 'ok-text']">{{ d.blacklist?.length || 0 }}<small> / {{ d.checked }}</small></span>
        <span v-else class="s-val muted small">仅支持 IPv4</span>
      </div>
      <div class="stat"><span class="s-label">风险标记</span>
        <span v-if="ipFlags.length" class="s-val s-flags"><span v-for="f in ipFlags" :key="f" :class="['tag', riskyFlags.has(f) ? 'bad' : '']">{{ f }}</span></span>
        <span v-else :class="['s-val', d.proxy ? 'bad-text' : 'ok-text']">{{ d.proxy ? '代理' : '无' }}</span>
      </div>
    </div>
    <div class="kv">
      <span>IP</span><span class="mono">{{ d.ip }}</span>
      <span>ASN</span><span>{{ d.asn }} · {{ d.org }}</span>
      <span>位置</span><span>{{ d.country }} {{ d.city }}</span>
      <template v-if="d.blacklist?.length"><span>命中黑名单</span><span class="bad-text">{{ d.blacklist.join('、') }}</span></template>
    </div>
    <div v-if="d.sources?.length" class="table-wrap">
      <table class="plain">
        <thead><tr><th>数据源</th><th>结论</th></tr></thead>
        <tbody>
          <tr v-for="s in d.sources" :key="s.name">
            <td class="nowrap">{{ s.name }}</td>
            <td v-if="s.skipped" class="muted small">跳过：{{ s.skipped }}{{ s.name === 'ipapi.is' ? '（在「设置 → Agent 与探测」填写）' : '' }}</td>
            <td v-else-if="s.error" class="warn-text small">查询失败：{{ s.error }}</td>
            <td v-else :class="sourceRisky(s) ? 'bad-text' : ''">{{ [s.type, ...(s.flags || []), s.score].filter(Boolean).join(' · ') || '无标记' }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>

  <!-- 回程路由 -->
  <div v-else-if="tool === 'trace' && Array.isArray(d)" class="traces">
    <div v-for="(t, i) in d" :key="i" class="trace">
      <div class="t-head">
        <b>{{ carrier[t.carrier] || t.carrier }}</b>
        <span :class="['tag', (lineGrade[t.line] || [])[0]]">{{ t.line || '未知线路' }}</span>
        <span v-if="lineGrade[t.line]" class="small muted">{{ lineGrade[t.line][1] }}</span>
      </div>
      <div class="small muted mono t-target" :title="t.target">{{ t.target }}</div>
      <ol class="hops">
        <li v-for="(h, j) in t.hops || []" :key="j">
          <span class="h-no">{{ j + 1 }}</span><span class="mono">{{ h }}</span>
          <span v-if="hopTag(h)" :class="['tag', 'h-tag', hopTag(h)[2]]">{{ hopTag(h)[1] }}</span>
        </li>
        <li v-if="!t.hops?.length" class="muted small">没有响应的节点</li>
      </ol>
    </div>
  </div>

  <!-- 测速 / iperf -->
  <div v-else-if="tool === 'speedtest' && d.server !== undefined" class="res">
    <div class="stats">
      <div class="stat"><span class="s-label">下载</span><span class="s-val big">{{ num(d.down_mbps, 1) }}<small> Mbps</small></span></div>
      <div class="stat"><span class="s-label">上传</span><span class="s-val big">{{ num(d.up_mbps, 1) }}<small> Mbps</small></span></div>
      <div class="stat"><span class="s-label">延迟</span><span class="s-val big">{{ num(d.ping_ms, 1) }}<small> ms</small></span></div>
    </div>
    <div class="small muted">测速点：{{ d.server }}</div>
  </div>
  <div v-else-if="tool === 'iperf' && d.mbps !== undefined" class="res">
    <div class="stats">
      <div class="stat"><span class="s-label">对端 → 本机</span><span class="s-val big">{{ num(d.mbps) }}<small> Mbps</small></span></div>
    </div>
    <div class="small muted">iperf3 反向模式测 10 秒，数值为本机收到的平均带宽。</div>
  </div>

  <!-- Reality 目标扫描 -->
  <div v-else-if="tool === 'reality_scan' && Array.isArray(d)" class="res">
    <div class="sum">
      <span class="small muted">{{ realityOk }} / {{ d.length }} 个可用</span>
      <span v-if="realityCdn" class="tag warn">{{ realityCdn }} 个走 CDN</span>
      <span v-if="realitySorted[0]?.ok" class="small">推荐 <b class="mono">{{ realitySorted[0].domain }}</b>（握手 {{ Math.round(realitySorted[0].handshake_ms) }} ms）</span>
    </div>
    <div v-if="!cdnChecked" class="banner warn">这份结果来自旧版 Agent，没有检测目标是否走 CDN。升级 Agent 后重新扫描。</div>
    <div v-else-if="realityCdn" class="banner warn"><span>
      Reality 会把未通过认证的连接原样转发给目标站。目标走 CDN 时，扫描器会把这台 VPS 当成该 CDN 的中转 IP，用它访问 CDN 上的其他网站，跑掉 VPS 的流量。
      <template v-if="!realityOk">默认列表里的大站基本都在 CDN 后面，可以在上方填入自己的候选域名重新扫描，优先选和 VPS 同机房、不走 CDN 的网站。</template>
    </span></div>
    <div class="table-wrap">
      <table class="plain">
        <thead><tr><th>目标站</th><th title="TLS 1.3、HTTP/2、X25519 三项都要支持">握手特性</th><th>握手</th><th>CDN</th><th>结论</th></tr></thead>
        <tbody>
          <tr v-for="c in realitySorted" :key="c.domain" :class="{ dim: !c.ok }">
            <td class="mono small">{{ c.domain }}</td>
            <td class="nowrap feats">
              <span :class="c.tls13 ? 'ok-text' : 'bad-text'">{{ c.tls13 ? '' : '✕' }}TLS1.3</span>
              <span :class="c.h2 ? 'ok-text' : 'bad-text'">{{ c.h2 ? '' : '✕' }}H2</span>
              <span :class="c.x25519 ? 'ok-text' : 'bad-text'">{{ c.x25519 ? '' : '✕' }}X25519</span>
            </td>
            <td :class="['mono', 'nowrap', hsClass(c.handshake_ms)]">{{ c.handshake_ms ? Math.round(c.handshake_ms) + ' ms' : '—' }}</td>
            <td class="nowrap">
              <span v-if="!('cdn' in c)" class="muted small">未检测</span>
              <span v-else-if="c.cdn" class="warn-text" :title="c.cdn_by">{{ c.cdn }}</span>
              <span v-else class="ok-text small">无</span>
            </td>
            <td class="verdict">
              <span :class="['tag', c.ok ? 'ok' : 'bad']">{{ realityVerdict(c) }}</span>
              <span v-if="realityReason(c)" class="small muted reason" :title="realityReason(c)">{{ realityReason(c) }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>

  <!-- 网络状态 -->
  <div v-else-if="tool === 'net_status' && d.values" class="res">
    <div v-if="!d.supported" class="banner warn">{{ d.reason || '这台机器不支持修改拥塞控制' }}</div>
    <div class="stats">
      <div class="stat"><span class="s-label">拥塞控制</span><span :class="['s-val', d.congestion === 'bbr' ? 'ok-text' : 'warn-text']">{{ d.congestion || '—' }}</span></div>
      <div class="stat"><span class="s-label">队列</span><span class="s-val">{{ d.qdisc || '—' }}</span></div>
      <div class="stat"><span class="s-label">面板优化</span><span :class="['s-val', d.tuned ? 'ok-text' : 'muted']">{{ d.tuned ? '已应用' : '未应用' }}</span></div>
    </div>
    <div class="kv">
      <span>内核</span><span class="mono">{{ d.kernel || '—' }}</span>
      <span>虚拟化</span><span>{{ d.virt || '—' }}</span>
      <span>可用算法</span><span class="mono">{{ d.available || '—' }}</span>
    </div>
    <div class="table-wrap">
      <table class="plain">
        <thead><tr><th>sysctl</th><th>当前值</th></tr></thead>
        <tbody>
          <tr v-for="(v, k) in d.values" :key="k">
            <td class="mono small">{{ k }}</td>
            <td class="mono small">{{ v || '—' }}<span v-if="sysctlHint(k, v)" class="muted"> （{{ sysctlHint(k, v) }}）</span></td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>

  <!-- 网络优化 -->
  <div v-else-if="tool === 'tune_net' && d.diff" class="res">
    <div class="small muted">{{ d.applied ? '已应用' : '预览（未应用）' }}</div>
    <pre class="pre light">{{ (d.diff || []).join('\n') || '无需修改' }}</pre>
  </div>

  <pre v-else class="pre light">{{ typeof d === 'string' ? d : JSON.stringify(d, null, 2) }}</pre>
</template>

<style scoped>
.res { display: flex; flex-direction: column; gap: 14px; min-width: 0; }
.sum { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.chips { display: flex; flex-wrap: wrap; gap: 6px; }
.chip { display: inline-flex; align-items: center; gap: 6px; height: 24px; padding: 0 8px; border-radius: 4px; font-size: 12px; background: var(--c-surface); color: var(--c-text-2); white-space: nowrap; }
.chip b { font-weight: 500; font-size: 12px; }
.chip.ok b { color: var(--c-primary); }
.chip.warn b { color: var(--c-warning); }
.chip.bad { background: var(--c-danger-soft); }
.chip.bad b { color: var(--c-danger); }

.diag { display: flex; flex-direction: column; font-size: 13px; }
.d-row { display: grid; grid-template-columns: 8px 96px minmax(0, 1fr); gap: 12px; align-items: baseline; padding: 9px 0; border-top: 1px solid var(--c-border); }
.d-row:first-child { border-top: 0; padding-top: 2px; }
.d-row .dot { align-self: start; margin-top: 6px; }
.d-name { font-weight: 600; }
.d-detail { min-width: 0; word-break: break-word; color: var(--c-text-2); display: flex; flex-direction: column; gap: 6px; }

.tiles { display: grid; grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); grid-auto-rows: 1fr; gap: 10px; }
.tile { border: 1px solid var(--c-border); border-radius: 6px; padding: 12px; display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: 12px; min-width: 0; }
.tile.u-bad { background: #fffafa; border-color: #f3d6db; }
.tile.u-bad :deep(.si), .tile.u-none :deep(.si) { filter: grayscale(.6); opacity: .75; }
.t-body { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.t-name, .t-sub { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.t-name { font-size: 14px; }
.t-head { display: flex; align-items: center; gap: 8px; justify-content: space-between; }

.stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(130px, 1fr)); gap: 10px; }
.stat { border: 1px solid var(--c-border); border-radius: 6px; padding: 10px 14px; display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.s-label { font-size: 12px; color: var(--c-muted); }
.s-val { font-size: 17px; font-weight: 600; font-family: var(--mono); overflow-wrap: anywhere; }
.s-val.big { font-size: 24px; }
.s-val small { font-size: 12px; font-weight: 400; color: var(--c-muted); }
.s-flags { display: flex; flex-wrap: wrap; gap: 4px; font-family: inherit; }

.traces { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: 12px; }
.trace { border: 1px solid var(--c-border); border-radius: 6px; padding: 12px 14px; min-width: 0; }
.trace .t-head { justify-content: flex-start; }
.t-target { margin: 2px 0 8px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.hops { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 2px; }
.hops li { display: flex; align-items: center; gap: 8px; min-height: 24px; font-size: 12px; }
.h-no { width: 18px; text-align: right; color: var(--c-muted); font-family: var(--mono); font-size: 11px; flex: none; }
.h-tag { height: 18px; padding: 0 6px; font-size: 11px; }

table.plain tr.dim td { color: var(--c-muted); }
.feats { font-family: var(--mono); font-size: 11px; }
.feats span + span { margin-left: 6px; }
.verdict .reason { display: block; margin-top: 3px; max-width: 190px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ok-text { color: var(--c-primary); }
.bad-text { color: var(--c-danger); }
.warn-text { color: var(--c-warning); }
</style>
