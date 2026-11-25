<script setup>
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { NButton, NSelect, NDropdown } from 'naive-ui'
import { api } from '../../api'
import { state, on, refreshAlertCount } from '../../store'
import { run } from '../../helpers'
import { ago, dt, dur, sevLabel, sevClass } from '../../format'

const emit = defineEmits(['update:firing'])

const events = ref([])
const firing = ref(0)
const fStatus = ref('firing')
const fSev = ref('')
const fServer = ref(0)

const statusOpts = [{ label: '进行中', value: 'firing' }, { label: '通知', value: 'info' }, { label: '已恢复', value: 'resolved' }, { label: '全部', value: 'all' }]
const sevOpts = [{ label: '全部级别', value: '' }, { label: '严重', value: 'critical' }, { label: '警告', value: 'warning' }, { label: '提醒', value: 'info' }]
const serverOpts = computed(() => [{ label: '全部服务器', value: 0 }, ...state.servers.map((s) => ({ label: s.name, value: s.id }))])
const silenceOpts = [{ label: '静默 1 小时', key: 60 }, { label: '静默 6 小时', key: 360 }, { label: '静默 24 小时', key: 1440 }, { label: '静默 7 天', key: 10080 }]

let evAbort = null
async function loadEvents() {
  evAbort?.abort()
  const ac = new AbortController()
  evAbort = ac
  const q = new URLSearchParams({ status: fStatus.value || 'all', limit: '300' })
  if (fSev.value) q.set('severity', fSev.value)
  if (fServer.value) q.set('server_id', fServer.value)
  const r = await run(() => api.get('/alerts/events?' + q, { signal: ac.signal }))
  if (ac.signal.aborted || !r) return
  events.value = r.items
  firing.value = r.firing
  state.alertsFiring = r.firing
  emit('update:firing', r.firing)
}
function lasting(e) {
  if (e.status === 'info') return '—'
  const end = e.resolved_at ? new Date(e.resolved_at).getTime() : Date.now()
  return dur((end - new Date(e.started_at).getTime()) / 1000)
}
const silenced = (e) => e.silenced_to && new Date(e.silenced_to) > new Date()
async function ack(e) { if (await run(() => api.post(`/alerts/events/${e.id}/ack`))) { e.acked = true; refreshAlertCount(); loadEvents() } }
async function silence(e, minutes) { if (await run(() => api.post(`/alerts/events/${e.id}/silence`, { minutes }), '已静默')) loadEvents() }
async function ackAll() { if (await run(() => api.post('/alerts/events/ack-all'), '已全部确认')) { loadEvents(); refreshAlertCount() } }

onMounted(loadEvents)
watch([fStatus, fSev, fServer], loadEvents)
const off = on('alert', loadEvents)
onBeforeUnmount(() => { off(); evAbort?.abort() })
</script>

<template>
  <div class="sec-head">
    <div class="s-head"><h2>告警事件</h2><p>进行中的告警可以确认或静默一段时间，条件恢复后自动关闭。</p></div>
    <span class="spacer" />
    <n-select v-model:value="fStatus" :options="statusOpts" aria-label="状态" style="width: 100px" />
    <n-select v-model:value="fSev" :options="sevOpts" aria-label="严重度" style="width: 110px" />
    <n-select v-model:value="fServer" :options="serverOpts" aria-label="服务器" style="width: 140px" filterable />
    <n-button :disabled="!firing" @click="ackAll">全部确认</n-button>
  </div>
  <div class="card s-card">
    <div v-if="!events.length" class="empty">没有告警</div>
    <template v-else>
      <div class="table-wrap hide-mobile">
        <table class="plain">
          <thead><tr><th>严重度</th><th>服务器</th><th>内容</th><th>开始时间</th><th>持续</th><th>状态</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="e in events" :key="e.id">
              <td><span :class="['tag', sevClass[e.severity]]">{{ sevLabel[e.severity] }}</span></td>
              <td class="nowrap">{{ e.server_name || '面板' }}</td>
              <td><div class="small muted">{{ e.kind_label }}</div>{{ e.text }}</td>
              <td class="nowrap">{{ dt(e.started_at) }}</td>
              <td class="nowrap">{{ lasting(e) }}</td>
              <td class="nowrap">
                <span v-if="e.status === 'resolved'" class="tag ok">已恢复</span>
                <span v-else-if="e.status === 'info'" class="tag info">{{ e.acked ? '通知 · 已确认' : '通知' }}</span>
                <span v-else-if="silenced(e)" class="tag">静默至 {{ dt(e.silenced_to) }}</span>
                <span v-else-if="e.acked" class="tag info">已确认</span>
                <span v-else :class="['tag', sevClass[e.severity]]">进行中</span>
              </td>
              <td class="nowrap">
                <template v-if="e.status === 'firing'">
                  <n-button v-if="!e.acked" size="tiny" quaternary @click="ack(e)">确认</n-button>
                  <n-dropdown :options="silenceOpts" trigger="click" @select="(m) => silence(e, m)"><n-button size="tiny" quaternary>静默</n-button></n-dropdown>
                </template>
                <n-button v-else-if="e.status === 'info' && !e.acked" size="tiny" quaternary @click="ack(e)">确认</n-button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="m-list">
        <div v-for="e in events" :key="e.id" class="m-item">
          <div class="row">
            <span :class="['tag', sevClass[e.severity]]">{{ sevLabel[e.severity] }}</span>
            <b>{{ e.server_name || '面板' }}</b><span class="spacer" />
            <span class="small muted">{{ ago(e.started_at) }}</span>
          </div>
          <div class="small">{{ e.text }}</div>
          <div class="row">
            <span v-if="e.status === 'resolved'" class="tag ok">已恢复 · 持续 {{ lasting(e) }}</span>
            <span v-else-if="e.status === 'info'" class="tag info">{{ e.acked ? '通知 · 已确认' : '通知' }}</span>
            <span v-else-if="e.acked" class="tag info">已确认</span>
            <span class="spacer" />
            <template v-if="e.status === 'firing'">
              <n-button v-if="!e.acked" size="small" @click="ack(e)">确认</n-button>
              <n-button size="small" @click="silence(e, 60)">静默 1 小时</n-button>
            </template>
            <n-button v-else-if="e.status === 'info' && !e.acked" size="small" @click="ack(e)">确认</n-button>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>
