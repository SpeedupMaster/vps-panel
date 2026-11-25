<script setup>
import { ref, computed, onMounted } from 'vue'
import { NButton, NSelect, NSwitch, NModal, NInput, NInputNumber } from 'naive-ui'
import { api } from '../../api'
import { state, serverName } from '../../store'
import { run, ask, idsCsv } from '../../helpers'
import { dur, sevLabel, sevClass } from '../../format'

const props = defineProps({ channels: { type: Array, required: true } })

const rules = ref([])
const kinds = ref({})
const editing = ref({ show: false, r: {} })
const thresholdKinds = { cpu: '%', mem: '%', disk: '%', steal: '%', load: '（每核）', backhaul_loss: '%', backhaul_latency: 'ms', traffic: '%' }
const kindOpts = computed(() => Object.entries(kinds.value).map(([value, label]) => ({ label, value })))
const scopeOpts = [{ label: '全部服务器', value: 'all' }, { label: '仅节点', value: 'node' }, { label: '仅探针', value: 'probe' }, { label: '入口机', value: 'entry' }, { label: '落地机', value: 'landing' }, { label: '指定服务器', value: 'ids' }]
const sevOptsRule = [{ label: '严重', value: 'critical' }, { label: '警告', value: 'warning' }, { label: '提醒', value: 'info' }]
const serverOpts = computed(() => state.servers.map((s) => ({ label: s.name, value: s.id })))

async function loadRules() { rules.value = (await run(() => api.get('/alerts/rules'))) || [] }
function editRule(r) {
  const base = r ? { ...r } : { name: '', kind: 'cpu', threshold: 90, duration: 300, cooldown: 3600, severity: 'warning', scope: 'all', channels: '', enabled: true }
  const ids = String(base.scope).startsWith('ids:') ? idsCsv(base.scope.slice(4)) : []
  editing.value = { show: true, r: { ...base, scopeKind: ids.length ? 'ids' : base.scope, ids, chIds: idsCsv(base.channels) } }
}
async function saveRule() {
  const r = editing.value.r
  const body = {
    id: r.id, name: r.name || kinds.value[r.kind], kind: r.kind, threshold: r.threshold || 0, duration: r.duration || 0,
    cooldown: r.cooldown || 0, severity: r.severity, enabled: r.enabled,
    scope: r.scopeKind === 'ids' ? 'ids:' + r.ids.join(',') : r.scopeKind, channels: r.chIds.join(','),
  }
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
  if (String(s).startsWith('ids:')) return idsCsv(s.slice(4)).map(serverName).join('、')
  return scopeOpts.find((o) => o.value === s)?.label || s
}
const chName = (ids) => (ids ? idsCsv(ids).map((id) => props.channels.find((c) => c.id === id)?.name || '#' + id).join('、') : '全部渠道')

onMounted(() => {
  loadRules()
  api.get('/alerts/kinds').then((k) => { kinds.value = k }).catch(() => {})
})
</script>

