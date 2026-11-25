<script setup>
import { ref, computed, onMounted } from 'vue'
import { NButton, NInput, NInputNumber, NSwitch, NSelect, NCheckboxGroup, NCheckbox, NModal, NUpload } from 'naive-ui'
import { api } from '../../api'
import { ui } from '../../store'
import { run, ask } from '../../helpers'
import { dt, ago, bytes } from '../../format'

const props = defineProps({
  cfg: { type: Object, required: true },
  orig: { type: Object, required: true },
  backupDirtyKeys: { type: Array, required: true },
  saving: { type: Boolean, default: false },
})
const emit = defineEmits(['set-val', 'reset-backup', 'save-backup'])

const bk = ref({ items: [], last: '', last_error: '', encrypted: false })
const tgChannels = ref([])
const backing = ref(false)
const downloading = ref('')
const deletingBackup = ref('')
const restoring = ref(false)
const restore = ref({ show: false, name: '', file: null, password: '' })

async function loadBackups() {
  bk.value = (await api.get('/backups').catch(() => null)) || bk.value
  tgChannels.value = ((await api.get('/channels').catch(() => [])) || []).filter((c) => c.type === 'telegram')
}
const S = (k, label, def, hint) => ({ k, label, type: 'str', def, hint })
const isDirty = (k) => (props.cfg[k] ?? '') !== (props.orig[k] ?? '')
const boolVal = (k, def) => { const v = props.cfg[k]; return v === '' || v == null ? def : v === '1' || v === 'true' }
const numVal = (k) => { const v = props.cfg[k]; return v === '' || v == null ? null : Number(v) }
const placeholder = (f) => (f.def !== '' && f.def != null ? `默认 ${f.def}` : '未设置')

const targets = computed({
  get: () => (props.cfg.backup_target || '').split(',').filter(Boolean),
  set: (v) => emit('set-val', 'backup_target', v.join(',')),
})
const remoteGroups = computed(() => [
  targets.value.includes('s3') && { title: 'S3 / R2', desc: '兼容 S3 的对象存储，例如 Cloudflare R2、AWS S3、MinIO。', fields: [
    S('backup_s3_endpoint', 'Endpoint', '', '例如 https://<账户 ID>.r2.cloudflarestorage.com'),
    S('backup_s3_bucket', 'Bucket', '', ''),
    S('backup_s3_region', 'Region', 'auto', 'R2 用 auto'),
    S('backup_s3_access_key', 'Access Key', '', ''),
    { k: 'backup_s3_secret', label: 'Secret Key', type: 'secret', def: '' },
    S('backup_s3_prefix', '路径前缀', 'vps-panel', '备份放在 Bucket 里的这个目录下'),
  ] },
  targets.value.includes('webdav') && { title: 'WebDAV', desc: '坚果云、Nextcloud、群晖等支持 WebDAV 的网盘。', fields: [
    S('backup_webdav_url', '地址', '', '备份上传到的目录，例如 https://dav.example.com/backup/'),
    S('backup_webdav_user', '用户名', '', ''),
    { k: 'backup_webdav_password', label: '密码', type: 'secret', def: '' },
  ] },
  targets.value.includes('telegram') && { title: 'Telegram', desc: '用告警渠道里配置的 Bot，把备份文件发到对应的 Chat。', fields: [
    { k: 'backup_tg_channel', label: '发送到', type: 'tg', def: '', hint: '在「告警」页添加 Telegram 渠道' },
  ] },
].filter(Boolean))

