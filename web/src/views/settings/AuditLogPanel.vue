<script setup>
import { ref, watch, onMounted, onBeforeUnmount } from 'vue'
import { NButton, NSelect } from 'naive-ui'
import { api } from '../../api'
import { dt } from '../../format'

const audit = ref({ type: 'all', items: [], offset: 0, more: false })
const auditLoading = ref(false)
let auditAbort = null

const auditTypes = [
  { label: '全部', value: 'all' }, { label: '登录', value: 'login' }, { label: '服务器', value: 'server' },
  { label: '线路', value: 'route' }, { label: '订阅', value: 'sub' }, { label: '告警', value: 'alert' }, { label: '系统', value: 'system' },
]
const typeLabel = Object.fromEntries(auditTypes.map((t) => [t.value, t.label]))

async function loadAudit(reset) {
  if (!reset && auditLoading.value) return
  auditAbort?.abort()
  const ac = new AbortController()
  auditAbort = ac
  if (reset) audit.value.offset = 0
  const a = audit.value
  auditLoading.value = true
  try {
    const rows = await api.get(`/audit?type=${a.type}&limit=100&offset=${a.offset}`, { signal: ac.signal }).catch(() => null)
    if (ac.signal.aborted || !rows) return
    audit.value.items = reset ? rows : [...a.items, ...rows]
    audit.value.more = rows.length === 100
    audit.value.offset += rows.length
  } finally {
    if (auditAbort === ac) auditLoading.value = false
  }
}

watch(() => audit.value.type, () => loadAudit(true))
onMounted(() => loadAudit(true))
onBeforeUnmount(() => auditAbort?.abort())
</script>

<template>
  <div class="sec-head">
    <div class="s-head">
      <h2>审计日志</h2>
      <p>登录和面板上的修改操作都记录在这里。</p>
    </div>
    <span class="spacer" />
    <n-select v-model:value="audit.type" :options="auditTypes" aria-label="类别" style="width: 120px" />
    <n-button tag="a" :href="`/api/audit?format=csv&type=${audit.type}`" download>导出 CSV</n-button>
  </div>
  <div class="card s-card">
    <div v-if="!audit.items.length" class="empty">暂无记录</div>
    <template v-else>
      <div class="table-wrap hide-mobile">
        <table class="plain audit">
          <thead><tr><th>时间</th><th>类别</th><th>操作</th><th>对象</th><th>详情</th><th>IP</th></tr></thead>
          <tbody>
            <tr v-for="l in audit.items" :key="l.id">
              <td>{{ dt(l.ts) }}</td><td>{{ typeLabel[l.type] || l.type }}</td><td class="mono small">{{ l.action }}</td>
              <td class="small">{{ l.target }}</td><td class="small muted detail" :title="l.detail">{{ l.detail }}</td><td class="mono small">{{ l.ip }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="m-list only-mobile">
        <div v-for="l in audit.items" :key="l.id" class="m-item">
          <div class="m-row"><span class="mono">{{ l.action }}</span><span class="tag">{{ typeLabel[l.type] || l.type }}</span></div>
          <div v-if="l.target || l.detail" class="audit-text small">{{ [l.target, l.detail].filter(Boolean).join(' · ') }}</div>
          <div class="m-row small"><span>{{ dt(l.ts) }}</span><span class="mono muted">{{ l.ip }}</span></div>
        </div>
      </div>
    </template>
    <div v-if="audit.more" class="f-actions more"><n-button :loading="auditLoading" :disabled="auditLoading" @click="loadAudit(false)">加载更多</n-button></div>
  </div>
</template>

<style scoped>
.table-wrap td.detail { white-space: normal; min-width: 260px; overflow-wrap: anywhere; }
.audit-text { color: var(--c-text-2); line-height: 1.55; overflow-wrap: anywhere; }
.more { justify-content: center; }
</style>