<template>
  <div class="sec-head">
    <div class="s-head"><h2>告警规则</h2><p>条件持续满足时按严重度通知所选渠道；告警未恢复时，按重复提醒间隔再次通知。</p></div>
    <span class="spacer" />
    <n-button type="primary" @click="editRule(null)">新建规则</n-button>
  </div>
  <div class="card s-card">
    <div v-if="!rules.length" class="empty">还没有告警规则</div>
    <template v-else>
      <div class="table-wrap hide-mobile">
        <table class="plain">
          <thead><tr><th>规则</th><th>条件</th><th>范围</th><th>重复提醒</th><th>严重度</th><th>渠道</th><th>启用</th><th /></tr></thead>
          <tbody>
            <tr v-for="r in rules" :key="r.id">
              <td><b>{{ r.name }}</b><div class="muted small">{{ kinds[r.kind] || r.kind }}</div></td>
              <td class="small">{{ cond(r) }}</td>
              <td class="small">{{ scopeText(r.scope) }}</td>
              <td class="small nowrap">{{ r.cooldown ? dur(r.cooldown) : '—' }}</td>
              <td><span :class="['tag', sevClass[r.severity]]">{{ sevLabel[r.severity] }}</span></td>
              <td class="small">{{ chName(r.channels) }}</td>
              <td><n-switch size="small" :value="r.enabled" :aria-label="'启用 ' + r.name" @update:value="(v) => toggleRule(r, v)" /></td>
              <td class="nowrap" style="text-align: right"><n-button size="tiny" quaternary @click="editRule(r)">编辑</n-button><n-button size="tiny" quaternary type="error" @click="delRule(r)">删除</n-button></td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="m-list">
        <div v-for="r in rules" :key="r.id" :class="['m-item', { dim: !r.enabled }]">
          <div class="row">
            <span :class="['tag', sevClass[r.severity]]">{{ sevLabel[r.severity] }}</span>
            <b>{{ r.name }}</b><span class="spacer" />
            <n-switch size="small" :value="r.enabled" :aria-label="'启用 ' + r.name" @update:value="(v) => toggleRule(r, v)" />
          </div>
          <div class="m-row"><span>条件</span><span>{{ cond(r) }}</span></div>
          <div class="m-row"><span>范围</span><span>{{ scopeText(r.scope) }}</span></div>
          <div class="m-row"><span>渠道</span><span>{{ chName(r.channels) }}</span></div>
          <div class="m-row"><span>重复提醒</span><span>{{ r.cooldown ? dur(r.cooldown) : '—' }}</span></div>
          <div class="row">
            <span class="spacer" />
            <n-button size="small" @click="editRule(r)">编辑</n-button>
            <n-button size="small" type="error" quaternary @click="delRule(r)">删除</n-button>
          </div>
        </div>
      </div>
    </template>
  </div>

  <n-modal v-model:show="editing.show" preset="card" :title="editing.r.id ? '编辑规则' : '新建规则'" style="width: min(560px, 96vw)">
    <div class="m-form">
      <div class="field"><div class="f-label"><span class="f-name">类型</span></div><div class="f-ctl"><n-select v-model:value="editing.r.kind" :options="kindOpts" filterable /></div></div>
      <div class="field"><div class="f-label"><span class="f-name">名称</span></div><div class="f-ctl"><n-input v-model:value="editing.r.name" :placeholder="'默认 ' + (kinds[editing.r.kind] || '')" /></div></div>
      <div v-if="thresholdKinds[editing.r.kind] !== undefined" class="field">
        <div class="f-label"><span class="f-name">阈值</span><span class="f-hint">超过这个值算异常</span></div>
        <div class="f-ctl"><n-input-number v-model:value="editing.r.threshold" :min="0" :show-button="false"><template #suffix><span class="f-unit">{{ thresholdKinds[editing.r.kind] }}</span></template></n-input-number></div>
      </div>
      <div class="field">
        <div class="f-label"><span class="f-name">持续</span><span class="f-hint">异常持续这么久才告警，0 = 立即</span></div>
        <div class="f-ctl"><n-input-number v-model:value="editing.r.duration" :min="0" :step="60" :show-button="false" placeholder="0"><template #suffix><span class="f-unit">秒</span></template></n-input-number></div>
      </div>
      <div class="field">
        <div class="f-label"><span class="f-name">重复提醒间隔</span><span class="f-hint">告警未恢复时，隔多久再提醒一次</span></div>
        <div class="f-ctl"><n-input-number v-model:value="editing.r.cooldown" :min="0" :step="600" :show-button="false" placeholder="0"><template #suffix><span class="f-unit">秒</span></template></n-input-number></div>
      </div>
      <div class="field"><div class="f-label"><span class="f-name">严重度</span></div><div class="f-ctl"><n-select v-model:value="editing.r.severity" :options="sevOptsRule" /></div></div>
      <div class="field">
        <div class="f-label"><span class="f-name">范围</span></div>
        <div class="f-ctl ctl-col"><n-select v-model:value="editing.r.scopeKind" :options="scopeOpts" />
        <n-select v-if="editing.r.scopeKind === 'ids'" v-model:value="editing.r.ids" multiple :options="serverOpts" placeholder="选择服务器" /></div>
      </div>
      <div class="field">
        <div class="f-label"><span class="f-name">通知渠道</span><span class="f-hint">不选 = 全部启用的渠道</span></div>
        <div class="f-ctl"><n-select v-model:value="editing.r.chIds" multiple :options="channels.map((c) => ({ label: c.name, value: c.id }))" placeholder="全部渠道" /></div>
      </div>
      <div class="field f-bool"><div class="f-label"><span class="f-name">启用</span></div><div class="f-ctl"><n-switch v-model:value="editing.r.enabled" aria-label="启用" /></div></div>
    </div>
    <template #footer><div class="m-foot"><n-button @click="editing.show = false">取消</n-button><n-button type="primary" @click="saveRule">保存</n-button></div></template>
  </n-modal>
</template>

<style scoped>
.m-item.dim { opacity: .6; }
.m-row > :last-child { text-align: right; overflow-wrap: anywhere; }
</style>
