<script setup>
// Picks which zstaticcdn.com nodes (and custom targets) the agents ping.
import { ref, computed, watch } from 'vue'
import { NDrawer, NDrawerContent, NButton, NInput, NSelect, NCheckbox, NInputNumber, NTabs, NTabPane } from 'naive-ui'
import { api } from '../api'
import { ui } from '../store'
import { run, ask } from '../helpers'
import { ago, carrierLabel } from '../format'
import { drawerWidth } from '../mobile'

const props = defineProps({ show: Boolean })
const emit = defineEmits(['update:show', 'changed'])
const items = ref([])
const syncedAt = ref('')
const picked = ref(new Set())
const q = ref('')
const carrier = ref('')
const busy = ref(false)
const custom = ref({ name: '', carrier: 'telecom', host: '', port: 80, type: 'tcp' })

async function load() {
  const r = await run(() => api.get('/probes/targets'))
  if (!r) return
  items.value = r.items || []
  syncedAt.value = r.synced_at
  picked.value = new Set(items.value.filter((t) => t.enabled).map((t) => t.id))
}
watch(() => props.show, (v) => v && load())

const zs = computed(() => items.value.filter((t) => t.source === 'zstatic'))
const cs = computed(() => items.value.filter((t) => t.source === 'custom'))
const provinces = computed(() => {
  const m = new Map()
  for (const t of zs.value) {
    if (carrier.value && t.carrier !== carrier.value) continue
    if (q.value && !(t.province + t.city + t.name + t.host).includes(q.value)) continue
    const k = t.province || '其他'
    if (!m.has(k)) m.set(k, [])
    m.get(k).push(t)
  }
  return [...m.entries()]
})
const carriers = [{ label: '全部运营商', value: '' }, { label: '电信', value: 'telecom' }, { label: '联通', value: 'unicom' }, { label: '移动', value: 'mobile' }]
function toggle(id, v) {
  const s = new Set(picked.value)
  v ? s.add(id) : s.delete(id)
  picked.value = s
}
function pickPreset() {
  // One node per carrier in Beijing, Shanghai and Guangzhou: a sane default of 9.
  const want = ['北京', '上海', '广东']
  const s = new Set([...picked.value].filter((id) => !zs.value.find((t) => t.id === id)))
  for (const p of want) for (const c of ['telecom', 'unicom', 'mobile']) {
    const t = zs.value.find((x) => x.province.startsWith(p) && x.carrier === c)
    if (t) s.add(t.id)
  }
  picked.value = s
}
async function save() {
  if (picked.value.size > 60) { ui.message.warning('最多同时启用 60 个目标'); return }
  busy.value = true
  const r = await run(() => api.post('/probes/targets/bulk', { enabled: [...picked.value] }), `已启用 ${picked.value.size} 个目标，Agent 将在下一轮使用`)
  busy.value = false
  if (r) { emit('changed'); emit('update:show', false) }
}
async function sync() {
  busy.value = true
  const r = await run(() => api.post('/probes/sync'))
  busy.value = false
  if (r) { ui.message.success(`已同步 ${r.count} 个节点`); load() }
}
async function addCustom() {
  const r = await run(() => api.post('/probes/targets', custom.value), '已添加')
  if (r) { custom.value = { name: '', carrier: 'telecom', host: '', port: 80, type: 'tcp' }; load(); emit('changed') }
}
async function delCustom(t) {
  if (!(await ask('删除目标', `删除「${t.name}」？历史数据保留。`))) return
  if (await run(() => api.del(`/probes/targets/${t.id}`), '已删除')) { load(); emit('changed') }
}
const types = [{ label: 'TCP', value: 'tcp' }, { label: 'ICMP', value: 'icmp' }]
</script>

