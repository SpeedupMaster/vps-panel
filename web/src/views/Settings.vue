<script setup>
import { ref, computed, onMounted } from 'vue'
import { NTabs, NTabPane } from 'naive-ui'
import { api } from '../api'
import { ui } from '../store'
import { run, computeDirtyKeys } from '../helpers'
import { useQueryTab } from '../composables/useQueryTab'
import { useDirtyForm } from '../composables/useDirtyForm'
import AccountSecurityPanel from './settings/AccountSecurityPanel.vue'
import SystemParamsPanel from './settings/SystemParamsPanel.vue'
import BackupsPanel from './settings/BackupsPanel.vue'
import AuditLogPanel from './settings/AuditLogPanel.vue'

const settingsTabs = [
  { name: 'panel', label: '面板' }, { name: 'agent', label: 'Agent 与探测' },
  { name: 'xray', label: 'Xray' }, { name: 'retention', label: '数据保留' },
]
const tab = useQueryTab(['account', 'panel', 'agent', 'xray', 'retention', 'backup', 'audit'], 'account')

const cfg = ref({})
const orig = ref({})
const saving = ref(false)
const backupKeys = [
  'backup_enabled', 'backup_time', 'backup_keep', 'backup_target', 'backup_password',
  'backup_s3_endpoint', 'backup_s3_bucket', 'backup_s3_region', 'backup_s3_access_key', 'backup_s3_secret',
  'backup_s3_prefix', 'backup_webdav_url', 'backup_webdav_user', 'backup_webdav_password', 'backup_tg_channel',
]
const dirtyKeys = computed(() => computeDirtyKeys(cfg.value, orig.value).filter((k) => !backupKeys.includes(k)))
const backupDirtyKeys = computed(() => computeDirtyKeys(cfg.value, orig.value, backupKeys))
useDirtyForm(() => dirtyKeys.value.length > 0 || backupDirtyKeys.value.length > 0)

async function loadSettings() {
  const s = (await api.get('/settings').catch(() => ({}))) || {}
  cfg.value = { ...s }
  orig.value = { ...s }
}
function setVal(k, v) {
  cfg.value = { ...cfg.value, [k]: v == null ? '' : typeof v === 'boolean' ? (v ? '1' : '0') : String(v) }
}
function resetKeys(keys) {
  const c = { ...cfg.value }
  for (const k of keys) c[k] = orig.value[k]
  cfg.value = c
}
async function saveSettings(keys, after) {
  if (saving.value) return
  const body = {}
  for (const k of keys || dirtyKeys.value) body[k] = cfg.value[k]
  if (!Object.keys(body).length) { ui.message.info('没有修改'); return }
  if (body.fx_rates) { try { JSON.parse(body.fx_rates) } catch { ui.message.error('汇率不是合法的 JSON'); return } }
  const adminPathChanged = Object.prototype.hasOwnProperty.call(body, 'admin_path') &&
    String(body.admin_path || '').trim().replace(/^\/+|\/+$/g, '') !== String(orig.value.admin_path || '').trim().replace(/^\/+|\/+$/g, '')
  saving.value = true
  try {
    if (await run(() => api.put('/settings', body), adminPathChanged ? '后台路径已更新，正在跳转…' : '已保存')) {
      if (adminPathChanged) {
        orig.value = { ...cfg.value }
        const seg = String(body.admin_path || '').trim().replace(/^\/+|\/+$/g, '')
        const target = seg ? `/${seg}/settings?tab=panel` : '/settings?tab=panel'
        try { await window.caches?.delete('pages') } catch { /* ignore */ }
        window.location.assign(target)
        return
      }
      await loadSettings()
      after?.()
    }
  } finally { saving.value = false }
}

onMounted(loadSettings)
</script>

<template>
  <div class="page">
    <div class="page-head"><div class="ph"><h1>设置</h1><p class="page-desc">账号安全、面板、Agent、Xray、数据保留与备份的全局设置。</p></div></div>
    <n-tabs v-model:value="tab" type="line" animated>
      <n-tab-pane name="account" tab="账号与安全">
        <account-security-panel />
      </n-tab-pane>
      <n-tab-pane v-for="t in settingsTabs" :key="t.name" :name="t.name" :tab="t.label">
        <system-params-panel :section="t.name" :cfg="cfg" :orig="orig" :dirty-keys="dirtyKeys" :saving="saving"
          @set-val="setVal" @reset="cfg = { ...orig }" @save="saveSettings()" />
      </n-tab-pane>
      <n-tab-pane name="backup" tab="备份">
        <backups-panel :cfg="cfg" :orig="orig" :backup-dirty-keys="backupDirtyKeys" :saving="saving"
          @set-val="setVal" @reset-backup="resetKeys(backupKeys)" @save-backup="(after) => saveSettings(backupDirtyKeys, after)" />
      </n-tab-pane>
      <n-tab-pane name="audit" tab="审计日志">
        <audit-log-panel />
      </n-tab-pane>
    </n-tabs>
  </div>
</template>

<style scoped>
p { line-height: 1.6; }
</style>
