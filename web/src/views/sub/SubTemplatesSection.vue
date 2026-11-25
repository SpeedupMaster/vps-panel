<script setup>
import { ref, onMounted } from 'vue'
import { NButton, NInput, NSelect, NSwitch, NModal } from 'naive-ui'
import { api } from '../../api'
import { run, ask } from '../../helpers'

defineProps({
  targets: { type: Array, required: true },
  targetLabel: { type: Object, required: true },
})
const emit = defineEmits(['changed'])

const templates = ref([])
const tplHint = '节点会注入到模板的 proxies / outbounds；代理组里用 {{all}} 引用全部节点、{{regions}} 引用按国家动态生成的组、{{regions_ai}} 引用避开香港/大陆等 AI 受限地区的国家组、{{groups}} 引用全部分组。保存前会用当前节点渲染一次校验。'
const tpl = ref({ show: false, id: 0, target: 'mihomo', name: '', content: '', active: true })
const builtin = ref(null)
const saving = ref(false)
const deleting = ref(0)

async function loadTemplates() { templates.value = (await run(() => api.get('/sub/templates'))) || [] }
async function editTemplate(t) {
  if (!builtin.value) builtin.value = await run(() => api.get('/sub/templates/default'))
  tpl.value = t ? { show: true, ...t } : { show: true, id: 0, target: 'mihomo', name: '我的模板', content: builtin.value?.mihomo || '', active: true }
}
function loadBuiltin() { tpl.value.content = builtin.value?.[tpl.value.target] || '' }
async function saveTemplate() {
  if (saving.value) return
  const t = tpl.value
  const body = { target: t.target, name: t.name, content: t.content, active: t.active }
  saving.value = true
  try {
    const r = await run(() => (t.id ? api.put(`/sub/templates/${t.id}`, body) : api.post('/sub/templates', body)), '模板已保存')
    if (r) { tpl.value.show = false; loadTemplates(); emit('changed') }
  } finally { saving.value = false }
}
async function delTemplate(t) {
  if (deleting.value) return
  deleting.value = t.id
  try {
    if (!(await ask('删除模板', `删除「${t.name}」？会回到内置模板。`))) return
    if (await run(() => api.del(`/sub/templates/${t.id}`), '已删除')) { loadTemplates(); emit('changed') }
  } finally { deleting.value = 0 }
}
async function activate(t, v) {
  if (await run(() => api.put(`/sub/templates/${t.id}`, { ...t, active: v }))) { loadTemplates(); emit('changed') }
}

onMounted(loadTemplates)
</script>

<template>
  <section class="l-sec">
    <div class="sec-head">
      <div class="s-head"><h2>规则模板</h2><p>分流规则模板。没有启用自定义模板时，使用内置防 DNS 泄露、按国家动态分组与多场景分流模板。</p></div>
      <span class="spacer" />
      <n-button @click="editTemplate(null)">自定义模板</n-button>
    </div>
    <div class="card s-card">
      <div v-if="!templates.length" class="empty">正在使用内置模板</div>
      <div v-else class="table-wrap">
        <table class="plain">
          <thead><tr><th>名称</th><th>格式</th><th>启用</th><th /></tr></thead>
          <tbody>
            <tr v-for="t in templates" :key="t.id">
              <td>{{ t.name }}</td><td>{{ targetLabel[t.target] || t.target }}</td>
              <td><n-switch size="small" :value="t.active" @update:value="(v) => activate(t, v)" /></td>
              <td style="text-align: right"><n-button size="tiny" quaternary @click="editTemplate(t)">编辑</n-button><n-button size="tiny" quaternary type="error" :loading="deleting === t.id" :disabled="!!deleting" @click="delTemplate(t)">删除</n-button></td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </section>

  <n-modal v-model:show="tpl.show" preset="card" :title="tpl.id ? '编辑模板' : '自定义模板'" style="width: min(860px, 96vw)">
    <div class="m-form">
      <div class="field"><div class="f-label"><span class="f-name">名称</span></div><div class="f-ctl"><n-input v-model:value="tpl.name" placeholder="例如 我的分流规则" /></div></div>
      <div class="field">
        <div class="f-label"><span class="f-name">格式</span><span v-if="tpl.id" class="f-hint">创建后不能修改</span></div>
        <div class="f-ctl ctl-row"><n-select v-model:value="tpl.target" :options="targets.slice(1, 3)" :disabled="!!tpl.id" aria-label="格式" /><n-button @click="loadBuiltin">载入内置模板</n-button></div>
      </div>
      <div class="field stacked">
        <div class="f-label"><span class="f-name">模板内容</span><span class="f-hint">{{ tplHint }}</span></div>
        <div class="f-ctl"><n-input v-model:value="tpl.content" type="textarea" :rows="16" class="mono" placeholder="模板内容" aria-label="模板内容" /></div>
      </div>
      <div class="field f-bool">
        <div class="f-label"><span class="f-name">保存后启用</span><span class="f-hint">同一格式只能启用一个模板</span></div>
        <div class="f-ctl"><n-switch v-model:value="tpl.active" aria-label="保存后启用" /></div>
      </div>
    </div>
    <template #footer><div class="m-foot"><n-button @click="tpl.show = false">取消</n-button><n-button type="primary" :loading="saving" :disabled="saving" @click="saveTemplate">保存</n-button></div></template>
  </n-modal>
</template>
