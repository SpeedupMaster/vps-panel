<script setup>
import { ref, computed, onMounted, watch, onBeforeUnmount } from 'vue'
import { NButton, NSelect, NRadioGroup, NRadio, NCheckbox, NSwitch, NTabs, NTabPane, NInput, NUpload, NModal } from 'naive-ui'
import { api } from '../api'
import { ui, on } from '../store'
import { run, ask } from '../helpers'
import { ago, dt, date } from '../format'

const comp = ref('xray')
const data = ref({ items: [], servers: [] })
const pre = ref(false)
const version = ref('')
const canary = ref(true)
const picked = ref(new Set())
const history = ref([])
const batch = ref('')
const batchTasks = ref([])
const panel = ref(null)
const busy = ref(false)

async function load() {
  const c = comp.value === 'geo' ? 'xray' : comp.value
  const r = await run(() => api.get(`/releases/${c}`))
  if (r) data.value = { ...r, items: r.items || [], servers: r.servers || [] }
  version.value = comp.value === 'xray' ? (r?.latest_xray || '') : comp.value === 'agent' ? (r?.agent_published || '') : ''
  picked.value = new Set()
}
async function loadHistory() { history.value = (await run(() => api.get('/updates/history'))) || [] }
async function loadPanel() { panel.value = await api.get('/system/version').catch((e) => ({ error: e.message })) }
onMounted(() => { load(); loadHistory(); loadPanel() })
watch(comp, load)
const off = on('task', (d) => { if (batch.value) loadBatch(); if (['update_agent', 'update_xray', 'update_geo'].includes(d.type)) loadHistory() })
onBeforeUnmount(off)

const comps = [{ label: 'Xray', value: 'xray' }, { label: 'Agent', value: 'agent' }, { label: '规则文件（geoip / geosite）', value: 'geo' }]
const candidates = computed(() => {
  const s = data.value.servers || []
  return comp.value === 'agent' ? s : s.filter((x) => x.mode === 'node')
})
const current = (s) => (comp.value === 'agent' ? s.agent_version : comp.value === 'xray' ? s.xray_version : '—')
const norm = (v) => String(v || '').replace(/^v/, '')
const outdated = (s) => comp.value !== 'geo' && version.value && norm(current(s)) !== norm(version.value)
const versions = computed(() => {
  const items = (data.value.items || []).filter((r) => pre.value || r.channel !== 'pre')
  return items.map((r) => ({ label: r.version + (r.channel === 'pre' ? '（预发布）' : '') + (r.published ? '（已发布）' : ''), value: r.version }))
})
function toggle(id, v) { const s = new Set(picked.value); v ? s.add(id) : s.delete(id); picked.value = s }
function pickOutdated() { picked.value = new Set(candidates.value.filter(outdated).map((s) => s.id)) }
async function start() {
  if (!picked.value.size) return
  if (comp.value !== 'geo' && !version.value) { ui.message.warning('请选择版本'); return }
  const name = comps.find((c) => c.value === comp.value).label
  if (!(await ask('批量更新', `${name}${version.value ? ' → ' + version.value : ''}，共 ${picked.value.size} 台。${canary.value ? '先更新 1 台并确认正常，再更新其余。' : '全部同时更新。'}`))) return
  busy.value = true
  const r = await run(() => api.post('/updates', { component: comp.value, version: comp.value === 'geo' ? '' : version.value, server_ids: [...picked.value], canary: canary.value }), '已开始更新')
  busy.value = false
  if (r) { batch.value = r.batch; loadBatch() }
}
async function loadBatch() {
  if (!batch.value) return
  batchTasks.value = (await api.get(`/tasks?batch=${batch.value}`).catch(() => [])) || []
  if (batchTasks.value.length && batchTasks.value.every((t) => t.finished_at)) { load(); loadHistory() }
}
const serverName = (id) => (data.value.servers || []).find((s) => s.id === id)?.name || '#' + id
const stLabel = { pending: '排队中', sent: '已下发', running: '进行中', success: '成功', failed: '失败', timeout: '超时' }
const stClass = { success: 'ok', failed: 'bad', timeout: 'bad', running: 'warn', sent: 'warn' }
async function sync() {
  const r = await run(() => api.post('/releases/xray/sync'))
  if (r) { ui.message.success('最新版本 ' + (r.latest || '—')); load() }
}
const notes = ref({ show: false, title: '', body: '' })
function showNotes(r) { notes.value = { show: true, title: `${r.component === 'agent' ? 'Agent' : 'Xray'} ${r.version}`, body: r.notes || '没有更新说明' } }

