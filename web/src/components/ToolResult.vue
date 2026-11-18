<script setup>
// Renders a saved tool result (store.ToolResult) by tool type.
import { computed } from 'vue'

const props = defineProps({ tool: { type: String, required: true }, data: { type: [String, Object, Array], default: null } })
const d = computed(() => {
  if (props.data == null) return null
  if (typeof props.data !== 'string') return props.data
  try { return JSON.parse(props.data) } catch { return props.data }
})
const unlockClass = { yes: 'ok', partial: 'warn', no: 'bad', unknown: '' }
const unlockText = { yes: '解锁', partial: '部分', no: '未解锁', unknown: '未知' }
const lvl = { ok: 'ok', warn: 'warn', fail: 'bad' }
const carrier = { telecom: '电信', unicom: '联通', mobile: '移动' }
// "机房" is expected for a VPS; these flags are what services actually block on.
const riskyFlags = new Set(['代理', 'VPN', 'Tor', '滥用'])
const sourceRisky = (s) => s.flags?.some((f) => riskyFlags.has(f)) || (s.name === 'proxycheck.io' && d.value.risk >= 66)
</script>

<template>
  <div v-if="d == null" class="muted small">暂无结果</div>
  <div v-else-if="tool === 'diagnose' && Array.isArray(d)" class="list">
    <div v-for="(it, i) in d" :key="i" class="row item">
      <span :class="['tag', lvl[it.level]]">{{ { ok: '正常', warn: '注意', fail: '异常' }[it.level] || it.level }}</span>
      <b class="name">{{ it.name }}</b><span class="small muted detail">{{ it.detail }}</span>
    </div>
  </div>
  <div v-else-if="tool === 'unlock' && Array.isArray(d)" class="chips">
    <span v-for="it in d" :key="it.name" :class="['tag', unlockClass[it.status]]" :title="it.detail">{{ it.name }} · {{ unlockText[it.status] || it.status }}{{ it.region ? ' (' + it.region + ')' : '' }}</span>
  </div>
  <div v-else-if="tool === 'ip_quality' && d.ip" class="kv">
    <span>IP</span><span class="mono">{{ d.ip }}</span>
    <span>ASN</span><span>{{ d.asn }} · {{ d.org }}</span>
    <span>位置</span><span>{{ d.country }} {{ d.city }}</span>
    <template v-if="d.sources?.length">
      <template v-for="s in d.sources" :key="s.name">
        <span>{{ s.name }}</span>
        <span v-if="s.skipped" class="muted">跳过：{{ s.skipped }}{{ s.name === 'ipapi.is' ? '（在「设置 → Agent 与探测」填写）' : '' }}</span>
        <span v-else-if="s.error" class="warn-text">查询失败：{{ s.error }}</span>
        <span v-else :class="sourceRisky(s) ? 'bad-text' : ''">{{ [s.type, ...(s.flags || []), s.score].filter(Boolean).join(' · ') || '无标记' }}</span>
      </template>
    </template>
    <template v-else>
      <span>类型</span><span>{{ d.type || '—' }}{{ d.proxy ? ' · 被标记为代理' : '' }}</span>
      <span>风险分</span><span :class="d.risk >= 66 ? 'bad-text' : ''">{{ d.risk }}</span>
    </template>
    <span>黑名单</span><span>{{ d.blacklist?.length ? d.blacklist.join('、') : d.checked ? `无（查了 ${d.checked} 个）` : '未检查（仅支持 IPv4）' }}</span>
  </div>
  <div v-else-if="tool === 'trace' && Array.isArray(d)" class="list">
    <details v-for="(t, i) in d" :key="i" class="trace">
      <summary><b>{{ carrier[t.carrier] || t.carrier }}</b> <span class="tag info">{{ t.line || '未知线路' }}</span> <span class="muted small mono">{{ t.target }}</span></summary>
      <pre class="pre light">{{ (t.hops || []).join('\n') }}</pre>
    </details>
  </div>
  <div v-else-if="tool === 'speedtest' && d.server !== undefined" class="kv">
    <span>测速点</span><span>{{ d.server }}</span>
    <span>延迟</span><span class="mono">{{ Math.round(d.ping_ms) }} ms</span>
    <span>下载</span><span class="mono">{{ d.down_mbps?.toFixed(1) }} Mbps</span>
    <span>上传</span><span class="mono">{{ d.up_mbps?.toFixed(1) }} Mbps</span>
  </div>
  <div v-else-if="tool === 'reality_scan' && Array.isArray(d)" class="table-wrap">
    <table class="plain">
      <thead><tr><th>目标站</th><th>TLS 1.3</th><th>H2</th><th>X25519</th><th>握手</th><th>结论</th></tr></thead>
      <tbody>
        <tr v-for="c in [...d].sort((a, b) => (b.ok - a.ok) || a.handshake_ms - b.handshake_ms)" :key="c.domain">
          <td class="mono small">{{ c.domain }}</td>
          <td>{{ c.tls13 ? '✓' : '✕' }}</td><td>{{ c.h2 ? '✓' : '✕' }}</td><td>{{ c.x25519 ? '✓' : '✕' }}</td>
          <td class="mono">{{ c.handshake_ms ? Math.round(c.handshake_ms) + ' ms' : '—' }}</td>
          <td><span :class="['tag', c.ok ? 'ok' : 'bad']" :title="c.error || c.redirect">{{ c.ok ? '可用' : (c.error || (c.redirect ? '有跳转' : '不可用')) }}</span></td>
        </tr>
      </tbody>
    </table>
  </div>
  <div v-else-if="tool === 'tune_net' && d.diff" class="list">
    <div class="small muted">{{ d.applied ? '已应用' : '预览（未应用）' }}</div>
    <pre class="pre light">{{ (d.diff || []).join('\n') || '无需修改' }}</pre>
  </div>
  <pre v-else class="pre light">{{ typeof d === 'string' ? d : JSON.stringify(d, null, 2) }}</pre>
</template>

<style scoped>
.list { display: flex; flex-direction: column; gap: 6px; }
.item { flex-wrap: nowrap; align-items: flex-start; }
.item .name { white-space: nowrap; font-size: 13px; }
.detail { flex: 1; min-width: 0; word-break: break-all; }
.chips { display: flex; flex-wrap: wrap; gap: 6px; }
.trace summary { cursor: pointer; padding: 4px 0; font-size: 13px; }
.bad-text { color: var(--c-danger); }
.warn-text { color: var(--c-warning); }
</style>
