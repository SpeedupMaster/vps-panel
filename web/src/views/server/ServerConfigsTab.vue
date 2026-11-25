<script setup>
import { ref, onBeforeUnmount } from 'vue'
import { NButton } from 'naive-ui'
import { api } from '../../api'
import { run, ask } from '../../helpers'
import { dt } from '../../format'

const props = defineProps({
  sv: { type: Object, required: true },
  configs: { type: Array, required: true },
})
const emit = defineEmits(['view-config', 'task', 'reload'])

const rollingBack = ref(0)
let rollbackTimer = null
onBeforeUnmount(() => clearTimeout(rollbackTimer))

async function viewConfig(v) {
  const r = await run(() => api.get(`/servers/${props.sv.id}/configs/${v.id}`))
  if (r) emit('view-config', { title: `配置 v${v.version}`, body: r.config, diff: false })
}
async function viewDiff(v) {
  const r = await run(() => api.get(`/servers/${props.sv.id}/configs/${v.id}/diff`))
  if (r) emit('view-config', { title: r.from ? `v${r.from} → v${r.to}` : `v${r.to}（首个版本）`, body: r.diff, diff: true })
}
async function rollback(v) {
  if (rollingBack.value) return
  rollingBack.value = v.id
  try {
    if (!(await ask(`回滚到 v${v.version}`, '会把这个版本重新下发到服务器。若新配置启动失败，Agent 会自动恢复上一份。'))) return
    const r = await run(() => api.post(`/servers/${props.sv.id}/configs/${v.id}/rollback`, {}, { confirm: '回滚配置需要确认身份' }))
    if (r) {
      emit('task', { id: r.task_id, title: `回滚到 v${v.version}` })
      clearTimeout(rollbackTimer)
      rollbackTimer = setTimeout(() => emit('reload'), 3000)
    }
  } finally { rollingBack.value = 0 }
}
</script>

<template>
  <div class="card">
    <div v-if="!configs.length" class="empty">还没有下发过配置</div>
    <div v-else class="table-wrap">
      <table class="plain">
        <thead><tr><th>版本</th><th>时间</th><th>原因</th><th>状态</th><th /></tr></thead>
        <tbody>
          <tr v-for="v in configs" :key="v.id">
            <td class="mono">v{{ v.version }}</td>
            <td class="nowrap">{{ dt(v.created_at) }}</td>
            <td>{{ v.reason || '—' }}</td>
            <td><span v-if="v.version === sv.xray_config_ver" class="tag ok">当前</span><span v-else-if="v.applied" class="tag">已应用</span><span v-else class="tag warn">未应用</span></td>
            <td class="nowrap" style="text-align: right">
              <n-button size="tiny" quaternary @click="viewConfig(v)">查看</n-button>
              <n-button size="tiny" quaternary @click="viewDiff(v)">差异</n-button>
              <n-button v-if="v.version !== sv.xray_config_ver" size="tiny" quaternary type="warning" :loading="rollingBack === v.id" :disabled="!!rollingBack" @click="rollback(v)">回滚到此</n-button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
