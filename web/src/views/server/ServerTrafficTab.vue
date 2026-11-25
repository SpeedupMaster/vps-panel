<script setup>
import { computed } from 'vue'
import { bytes, date } from '../../format'
import Chart from '../../components/Chart.vue'

const props = defineProps({
  sv: { type: Object, required: true },
  traffic: { type: Object, default: null },
})

const usage = computed(() => props.sv.usage || {})
const dailyOpt = computed(() => {
  const d = props.traffic?.daily || []
  return {
    grid: { left: 56, right: 12, top: 28, bottom: 26 },
    tooltip: { trigger: 'axis', valueFormatter: (v) => bytes(v) },
    legend: { top: 0, right: 0, itemWidth: 12, itemHeight: 8 },
    xAxis: { type: 'category', data: d.map((r) => { const x = new Date(r.hour * 1000); return `${x.getMonth() + 1}-${x.getDate()}` }), axisLabel: { fontSize: 10, color: '#5f6368' } },
    yAxis: { type: 'value', axisLabel: { fontSize: 10, color: '#5f6368', formatter: (v) => bytes(v, 0) }, splitLine: { lineStyle: { color: '#eef0f2' } } },
    series: [
      { name: '出站', type: 'bar', stack: 't', data: d.map((r) => r.up), itemStyle: { color: '#0f7b45' }, barMaxWidth: 18 },
      { name: '入站', type: 'bar', stack: 't', data: d.map((r) => r.down), itemStyle: { color: '#8cc7a6' }, barMaxWidth: 18 },
    ],
  }
})
const dailyTotal = computed(() => (props.traffic?.daily || []).reduce((a, r) => a + r.up + r.down, 0))
const routeTotal = computed(() => (props.traffic?.routes || []).reduce((a, r) => a + r.up + r.down, 0))
</script>

<template>
  <div v-if="!traffic" class="empty">加载中…</div>
  <div v-else class="sections">
    <section class="s-group">
      <div class="s-head">
        <h2>近 30 天</h2>
        <p>网卡统计的每日出站与入站，包含所有流量。</p>
      </div>
      <div class="card s-card">
        <div class="s-card-head">
          <span class="f-hint">合计</span>
          <b class="mono">{{ bytes(dailyTotal) }}</b>
        </div>
        <div v-if="!traffic.daily?.length" class="empty">还没有流量数据</div>
        <div v-else class="chart-pad"><chart :option="dailyOpt" height="260px" /></div>
      </div>
    </section>

    <section class="s-group">
      <div class="s-head">
        <h2>本周期按线路</h2>
        <p>{{ date(usage.cycle_start) }} 至 {{ date(usage.cycle_end) }}。代理流量按线路统计，剩下的是系统、探针等非代理流量。</p>
      </div>
      <div class="card s-card">
        <div class="field f-inline">
          <div class="f-label"><span class="f-name">本周期合计</span><span class="f-hint">出站 + 入站</span></div>
          <div class="f-ctl"><span class="f-val mono">{{ bytes(traffic.usage.up + traffic.usage.down) }}</span></div>
        </div>
        <div class="field f-inline">
          <div class="f-label"><span class="f-name">代理流量</span><span class="f-hint">{{ traffic.routes?.length || 0 }} 条线路</span></div>
          <div class="f-ctl"><span class="f-val mono">{{ bytes(routeTotal) }}</span></div>
        </div>
        <div class="field f-inline">
          <div class="f-label"><span class="f-name">非代理流量</span><span class="f-hint">系统、探针等</span></div>
          <div class="f-ctl"><span class="f-val mono">{{ bytes(Math.max(0, (traffic.usage.up + traffic.usage.down) - routeTotal)) }}</span></div>
        </div>
        <div v-if="traffic.routes?.length" class="table-wrap route-table">
          <table class="plain">
            <thead><tr><th>线路</th><th>上行</th><th>下行</th><th>合计</th></tr></thead>
            <tbody>
              <tr v-for="r in traffic.routes" :key="r.id"><td>{{ r.name }}</td><td class="mono">{{ bytes(r.up) }}</td><td class="mono">{{ bytes(r.down) }}</td><td class="mono">{{ bytes(r.up + r.down) }}</td></tr>
            </tbody>
          </table>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.chart-pad { padding: 12px 0; }
.f-val { font-size: 13.5px; text-align: right; overflow-wrap: anywhere; }
.route-table { border-top: 1px solid var(--c-border); }
</style>
