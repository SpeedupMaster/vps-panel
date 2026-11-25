<script setup>
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NSelect, NProgress } from 'naive-ui'
import { bytes, rate, date, dur, carrierLabel, overallLoss, lossText, lossClass } from '../../format'
import Chart from '../../components/Chart.vue'

const props = defineProps({
  sv: { type: Object, required: true },
  metrics: { type: Array, required: true },
  range: { type: Number, required: true },
  backhaul: { type: Object, required: true },
  releases: { type: Object, required: true },
})
const emit = defineEmits(['update:range', 'run-tool'])
const router = useRouter()

const sys = computed(() => props.sv.system || {})
const usage = computed(() => props.sv.usage || {})
const countModeText = { out: '只计出站', in: '只计入站', both: '双向合计', max: '取出入较大值' }
const ranges = [{ label: '1 小时', value: 3600 }, { label: '6 小时', value: 21600 }, { label: '24 小时', value: 86400 }, { label: '7 天', value: 604800 }, { label: '30 天', value: 2592000 }]
const mbps = (v) => (!v ? '未设置' : v >= 1000 && v % 1000 === 0 ? `${v / 1000} Gbps` : `${v} Mbps`)
const xrayOutdated = computed(() => !!(props.releases.xray && props.sv.xray_version && props.releases.xray.replace(/^v/, '') !== props.sv.xray_version.replace(/^v/, '')))

const fmtTime = (ts) => {
  const d = new Date(ts * 1000)
  return props.range > 86400
    ? `${d.getMonth() + 1}-${d.getDate()} ${String(d.getHours()).padStart(2, '0')}:00`
    : `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}
function lineOpt(series, yFmt, max) {
  return {
    grid: { left: 52, right: 12, top: 28, bottom: 26 },
    tooltip: { trigger: 'axis', valueFormatter: yFmt },
    legend: { top: 0, right: 0, itemWidth: 12, itemHeight: 8, textStyle: { fontSize: 11 } },
    xAxis: { type: 'category', data: props.metrics.map((m) => fmtTime(m.TS)), axisLabel: { fontSize: 10, color: '#5f6368' }, axisLine: { lineStyle: { color: '#d5d8dd' } } },
    yAxis: { type: 'value', max, axisLabel: { fontSize: 10, color: '#5f6368', formatter: yFmt }, splitLine: { lineStyle: { color: '#eef0f2' } } },
    series: series.map(([name, key, color, area]) => ({ name, type: 'line', showSymbol: false, smooth: 0.2, lineStyle: { width: 1.5, color }, itemStyle: { color }, areaStyle: area ? { color, opacity: 0.08 } : undefined, data: props.metrics.map(key) })),
  }
}
const cpuOpt = computed(() => lineOpt([['CPU', (m) => +m.CPU.toFixed(1), '#0f7b45', true], ['Steal', (m) => +m.Steal.toFixed(1), '#b8650a']], (v) => v + '%', 100))
const memOpt = computed(() => {
  const t = sys.value.mem_total
  return lineOpt([['内存', (m) => (t ? +(m.MemUsed / t * 100).toFixed(1) : 0), '#2f6fd0', true]], (v) => v + '%', 100)
})
const netOpt = computed(() => lineOpt([['出站', (m) => m.NetOut, '#0f7b45', true], ['入站', (m) => m.NetIn, '#2f6fd0']], (v) => rate(v)))
const connOpt = computed(() => lineOpt([['TCP', (m) => m.TCP, '#0f7b45'], ['UDP', (m) => m.UDP, '#b8650a'], ['Load', (m) => m.Load1, '#5f6368']], (v) => String(v)))

const bhColors = { telecom: '#2f6fd0', unicom: '#b8650a', mobile: '#0f7b45' }
const bhEmpty = computed(() => {
  const t = props.backhaul.targets
  if (!t.length) return '还没有探测目标，去「延迟」页同步 zstatic 节点或添加自定义目标。'
  if (!t.some((x) => x.enabled && ['telecom', 'unicom', 'mobile'].includes(x.carrier))) return '没有启用的国内三网探测目标，去「延迟」页启用。'
  if (props.sv.status !== 'online') return '机器不在线，上线后约 1 分钟出数据。'
  return '探测目标已启用，但这台机器近 24 小时没有上报结果。可能刚连上（约 1 分钟出第一轮）；一直没有的话，用 journalctl -u vpsagent 看 Agent 日志。'
})
const bhLoss = computed(() => {
  const { targets, series } = props.backhaul
  return ['telecom', 'unicom', 'mobile']
    .map((c) => ({ c, v: overallLoss(targets.filter((t) => t.carrier === c).flatMap((t) => series[t.id] || [])) }))
    .filter((x) => x.v != null)
})
const bhOpt = computed(() => {
  const { targets, series } = props.backhaul
  const buckets = new Map()
  for (const tg of targets) {
    for (const r of series[tg.id] || []) {
      const k = Math.floor(r.ts / 300) * 300
      if (!buckets.has(k)) buckets.set(k, { telecom: { v: [], l: [] }, unicom: { v: [], l: [] }, mobile: { v: [], l: [] } })
      const b = buckets.get(k)[tg.carrier]
      if (!b) continue
      b.l.push(r.loss)
      if (r.loss < 100 && r.avg > 0) b.v.push(r.avg)
    }
  }
  const ks = [...buckets.keys()].sort((a, b) => a - b)
  if (!ks.length) return null
  const med = (a) => { if (!a.length) return null; const s = [...a].sort((x, y) => x - y); const m = s.length >> 1; return Math.round(s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2) }
  const x = ks.map((k) => { const d = new Date(k * 1000); return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}` })
  const lossOf = (c, i) => { const l = buckets.get(ks[i])[c].l; return l.length ? Math.round(l.reduce((a, b) => a + b, 0) / l.length) : 0 }
  return {
    grid: { left: 44, right: 10, top: 28, bottom: 24 },
    legend: { top: 0, itemWidth: 14, textStyle: { fontSize: 12 } },
    tooltip: {
      trigger: 'axis',
      formatter: (ps) => `${ps[0].axisValue}<br>` + ps.map((p) => {
        const c = ['telecom', 'unicom', 'mobile'][p.seriesIndex]
        const loss = lossOf(c, p.dataIndex)
        return `${p.marker}${p.seriesName} ${p.value == null ? '超时' : p.value + ' ms'}${loss ? ` · 丢包 ${loss}%` : ''}`
      }).join('<br>'),
    },
    xAxis: { type: 'category', data: x, axisLabel: { fontSize: 11 } },
    yAxis: { type: 'value', name: 'ms', nameTextStyle: { fontSize: 11 }, axisLabel: { fontSize: 11 }, splitLine: { lineStyle: { color: '#eef0f2' } } },
    series: ['telecom', 'unicom', 'mobile'].map((c) => ({
      name: carrierLabel[c], type: 'line', showSymbol: false, connectNulls: false, lineStyle: { width: 1.5 }, color: bhColors[c],
      data: ks.map((k) => med(buckets.get(k)[c].v)),
    })),
  }
})
</script>