<template>
  <n-drawer :show="show" :width="drawerWidth(620)" @update:show="(v) => emit('update:show', v)">
    <n-drawer-content title="管理拨测目标" closable :native-scrollbar="false">
      <n-tabs type="line" animated>
        <n-tab-pane name="zs" tab="zstaticcdn 节点">
          <p class="small muted" style="margin-top: 0">
            节点列表来自 zstaticcdn.com，覆盖各省三网。{{ syncedAt ? '上次同步 ' + ago(syncedAt) : '尚未同步' }}
            <button class="link-btn" :disabled="busy" @click="sync">立即同步</button>
          </p>
          <div class="row" style="margin-bottom: 10px">
            <n-input v-model:value="q" placeholder="搜索省份 / 城市" clearable size="small" style="flex: 1; min-width: 140px" />
            <n-select v-model:value="carrier" :options="carriers" size="small" style="width: 120px" />
            <n-button size="small" @click="pickPreset">推荐 9 个</n-button>
          </div>
          <div v-if="!zs.length" class="empty">还没有节点，点「立即同步」拉取。</div>
          <div v-for="[prov, list] in provinces" :key="prov" class="prov">
            <div class="prov-h">{{ prov }}</div>
            <div class="prov-list">
              <n-checkbox v-for="t in list" :key="t.id" :checked="picked.has(t.id)" @update:checked="(v) => toggle(t.id, v)">
                {{ carrierLabel[t.carrier] || t.carrier }}<span class="muted small"> {{ t.city !== prov ? t.city : '' }}</span>
              </n-checkbox>
            </div>
          </div>
        </n-tab-pane>
        <n-tab-pane name="custom" tab="自定义目标">
          <div class="custom-form">
            <n-input v-model:value="custom.name" placeholder="名称，如 家宽" size="small" />
            <n-select v-model:value="custom.carrier" :options="carriers.slice(1)" size="small" />
            <n-input v-model:value="custom.host" placeholder="IP 或域名" size="small" />
            <n-select v-model:value="custom.type" :options="types" size="small" />
            <n-input-number v-if="custom.type === 'tcp'" v-model:value="custom.port" :min="1" :max="65535" :show-button="false" size="small" placeholder="端口" />
            <n-button size="small" type="primary" @click="addCustom">添加</n-button>
          </div>
          <div v-if="!cs.length" class="empty">没有自定义目标</div>
          <table v-else class="plain">
            <thead><tr><th /><th>名称</th><th>地址</th><th /></tr></thead>
            <tbody>
              <tr v-for="t in cs" :key="t.id">
                <td><n-checkbox :checked="picked.has(t.id)" @update:checked="(v) => toggle(t.id, v)" /></td>
                <td>{{ t.name }} <span class="muted small">{{ carrierLabel[t.carrier] }}</span></td>
                <td class="mono small">{{ t.type }} {{ t.host }}{{ t.type === 'tcp' ? ':' + t.port : '' }}</td>
                <td><n-button size="tiny" quaternary type="error" @click="delCustom(t)">删除</n-button></td>
              </tr>
            </tbody>
          </table>
        </n-tab-pane>
      </n-tabs>
      <template #footer>
        <div class="row" style="width: 100%">
          <span class="small" :class="picked.size > 60 ? 'bad-text' : 'muted'">已选 {{ picked.size }} / 60</span>
          <span class="spacer" />
          <n-button @click="emit('update:show', false)">取消</n-button>
          <n-button type="primary" :loading="busy" @click="save">保存</n-button>
        </div>
      </template>
    </n-drawer-content>
  </n-drawer>
</template>

<style scoped>
.prov { border-bottom: 1px solid var(--c-border); padding: 8px 0; }
.prov-h { font-size: 13px; font-weight: 600; margin-bottom: 6px; }
.prov-list { display: flex; flex-wrap: wrap; gap: 6px 18px; }
.custom-form { display: grid; grid-template-columns: 1fr 100px 1fr 80px 80px auto; gap: 8px; margin-bottom: 14px; }
.bad-text { color: var(--c-danger); }
@media (max-width: 767px) { .custom-form { grid-template-columns: 1fr 1fr; } }
</style>