async function backupNow() {
  if (backing.value) return
  backing.value = true
  try {
    const r = await run(() => api.post('/backups'))
    if (r) { r.warning ? ui.message.warning('本地备份已完成，远程上传失败：' + r.warning) : ui.message.success('备份完成：' + r.name); loadBackups() }
  } finally { backing.value = false }
}
async function downloadBackup(b) {
  if (downloading.value) return
  downloading.value = b.name
  try {
    const blob = await run(() => api.blob(`/backups/${encodeURIComponent(b.name)}`, { confirm: '下载备份需要确认身份' }))
    if (!blob) return
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = b.name
    document.body.appendChild(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } finally { downloading.value = '' }
}
async function delBackup(b) {
  if (deletingBackup.value) return
  deletingBackup.value = b.name
  try {
    if (!(await ask('删除备份', `删除 ${b.name}？只删除面板本地的副本。`, { danger: true }))) return
    if (await run(() => api.del(`/backups/${encodeURIComponent(b.name)}`), '已删除')) loadBackups()
  } finally { deletingBackup.value = '' }
}
function openRestore(b) { restore.value = { show: true, name: b?.name || '', file: null, password: '' } }
async function doRestore() {
  if (restoring.value) return
  const r = restore.value
  if (!r.name && !r.file) { ui.message.warning('选择备份文件'); return }
  restoring.value = true
  try {
    if (!(await ask('恢复备份', '当前数据会被备份文件覆盖（恢复前自动再备份一次），面板随后重启，所有会话需要重新登录。', { danger: true, okText: '恢复' }))) return
    const fd = new FormData()
    if (r.name) fd.append('name', r.name); else fd.append('file', r.file)
    if (r.password) fd.append('password', r.password)
    const res = await run(() => api.post('/backups/restore', fd, { confirm: '恢复备份需要确认身份' }))
    if (res) {
      restore.value.show = false
      ui.message.loading('面板重启中…', { duration: 0 })
      setTimeout(() => location.reload(), 6000)
    }
  } finally { restoring.value = false }
}

onMounted(loadBackups)
defineExpose({ loadBackups })
</script>

<template>
  <div class="sections">
    <section class="s-group">
      <div class="s-head">
        <h2>备份文件</h2>
        <p>备份包含数据库和密钥，保存在面板所在的机器上。</p>
      </div>
      <div class="card s-card">
        <div class="s-card-head">
          <span class="f-hint">{{ bk.items?.length || 0 }} 个备份<template v-if="bk.last"> · 上次备份 {{ ago(bk.last) }}</template></span>
          <span class="spacer" />
          <n-button size="small" @click="openRestore(null)">上传并恢复</n-button>
          <n-button size="small" type="primary" :loading="backing" @click="backupNow">立即备份</n-button>
        </div>
        <div v-if="bk.last_error" class="banner warn">上次远程备份失败：{{ bk.last_error }}</div>
        <div v-if="!bk.encrypted" class="banner info">未设置备份密码，备份里的密钥是明文。建议在下方「自动备份」里设置。</div>
        <div v-if="!bk.items?.length" class="empty">还没有备份</div>
        <div v-for="b in bk.items" :key="b.name" class="b-item">
          <div class="b-main">
            <span class="mono small">{{ b.name }}</span>
            <span class="f-hint">{{ dt(b.at) }} · {{ bytes(b.size) }}{{ b.encrypted ? ' · 已加密' : '' }}</span>
          </div>
          <div class="row b-act">
            <n-button size="tiny" quaternary :loading="downloading === b.name" :disabled="!!downloading" @click="downloadBackup(b)">下载</n-button>
            <n-button size="tiny" quaternary @click="openRestore(b)">恢复</n-button>
            <n-button size="tiny" quaternary type="error" :loading="deletingBackup === b.name" :disabled="!!deletingBackup" @click="delBackup(b)">删除</n-button>
          </div>
        </div>
      </div>
    </section>

    <section class="s-group">
      <div class="s-head">
        <h2>自动备份</h2>
        <p>每天定时备份一次，可以同时上传到远程存储。</p>
      </div>
      <div class="card s-card">
        <div :class="['field', 'f-bool', { dirty: isDirty('backup_enabled') }]">
          <div class="f-label"><span class="f-name">开启自动备份<i v-if="isDirty('backup_enabled')" class="f-dot" /></span></div>
          <div class="f-ctl"><n-switch :value="boolVal('backup_enabled', true)" aria-label="开启自动备份" @update:value="(v) => emit('set-val', 'backup_enabled', v)" /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">备份时间<i v-if="isDirty('backup_time')" class="f-dot" /></span><span class="f-hint">每天这个时间执行，24 小时制</span></div>
          <div class="f-ctl"><n-input :value="cfg.backup_time" placeholder="默认 03:30" clearable @update:value="(v) => emit('set-val', 'backup_time', v)" /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">保留份数<i v-if="isDirty('backup_keep')" class="f-dot" /></span><span class="f-hint">面板本机保留最近的几份，更早的自动删除</span></div>
          <div class="f-ctl">
            <n-input-number :value="numVal('backup_keep')" placeholder="默认 7" :min="1" :show-button="false" clearable @update:value="(v) => emit('set-val', 'backup_keep', v)">
              <template #suffix><span class="f-unit">份</span></template>
            </n-input-number>
          </div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">备份密码<i v-if="isDirty('backup_password')" class="f-dot" /></span><span class="f-hint">恢复时需要这个密码，忘了就无法从加密备份恢复</span></div>
          <div class="f-ctl"><n-input :value="cfg.backup_password" type="password" show-password-on="click" placeholder="留空 = 不加密" :input-props="{ autocomplete: 'new-password' }" @update:value="(v) => emit('set-val', 'backup_password', v)" /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">同时上传到<i v-if="isDirty('backup_target')" class="f-dot" /></span><span class="f-hint">勾选后在下方填写对应的配置</span></div>
          <div class="f-ctl">
            <n-checkbox-group v-model:value="targets">
              <div class="row"><n-checkbox value="s3">S3 / R2</n-checkbox><n-checkbox value="webdav">WebDAV</n-checkbox><n-checkbox value="telegram">Telegram</n-checkbox></div>
            </n-checkbox-group>
          </div>
        </div>
      </div>
    </section>

    <section v-for="g in remoteGroups" :key="g.title" class="s-group">
      <div class="s-head"><h2>{{ g.title }}</h2><p>{{ g.desc }}</p></div>
      <div class="card s-card">
        <div v-for="f in g.fields" :key="f.k" :class="['field', { dirty: isDirty(f.k) }]">
          <div class="f-label"><span class="f-name">{{ f.label }}<i v-if="isDirty(f.k)" class="f-dot" /></span><span v-if="f.hint" class="f-hint">{{ f.hint }}</span></div>
          <div class="f-ctl">
            <n-select v-if="f.type === 'tg'" :value="cfg[f.k] || ''" :options="[{ label: '第一个 Telegram 渠道', value: '' }, ...tgChannels.map((c) => ({ label: c.name, value: String(c.id) }))]" @update:value="(v) => emit('set-val', f.k, v)" />
            <n-input v-else-if="f.type === 'secret'" :value="cfg[f.k]" type="password" show-password-on="click" :placeholder="placeholder(f)" :input-props="{ autocomplete: 'off' }" @update:value="(v) => emit('set-val', f.k, v)" />
            <n-input v-else :value="cfg[f.k]" :placeholder="placeholder(f)" clearable @update:value="(v) => emit('set-val', f.k, v)" />
          </div>
        </div>
      </div>
    </section>
  </div>
  <div class="savebar">
    <span v-if="backupDirtyKeys.length" class="small dirty-note"><i class="f-dot" />{{ backupDirtyKeys.length }} 项未保存</span>
    <span v-else class="small muted">留空的项使用默认值</span>
    <span class="spacer" />
    <n-button :disabled="!backupDirtyKeys.length" @click="emit('reset-backup')">撤销</n-button>
    <n-button type="primary" :loading="saving" :disabled="!backupDirtyKeys.length || saving" @click="emit('save-backup', loadBackups)">保存</n-button>
  </div>

  <n-modal v-model:show="restore.show" preset="card" title="恢复备份" style="width: min(460px, 96vw)">
    <div class="m-form">
      <div v-if="restore.name" class="field f-inline">
        <div class="f-label"><span class="f-name">备份</span></div>
        <div class="f-ctl"><span class="mono small b-name">{{ restore.name }}</span></div>
      </div>
      <div v-else class="field f-inline">
        <div class="f-label"><span class="f-name">备份文件</span><span class="f-hint">.vpb 或 .gz</span></div>
        <div class="f-ctl"><n-upload :max="1" :default-upload="false" accept=".vpb,.gz" @change="({ fileList }) => (restore.file = fileList[0]?.file || null)">
          <n-button>选择备份文件</n-button>
        </n-upload></div>
      </div>
      <div class="field">
        <div class="f-label"><span class="f-name">备份密码</span><span class="f-hint">加密备份需要，未加密的留空</span></div>
        <div class="f-ctl"><n-input v-model:value="restore.password" type="password" show-password-on="click" placeholder="未设置" :input-props="{ autocomplete: 'off' }" /></div>
      </div>
      <div class="banner warn">恢复会覆盖数据库与密钥，面板随后自动重启。</div>
    </div>
    <template #footer><div class="m-foot"><n-button @click="restore.show = false">取消</n-button><n-button type="error" :loading="restoring" :disabled="restoring" @click="doRestore">恢复</n-button></div></template>
  </n-modal>
</template>

<style scoped>
.b-item { display: flex; align-items: center; gap: 10px; padding: 10px 0; border-top: 1px solid var(--c-border); flex-wrap: wrap; }
.b-main { display: flex; flex-direction: column; gap: 2px; flex: 1; min-width: 200px; }
.b-act { gap: 4px; }
.b-name { overflow-wrap: anywhere; text-align: right; }
</style>