// ---- agent release upload ----
const up = ref({ show: false, version: '', notes: '', files: {}, sigs: {}, publish: true })
function pickFile(kind, arch, { file }) { up.value[kind] = { ...up.value[kind], [arch]: file.file }; return false }
async function upload() {
  const u = up.value
  if (!u.version || !Object.keys(u.files).length) { ui.message.warning('填写版本并选择至少一个文件'); return }
  const fd = new FormData()
  fd.append('version', u.version); fd.append('notes', u.notes)
  if (u.publish) fd.append('publish', '1')
  for (const [a, f] of Object.entries(u.files)) fd.append('file_' + a, f)
  for (const [a, f] of Object.entries(u.sigs)) fd.append('sig_' + a, f)
  busy.value = true
  const r = await run(() => api.post('/releases/agent', fd), '已上传')
  busy.value = false
  if (r) { up.value = { show: false, version: '', notes: '', files: {}, sigs: {}, publish: true }; load() }
}
async function publish(r) { if (await run(() => api.post(`/releases/agent/${r.version}/publish`), `已发布 ${r.version}`)) load() }
async function delRelease(r) {
  if (!(await ask('删除版本', `删除 Agent ${r.version}？`, { danger: true }))) return
  if (await run(() => api.del(`/releases/agent/${r.version}`), '已删除')) load()
}
const archs = (r) => { try { return Object.keys(JSON.parse(r.assets || '{}')).join(' / ') } catch { return '' } }

