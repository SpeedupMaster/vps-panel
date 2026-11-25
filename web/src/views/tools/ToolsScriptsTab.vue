<script setup>
import { ref, computed, onMounted } from 'vue'
import { NButton, NSelect, NInput, NInputNumber } from 'naive-ui'
import { api } from '../../api'
import { state, serverName, ui } from '../../store'
import { run, ask } from '../../helpers'
import TaskLog from '../../components/TaskLog.vue'

const scripts = ref([])
const script = ref({ id: 0, name: '', content: '' })
const targets = ref([])
const timeout = ref(300)
const scriptRun = ref({ tasks: [] })
const runningScript = ref(false)
const deletingScript = ref(0)

async function loadScripts() { scripts.value = (await run(() => api.get('/scripts'))) || [] }
function pickScript(s) { script.value = s ? { ...s } : { id: 0, name: '', content: '#!/bin/sh\n' } }
async function saveScript() {
  const s = script.value
  if (!s.name || !s.content) { ui.message.warning('填写名称和内容'); return }
  const r = await run(() => (s.id ? api.put(`/scripts/${s.id}`, s) : api.post('/scripts', s)), '已保存')
  if (r) { script.value = { ...r }; loadScripts() }
}
async function delScript(s) {
  if (deletingScript.value) return
  deletingScript.value = s.id
  try {
    if (!(await ask('删除脚本', `删除「${s.name}」？`, { danger: true }))) return
    if (await run(() => api.del(`/scripts/${s.id}`), '已删除')) { if (script.value.id === s.id) pickScript(null); loadScripts() }
  } finally { deletingScript.value = 0 }
}
const execServers = computed(() => state.servers.filter((s) => s.online))
async function runScript() {
  if (runningScript.value) return
  if (!targets.value.length || !script.value.content) { ui.message.warning('选择服务器并填写脚本'); return }
  const names = targets.value.map(serverName).join('、')
  runningScript.value = true
  try {
    if (!(await ask('执行脚本', `在 ${names} 上以 root 执行「${script.value.name || '临时脚本'}」，超时 ${timeout.value} 秒。`))) return
    const r = await run(() => api.post('/scripts/run', { script_id: script.value.id, content: script.value.id ? '' : script.value.content, server_ids: targets.value, timeout: timeout.value }, { confirm: '远程执行需要确认身份' }))
    if (r) scriptRun.value = { tasks: (r.task_ids || []).map((id, i) => ({ id, server: targets.value[i] })) }
  } finally { runningScript.value = false }
}
const dirty = computed(() => { const o = scripts.value.find((s) => s.id === script.value.id); return o && o.content !== script.value.content })

onMounted(loadScripts)
</script>

<template>
  <div class="sec-head"><div class="s-head"><h2>脚本</h2><p>在多台服务器上批量执行脚本。脚本按 sha256 校验后执行，输出按机器分栏显示。</p></div></div>
  <div class="scripts">
    <div class="card s-card">
      <div class="s-card-head"><h3>脚本库</h3><span class="spacer" /><n-button size="small" @click="pickScript(null)">新建</n-button></div>
      <div v-if="!scripts.length" class="empty">还没有脚本</div>
      <div v-else class="s-list">
        <div v-for="s in scripts" :key="s.id" :class="['s-item', { on: script.id === s.id }]" role="button" tabindex="0" @click="pickScript(s)" @keydown.enter="pickScript(s)">
          <span>{{ s.name }}</span><span class="spacer" />
          <n-button size="tiny" quaternary type="error" :loading="deletingScript === s.id" :disabled="!!deletingScript" @click.stop="delScript(s)">删除</n-button>
        </div>
      </div>
    </div>
    <div class="card s-card">
      <div class="field">
        <div class="f-label"><span class="f-name">名称<i v-if="dirty" class="f-dot" /></span><span v-if="dirty" class="f-hint">有未保存的修改，执行时用脚本库里的版本</span></div>
        <div class="f-ctl"><n-input v-model:value="script.name" placeholder="脚本名称" /></div>
      </div>
      <div class="field stacked">
        <div class="f-label"><span class="f-name">内容</span></div>
        <div class="f-ctl"><n-input v-model:value="script.content" type="textarea" :rows="12" class="mono" placeholder="#!/bin/sh" aria-label="脚本内容" /></div>
      </div>
      <div class="f-actions"><span class="spacer" /><n-button @click="saveScript">保存到脚本库</n-button></div>
      <div class="field">
        <div class="f-label"><span class="f-name">执行的服务器</span><span class="f-hint">只列出在线的；未开启远程执行的不能选</span></div>
        <div class="f-ctl"><n-select v-model:value="targets" multiple placeholder="选择服务器" :options="execServers.map((s) => ({ label: s.name + (s.switches?.allow_remote_exec ? '' : '（未开启远程执行）'), value: s.id, disabled: !s.switches?.allow_remote_exec }))" /></div>
      </div>
      <div class="field">
        <div class="f-label"><span class="f-name">超时</span><span class="f-hint">10–3600 秒</span></div>
        <div class="f-ctl"><n-input-number v-model:value="timeout" :min="10" :max="3600" :show-button="false"><template #suffix><span class="f-unit">秒</span></template></n-input-number></div>
      </div>
      <div class="f-actions"><span class="spacer" /><n-button type="primary" :loading="runningScript" :disabled="!targets.length || runningScript" @click="runScript">执行</n-button></div>
    </div>
  </div>
  <div v-if="scriptRun.tasks.length" class="outs">
    <div v-for="t in scriptRun.tasks" :key="t.id" class="card s-card">
      <div class="s-card-head"><h3>{{ serverName(t.server) }}</h3></div>
      <div class="out-body"><task-log :task-id="t.id" height="300px" /></div>
    </div>
  </div>
</template>

<style scoped>
.scripts { display: grid; grid-template-columns: 260px minmax(0, 1fr); gap: 16px; align-items: start; }
.s-list { display: flex; flex-direction: column; gap: 2px; padding-bottom: 12px; margin: 0 -8px; }
.s-item { display: flex; align-items: center; padding: 8px 10px; border-radius: 6px; cursor: pointer; font-size: 13px; min-height: 40px; }
.s-item:hover { background: var(--c-surface); }
.s-item.on { background: var(--c-primary-soft); color: var(--c-primary); }
.outs { display: grid; grid-template-columns: repeat(auto-fit, minmax(360px, 1fr)); gap: 16px; margin-top: 16px; }
.out-body { display: flex; flex-direction: column; gap: 14px; padding-bottom: 18px; }
@media (max-width: 900px) { .scripts, .outs { grid-template-columns: 1fr; } }
</style>
