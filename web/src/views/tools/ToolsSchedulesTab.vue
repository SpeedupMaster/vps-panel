<script setup>
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { NButton, NSelect, NInput, NModal, NSwitch } from 'naive-ui'
import { api } from '../../api'
import { serverName } from '../../store'
import { run, ask, idsCsv } from '../../helpers'
import { dt } from '../../format'

defineProps({ serverOpts: { type: Array, required: true } })

const schedules = ref([])
const actions = ref({})
const sched = ref({ show: false, s: {} })
const deletingSched = ref(0)
let schedTimer = null

const serverSpec = [{ label: '全部服务器', value: 'all' }, { label: '全部节点', value: 'nodes' }, { label: '入口机', value: 'entries' }, { label: '落地机', value: 'landings' }, { label: '指定服务器', value: 'ids' }]

async function loadSchedules() {
  schedules.value = (await run(() => api.get('/schedules'))) || []
  if (!Object.keys(actions.value).length) actions.value = (await api.get('/tools').catch(() => ({})))?.schedule_actions || {}
}
function editSched(s) {
  const base = s ? { ...s } : { name: '', cron: '04:00', action: 'update_geo', servers: 'nodes', enabled: true }
  const isIds = !['all', 'nodes', 'entries', 'landings', ''].includes(base.servers)
  sched.value = { show: true, s: { ...base, spec: isIds ? 'ids' : base.servers || 'all', ids: isIds ? idsCsv(base.servers) : [] } }
}
async function saveSched() {
  const s = sched.value.s
  const body = { id: s.id, name: s.name || actions.value[s.action], cron: s.cron, action: s.action, servers: s.spec === 'ids' ? s.ids.join(',') : s.spec, enabled: s.enabled }
  if (await run(() => (s.id ? api.put(`/schedules/${s.id}`, body) : api.post('/schedules', body)), '已保存')) { sched.value.show = false; loadSchedules() }
}
async function delSched(s) {
  if (deletingSched.value) return
  deletingSched.value = s.id
  try {
    if (!(await ask('删除定时任务', `删除「${s.name}」？`, { danger: true }))) return
    if (await run(() => api.del(`/schedules/${s.id}`), '已删除')) loadSchedules()
  } finally { deletingSched.value = 0 }
}
async function runSchedNow(s) {
  if (await run(() => api.post(`/schedules/${s.id}/run`), '已开始执行')) {
    clearTimeout(schedTimer)
    schedTimer = setTimeout(loadSchedules, 1500)
  }
}
async function toggleSched(s, v) { if (await run(() => api.put(`/schedules/${s.id}`, { ...s, enabled: v }))) s.enabled = v }
function specText(v) {
  const o = serverSpec.find((x) => x.value === v)
  return o ? o.label : idsCsv(v).map(serverName).join('、')
}
function cronText(c) {
  if (c.startsWith('@every')) return '每 ' + c.slice(7).replace('h', ' 小时').replace('m', ' 分钟')
  const [t, d] = c.split('@')
  if (!d) return '每天 ' + t
  return '每周' + d.split(',').map((x) => '日一二三四五六'[Number(x) % 7]).join('、') + ' ' + t
}

onMounted(loadSchedules)
onBeforeUnmount(() => clearTimeout(schedTimer))
</script>

<template>
  <div class="sec-head">
    <div class="s-head"><h2>定时任务</h2><p>按面板所在时区定时执行检测、更新或脚本。</p></div>
    <span class="spacer" />
    <n-button type="primary" @click="editSched(null)">新建定时任务</n-button>
  </div>
  <div class="card s-card">
    <div v-if="!schedules.length" class="empty">没有定时任务</div>
    <div v-else class="table-wrap">
      <table class="plain">
        <thead><tr><th>名称</th><th>时间</th><th>动作</th><th>服务器</th><th>上次执行</th><th>启用</th><th /></tr></thead>
        <tbody>
          <tr v-for="s in schedules" :key="s.id">
            <td>{{ s.name }}</td><td class="nowrap">{{ cronText(s.cron) }}</td><td>{{ actions[s.action] || s.action }}</td>
            <td class="small">{{ specText(s.servers) }}</td><td class="nowrap small">{{ s.last_run && !s.last_run.startsWith('0001') ? dt(s.last_run) : '—' }}</td>
            <td><n-switch size="small" :value="s.enabled" @update:value="(v) => toggleSched(s, v)" /></td>
            <td class="nowrap" style="text-align: right">
              <n-button size="tiny" quaternary @click="runSchedNow(s)">立即执行</n-button>
              <n-button size="tiny" quaternary @click="editSched(s)">编辑</n-button>
              <n-button size="tiny" quaternary type="error" :loading="deletingSched === s.id" :disabled="!!deletingSched" @click="delSched(s)">删除</n-button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>

  <n-modal v-model:show="sched.show" preset="card" :title="sched.s.id ? '编辑定时任务' : '新建定时任务'" style="width: min(480px, 96vw)">
    <div class="m-form">
      <div class="field"><div class="f-label"><span class="f-name">动作</span></div><div class="f-ctl"><n-select v-model:value="sched.s.action" :options="Object.entries(actions).map(([value, label]) => ({ label, value }))" /></div></div>
      <div class="field"><div class="f-label"><span class="f-name">名称</span></div><div class="f-ctl"><n-input v-model:value="sched.s.name" :placeholder="'默认 ' + (actions[sched.s.action] || '')" /></div></div>
      <div class="field"><div class="f-label"><span class="f-name">时间</span><span class="f-hint">「HH:MM」每天；「HH:MM@1,3」每周一、三；「@every 6h」每 6 小时。按面板所在时区</span></div><div class="f-ctl"><n-input v-model:value="sched.s.cron" placeholder="04:00" /></div></div>
      <div class="field">
        <div class="f-label"><span class="f-name">服务器</span></div>
        <div class="f-ctl ctl-col"><n-select v-model:value="sched.s.spec" :options="serverSpec" />
        <n-select v-if="sched.s.spec === 'ids'" v-model:value="sched.s.ids" multiple :options="serverOpts" placeholder="选择服务器" /></div>
      </div>
      <div class="field f-bool"><div class="f-label"><span class="f-name">启用</span></div><div class="f-ctl"><n-switch v-model:value="sched.s.enabled" aria-label="启用" /></div></div>
    </div>
    <template #footer><div class="m-foot"><n-button @click="sched.show = false">取消</n-button><n-button type="primary" @click="saveSched">保存</n-button></div></template>
  </n-modal>
</template>