<template>
  <div class="sections">
    <section class="s-group">
      <div class="s-head">
        <h2>监控</h2>
        <p>CPU、内存、网络和连接数，按所选时间范围显示。</p>
      </div>
      <div class="card s-card">
        <div class="s-card-head">
          <span class="f-hint">时间范围</span>
          <span class="spacer" />
          <n-select :value="range" :options="ranges" size="small" style="width: 110px" aria-label="时间范围" @update:value="(v) => emit('update:range', v)" />
        </div>
        <div v-if="!metrics.length" class="empty">暂无数据</div>
        <div v-else class="charts">
          <div><div class="chart-h">CPU</div><chart :option="cpuOpt" height="180px" /></div>
          <div><div class="chart-h">内存</div><chart :option="memOpt" height="180px" /></div>
          <div><div class="chart-h">网络</div><chart :option="netOpt" height="180px" /></div>
          <div><div class="chart-h">连接数 / 负载</div><chart :option="connOpt" height="180px" /></div>
        </div>
      </div>
    </section>

    <section class="s-group">
      <div class="s-head">
        <h2>回程延迟</h2>
        <p>近 24 小时到国内三网的延迟中位数。<router-link :to="{ name: 'latency', query: { server: sv.id } }">查看详细</router-link></p>
        <div v-if="bhOpt && bhLoss.length" class="bh-loss" title="近 24 小时的整体丢包率（总丢包数 / 总发包数）">
          <span class="muted">24 小时整体丢包</span>
          <span v-for="x in bhLoss" :key="x.c" class="bh-loss-item"><span class="muted">{{ carrierLabel[x.c] }}</span> <b :class="lossClass(x.v)">{{ lossText(x.v) }}</b></span>
        </div>
      </div>
      <div class="card s-card">
        <div v-if="!bhOpt" class="empty">还没有探测数据<div class="small muted" style="margin-top: 6px">{{ bhEmpty }}</div></div>
        <div v-else class="chart-pad"><chart :option="bhOpt" height="200px" /></div>
      </div>
    </section>

    <section class="s-group">
      <div class="s-head">
        <h2>本周期流量</h2>
        <p>{{ date(usage.cycle_start) }} 至 {{ date(usage.cycle_end) }}，每月 {{ sv.reset_day || 1 }} 日重置。额度和计费口径在「资产」里修改。</p>
      </div>
      <div class="card s-card">
        <div class="usage">
          <div class="big">{{ bytes(usage.counted) }}<small v-if="sv.quota_bytes"> / {{ bytes(sv.quota_bytes, 0) }}</small><small v-else> · 不限额</small></div>
          <n-progress v-if="sv.quota_bytes" type="line" :percentage="Math.min(100, usage.percent || 0)" :show-indicator="false" :height="6" :color="usage.percent >= 90 ? '#b4233a' : '#0f7b45'" />
          <span v-if="sv.quota_bytes" :class="['small', usage.forecast_at ? 'bad-text' : 'muted']">
            {{ usage.forecast_at ? '按当前速度，预计 ' + date(usage.forecast_at) + ' 用完额度' : '按当前速度，本周期不会超额' }}
          </span>
        </div>
        <div class="field f-inline"><div class="f-label"><span class="f-name">带宽</span></div><div class="f-ctl"><span class="f-val">{{ mbps(sv.bandwidth_mbps) }}</span></div></div>
        <div class="field f-inline"><div class="f-label"><span class="f-name">出站 / 入站</span></div><div class="f-ctl"><span class="f-val">↑ {{ bytes(usage.up) }} · ↓ {{ bytes(usage.down) }}</span></div></div>
        <div class="field f-inline"><div class="f-label"><span class="f-name">计费口径</span></div><div class="f-ctl"><span class="f-val">{{ countModeText[sv.count_mode] || '只计出站' }}</span></div></div>
        <div class="field f-inline"><div class="f-label"><span class="f-name">超额动作</span></div><div class="f-ctl"><span class="f-val">{{ sv.on_exceeded === 'stop_xray' ? '告警并停止 Xray' : '仅告警（80% / 90% / 100%）' }}</span></div></div>
      </div>
    </section>

    <section class="s-group">
      <div class="s-head"><h2>系统信息</h2><p>由 Agent 自动上报。</p></div>
      <div class="card s-card">
        <div class="field f-inline"><div class="f-label"><span class="f-name">IPv4</span></div><div class="f-ctl"><span class="f-val mono">{{ sv.ipv4 || '—' }}</span></div></div>
        <div class="field f-inline"><div class="f-label"><span class="f-name">IPv6</span></div><div class="f-ctl"><span class="f-val mono">{{ sv.ipv6 || '—' }}</span></div></div>
        <div class="field f-inline"><div class="f-label"><span class="f-name">系统</span><span v-if="sys.kernel" class="f-hint">内核 {{ sys.kernel }}</span></div><div class="f-ctl"><span class="f-val">{{ sys.os || '—' }}</span></div></div>
        <div class="field f-inline"><div class="f-label"><span class="f-name">配置</span><span v-if="sys.cpu_model" class="f-hint">{{ sys.cpu_model }}</span></div><div class="f-ctl"><span class="f-val">{{ sys.cpu_cores ? sys.cpu_cores + ' vCPU · ' + bytes(sys.mem_total, 0) + ' · ' + bytes(sys.disk_total, 0) : '—' }}</span></div></div>
        <div class="field f-inline"><div class="f-label"><span class="f-name">虚拟化</span></div><div class="f-ctl"><span class="f-val">{{ [sys.virt, sys.arch].filter(Boolean).join(' · ') || '—' }}</span></div></div>
        <div class="field f-inline"><div class="f-label"><span class="f-name">磁盘</span></div><div class="f-ctl"><span class="f-val">{{ sys.disk_total ? bytes(sv.latest?.disk_used) + ' / ' + bytes(sys.disk_total, 0) : '—' }}</span></div></div>
        <div class="field f-inline"><div class="f-label"><span class="f-name">连接数</span></div><div class="f-ctl"><span class="f-val">TCP {{ sv.latest?.tcp ?? '—' }} · UDP {{ sv.latest?.udp ?? '—' }}</span></div></div>
      </div>
    </section>

    <section class="s-group">
      <div class="s-head"><h2>网络优化</h2><p>TCP 拥塞控制与队列算法。调整在「工具 → 网络优化」里进行。</p></div>
      <div class="card s-card">
        <div class="field f-inline"><div class="f-label"><span class="f-name">拥塞控制</span></div><div class="f-ctl"><span :class="['tag', sys.congestion === 'bbr' ? 'ok' : 'warn']">{{ sys.congestion === 'bbr' ? 'BBR 已开启' : (sys.congestion || '未知') }}</span></div></div>
        <div class="field f-inline"><div class="f-label"><span class="f-name">队列</span></div><div class="f-ctl"><span class="f-val mono">{{ sys.qdisc || '—' }}</span></div></div>
        <div v-if="!sv.switches?.allow_system_tune" class="banner warn">这台机器的 Agent 关闭了系统调优（本地配置 allow_system_tune），不能在面板里调整。</div>
        <div class="f-actions">
          <button v-if="sv.switches?.allow_system_tune" class="link-btn small" :disabled="!sv.online" @click="emit('run-tool', 'restore_net', '恢复网络参数')">恢复调优前的参数</button>
          <span class="spacer" />
          <n-button :disabled="!sv.online || !sv.switches?.allow_system_tune" @click="router.push({ name: 'tools', query: { server: sv.id, tab: 'bbr' } })">调整</n-button>
        </div>
      </div>
    </section>

    <section class="s-group">
      <div class="s-head"><h2>版本</h2><p>批量更新在<router-link :to="{ name: 'updates' }">更新中心</router-link>。</p></div>
      <div class="card s-card">
        <div class="field f-inline">
          <div class="f-label"><span class="f-name">Agent</span><span v-if="releases.agent && sv.agent_version && releases.agent !== sv.agent_version" class="f-hint">最新 {{ releases.agent }}</span></div>
          <div class="f-ctl ver"><span class="f-val mono">{{ sv.agent_version || '—' }}</span><span v-if="releases.agent && sv.agent_version && releases.agent !== sv.agent_version" class="tag warn">可更新</span></div>
        </div>
        <div v-if="sv.mode === 'node'" class="field f-inline">
          <div class="f-label">
            <span class="f-name">Xray</span>
            <span v-if="sv.xray_locked_version" class="f-hint">已锁定在 {{ sv.xray_locked_version }}，批量更新会跳过</span>
            <span v-else-if="xrayOutdated" class="f-hint">最新 {{ releases.xray }}</span>
          </div>
          <div class="f-ctl ver">
            <span class="f-val mono">{{ sv.xray_version || '—' }}</span>
            <span v-if="sv.xray_locked_version" class="tag">锁定</span>
            <span v-else-if="xrayOutdated" class="tag warn">可更新</span>
          </div>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.chart-pad { padding: 12px 0; }
.usage { display: flex; flex-direction: column; gap: 8px; padding: 16px 0 14px; }
.f-val { font-size: 13.5px; text-align: right; overflow-wrap: anywhere; }
.f-ctl.ver { display: flex; align-items: center; gap: 8px; }
.s-card > .charts { padding: 4px 0 16px; }
.charts { display: grid; grid-template-columns: 1fr 1fr; gap: 12px 20px; }
.chart-h { font-size: 12px; color: var(--c-muted); }
.big { font-size: 26px; font-weight: 700; font-variant-numeric: tabular-nums; }
.big small { font-size: 15px; color: var(--c-muted); font-weight: 500; }
.bad-text { color: var(--c-danger); }
.bh-loss { display: flex; flex-wrap: wrap; align-items: baseline; gap: 2px 12px; font-size: 13px; }
.bh-loss > .muted:first-child { flex-basis: 100%; }
.bh-loss b { font-weight: 600; font-variant-numeric: tabular-nums; }
.bh-loss b.muted { font-weight: 500; }
@media (max-width: 900px) { .charts { grid-template-columns: 1fr; } }
</style>