// ---- panel self update ----
async function selfUpdate() {
  if (!(await ask('更新面板', `更新到 ${panel.value.latest}？下载后会校验 sha256${data.value.signing ? '与签名' : ''}，旧版本自动备份，然后重启面板（约几秒不可用）。`))) return
  const r = await run(() => api.post('/system/update', {}, { confirm: '更新面板需要确认身份' }))
  if (r) { ui.message.success(`正在更新到 ${r.version}，面板重启后请刷新页面`); setTimeout(() => location.reload(), 8000) }
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div class="ph"><h1>更新中心</h1><p class="page-desc">Agent、Xray 和规则文件的版本与批量更新。上次检查：{{ data.synced_at ? ago(data.synced_at) : '从未' }}</p></div>
      <span class="spacer" />
      <n-button @click="sync">立即检查</n-button>
    </div>

    <div v-if="panel && (panel.available || panel.error || panel.repo)" :class="['banner', panel.available ? 'warn' : 'info']">
      <span>
        面板 {{ panel.current }}
        <template v-if="panel.available">· 有新版本 <b>{{ panel.latest }}</b></template>
        <template v-else-if="panel.error">· 检查失败：{{ panel.error }}</template>
        <template v-else>· 已是最新</template>
        <span class="muted small" v-if="panel.repo">（{{ panel.repo }}）</span>
      </span>
      <span class="spacer" />
      <n-button v-if="panel.available" size="small" type="primary" @click="selfUpdate">更新面板</n-button>
    </div>

    <section class="l-sec">
    <div class="sec-head"><div class="s-head"><h2>批量更新</h2><p>选择组件和目标版本，再勾选要更新的服务器。</p></div></div>
    <div class="card s-card">
      <div class="field">
        <div class="f-label"><span class="f-name">组件</span></div>
        <div class="f-ctl"><n-select v-model:value="comp" :options="comps" /></div>
      </div>
      <div v-if="comp !== 'geo'" class="field">
        <div class="f-label"><span class="f-name">目标版本</span><span class="f-hint">可以直接输入列表里没有的版本号</span></div>
        <div class="f-ctl"><n-select v-model:value="version" :options="versions" placeholder="选择版本" filterable tag /></div>
      </div>
      <div v-if="comp === 'xray'" class="field f-bool">
        <div class="f-label"><span class="f-name">显示预发布版</span></div>
        <div class="f-ctl"><n-switch v-model:value="pre" aria-label="显示预发布版" /></div>
      </div>
      <div class="field">
        <div class="f-label"><span class="f-name">更新策略</span><span class="f-hint">先更新 1 台时，这台失败就停止，其余不更新</span></div>
        <div class="f-ctl">
          <n-radio-group v-model:value="canary" class="strategy">
            <n-radio :value="true">先更新 1 台，正常后再更新其余</n-radio>
            <n-radio :value="false">全部同时更新</n-radio>
          </n-radio-group>
        </div>
      </div>
      <div class="table-wrap pick-table">
        <table class="plain">
          <thead><tr>
            <th style="width: 36px"><n-checkbox :checked="picked.size > 0 && picked.size === candidates.length" :indeterminate="picked.size > 0 && picked.size < candidates.length"
              aria-label="全选" @update:checked="(v) => picked = new Set(v ? candidates.map((s) => s.id) : [])" /></th>
            <th>服务器</th><th>角色</th><th>当前版本</th><th>更新后</th><th>说明</th>
          </tr></thead>
          <tbody>
            <tr v-for="s in candidates" :key="s.id">
              <td><n-checkbox :checked="picked.has(s.id)" :aria-label="'选择 ' + s.name" @update:checked="(v) => toggle(s.id, v)" /></td>
              <td>{{ s.name }}</td>
              <td class="small">{{ s.mode === 'node' ? '节点' : '探针' }}</td>
              <td class="mono">{{ current(s) || '—' }}</td>
              <td class="mono">{{ comp === 'geo' ? '最新' : picked.has(s.id) ? version : '' }}</td>
              <td class="small">
                <span v-if="comp === 'xray' && s.xray_locked_version" class="tag">锁定 {{ s.xray_locked_version }}（批量更新会跳过）</span>
                <span v-else-if="outdated(s)" class="tag warn">可更新</span>
                <span v-else-if="comp !== 'geo'" class="muted">已是该版本</span>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="!candidates.length" class="empty">没有可以更新的服务器</div>
      </div>
      <div class="f-actions">
        <button class="link-btn small" @click="pickOutdated">选择所有可更新</button>
        <span class="spacer" />
        <span class="small muted">已选 {{ picked.size }} 台</span>
        <n-button type="primary" :disabled="!picked.size" :loading="busy" @click="start">开始更新</n-button>
      </div>
      <div v-if="batch" class="batch">
        <div class="small muted">批次 {{ batch }}</div>
        <div v-if="!batchTasks.length" class="small muted">准备中…</div>
        <div v-for="t in batchTasks" :key="t.id" class="row small">
          <span :class="['tag', stClass[t.status]]">{{ stLabel[t.status] || t.status }}</span>
          <span>{{ serverName(t.server_id) }}</span>
          <span class="muted mono out">{{ (t.output || '').trim().split('\n').pop() }}</span>
        </div>
      </div>
    </div>
    </section>

    <n-tabs type="line" animated>
      <n-tab-pane name="xray" tab="Xray 版本">
        <div class="sec-head"><div class="s-head"><h2>Xray 版本</h2><p>版本与更新日志从 GitHub Release 同步。{{ data.latest_xray ? '最新稳定版 ' + data.latest_xray + '。' : '' }}</p></div></div>
        <div class="card s-card">
          <div v-if="comp === 'agent'" class="empty">切换到上方的 Xray 组件查看版本列表</div>
          <div v-else class="table-wrap">
          <table class="plain">
            <thead><tr><th>版本</th><th>通道</th><th>发布时间</th><th /></tr></thead>
            <tbody>
              <tr v-for="r in data.items.filter((x) => pre || x.channel !== 'pre').slice(0, 15)" :key="r.id">
                <td class="mono">{{ r.version }}</td>
                <td><span :class="['tag', r.channel === 'pre' ? 'warn' : 'ok']">{{ r.channel === 'pre' ? '预发布' : '稳定' }}</span></td>
                <td>{{ date(r.published_at) }}</td>
                <td style="text-align: right"><n-button size="tiny" quaternary @click="showNotes(r)">更新日志</n-button></td>
              </tr>
            </tbody>
          </table>
          </div>
        </div>
      </n-tab-pane>
      <n-tab-pane name="agent" tab="Agent 版本">
        <div class="sec-head">
          <div class="s-head"><h2>Agent 版本</h2><p>当前发布 {{ data.agent_published || '—' }}。{{ data.signing ? '已配置签名公钥，上传需附带签名。' : '未配置签名公钥（设置 → 更新）。' }}</p></div>
          <span class="spacer" />
          <n-button @click="up.show = true">上传新版本</n-button>
        </div>
        <div class="card s-card">
          <div v-if="comp !== 'agent'" class="empty">切换到上方的 Agent 组件查看版本列表</div>
          <div v-else class="table-wrap">
          <table class="plain">
            <thead><tr><th>版本</th><th>架构</th><th>上传时间</th><th>状态</th><th /></tr></thead>
            <tbody>
              <tr v-for="r in data.items" :key="r.id">
                <td class="mono">{{ r.version }}</td><td class="small">{{ archs(r) }}</td><td>{{ dt(r.published_at) }}</td>
                <td><span v-if="r.published" class="tag ok">当前发布</span></td>
                <td style="text-align: right" class="nowrap">
                  <n-button size="tiny" quaternary @click="showNotes(r)">说明</n-button>
                  <n-button v-if="!r.published" size="tiny" quaternary @click="publish(r)">发布</n-button>
                  <n-button v-if="!r.published" size="tiny" quaternary type="error" @click="delRelease(r)">删除</n-button>
                </td>
              </tr>
            </tbody>
          </table>
          </div>
        </div>
      </n-tab-pane>
      <n-tab-pane name="history" tab="更新记录">
        <div class="sec-head"><div class="s-head"><h2>更新记录</h2><p>Agent、Xray 和规则文件的更新任务及最后一行输出。</p></div></div>
        <div class="card s-card">
          <div v-if="!history.length" class="empty">暂无记录</div>
          <div v-else class="table-wrap">
            <table class="plain">
              <thead><tr><th>时间</th><th>服务器</th><th>类型</th><th>状态</th><th>输出</th></tr></thead>
              <tbody>
                <tr v-for="t in history" :key="t.id">
                  <td class="nowrap">{{ dt(t.created_at) }}</td><td>{{ t.server_name }}</td>
                  <td class="small">{{ { update_agent: 'Agent', update_xray: 'Xray', update_geo: '规则文件' }[t.type] || t.type }}</td>
                  <td><span :class="['tag', stClass[t.status]]">{{ stLabel[t.status] || t.status }}</span></td>
                  <td class="mono small out">{{ t.output }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </n-tab-pane>
    </n-tabs>

    <n-modal v-model:show="notes.show" preset="card" :title="notes.title" style="width: min(720px, 96vw)">
      <pre class="pre light" style="max-height: 65vh">{{ notes.body }}</pre>
    </n-modal>

    <n-modal v-model:show="up.show" preset="card" title="上传 Agent 版本" style="width: min(560px, 96vw)">
      <div class="m-form">
        <div class="field">
          <div class="f-label"><span class="f-name">版本号</span></div>
          <div class="f-ctl"><n-input v-model:value="up.version" placeholder="例如 v0.3.3" /></div>
        </div>
        <div class="field stacked">
          <div class="f-label"><span class="f-name">更新说明</span></div>
          <div class="f-ctl"><n-input v-model:value="up.notes" type="textarea" :rows="3" placeholder="可选" aria-label="更新说明" /></div>
        </div>
        <div v-for="a in ['amd64', 'arm64']" :key="a" class="field f-inline">
          <div class="f-label"><span class="f-name">{{ a }} 二进制</span><span v-if="data.signing" class="f-hint">同时选择对应的签名文件（.sig）</span></div>
          <div class="f-ctl ctl-row">
            <n-upload :default-upload="false" :max="1" :show-file-list="false" @before-upload="(e) => pickFile('files', a, e)"><n-button size="small">{{ up.files[a] ? up.files[a].name : '选择文件' }}</n-button></n-upload>
            <n-upload v-if="data.signing" :default-upload="false" :max="1" :show-file-list="false" accept=".sig" @before-upload="(e) => pickFile('sigs', a, e)"><n-button size="small">{{ up.sigs[a] ? up.sigs[a].name : '选择签名' }}</n-button></n-upload>
          </div>
        </div>
        <div class="field f-bool">
          <div class="f-label"><span class="f-name">上传后立即发布</span><span class="f-hint">新安装与自动更新使用此版本</span></div>
          <div class="f-ctl"><n-switch v-model:value="up.publish" aria-label="上传后立即发布" /></div>
        </div>
        <p class="f-note">签名用 <code class="mono">vps-panel sign &lt;私钥&gt; &lt;文件&gt;</code> 生成。</p>
      </div>
      <template #footer><div class="m-foot"><n-button @click="up.show = false">取消</n-button><n-button type="primary" :loading="busy" @click="upload">上传</n-button></div></template>
    </n-modal>
  </div>
</template>

<style scoped>
.strategy { display: flex; flex-direction: column; gap: 6px; }
.pick-table { border-top: 1px solid var(--c-border); }
.batch { padding: 12px 0 14px; border-top: 1px solid var(--c-border); display: flex; flex-direction: column; gap: 6px; }
.out { max-width: 420px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
