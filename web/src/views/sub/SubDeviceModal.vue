<script setup>
import { ref, computed, watch } from 'vue'
import { NButton, NInput, NSelect, NModal } from 'naive-ui'
import { api } from '../../api'
import { ui } from '../../store'
import { run, idsCsv } from '../../helpers'

const props = defineProps({
  show: { type: Boolean, default: false },
  device: { type: Object, default: null },
  routes: { type: Array, required: true },
  inSubCount: { type: Number, default: 0 },
  urlBase: { type: String, required: true },
  targets: { type: Array, required: true },
})
const emit = defineEmits(['update:show', 'saved'])

const form = ref({ id: 0, name: '', target: 'auto', enabled: true, token: '', origToken: '', routeIds: [] })
const saving = ref(false)
const isEdit = computed(() => !!form.value.id)

watch(() => [props.show, props.device], ([open]) => {
  if (!open) return
  const d = props.device
  form.value = d
    ? { id: d.id, name: d.name, target: d.target_default || 'auto', enabled: d.enabled, token: d.token || '', origToken: d.token || '', routeIds: idsCsv(d.routes) }
    : { id: 0, name: '', target: 'auto', enabled: true, token: '', origToken: '', routeIds: [] }
}, { immediate: true })

const routeOpts = computed(() => {
  const selected = new Set(form.value.routeIds || [])
  const known = new Set(props.routes.map((r) => r.id))
  return [
    ...props.routes.map((r) => {
      const off = !r.enabled ? '（已停用）' : !r.in_sub ? '（未加入订阅）' : ''
      return { label: r.name + off, value: r.id, disabled: !!off && !selected.has(r.id) }
    }),
    ...(form.value.routeIds || []).filter((id) => !known.has(id)).map((id) => ({ label: `已删除的线路 #${id}`, value: id })),
  ]
})

function randSuffix() {
  const b = new Uint8Array(24)
  crypto.getRandomValues(b)
  return btoa(String.fromCharCode(...b)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

async function submit() {
  if (saving.value) return
  const f = form.value
  if (!f.name.trim()) { ui.message.warning('请填写设备名'); return }
  const tok = f.token.trim()
  if (isEdit.value && !tok) { ui.message.warning('请填写订阅后缀'); return }
  saving.value = true
  try {
    if (isEdit.value) {
      const body = { name: f.name.trim(), target_default: f.target === 'auto' ? '' : f.target, enabled: f.enabled, routes: f.routeIds.join(',') }
      if (tok !== f.origToken) body.token = tok
      if (await run(() => api.put(`/sub/tokens/${f.id}`, body), '已保存')) {
        emit('update:show', false)
        emit('saved', f.id)
      }
    } else {
      const body = { name: f.name.trim(), target: f.target === 'auto' ? '' : f.target, token: tok, routes: f.routeIds.join(',') }
      if (await run(() => api.post('/sub/tokens', body), '已创建')) {
        emit('update:show', false)
        emit('saved', 0)
      }
    }
  } finally { saving.value = false }
}
</script>

<template>
  <n-modal :show="show" preset="card" :title="isEdit ? '编辑设备' : '添加设备'" style="width: min(500px, 94vw)" @update:show="(v) => emit('update:show', v)">
    <div class="m-form">
      <div class="field">
        <div class="f-label"><span class="f-name">设备名</span></div>
        <div class="f-ctl"><n-input v-model:value="form.name" placeholder="如 iPhone / 笔记本" @keyup.enter="submit" /></div>
      </div>
      <div class="field">
        <div class="f-label"><span class="f-name">默认格式</span><span class="f-hint">「自动识别」会按客户端 User-Agent 选择格式</span></div>
        <div class="f-ctl"><n-select v-model:value="form.target" :options="targets" /></div>
      </div>
      <div class="field stacked">
        <div class="f-label">
          <span class="f-name">可订阅线路</span>
          <span class="f-hint">{{ isEdit ? `留空跟随全局设置（当前 ${inSubCount} 条）；指定后仅下发所选线路，仍受停用及被封/离线过滤影响` : `留空跟随全局设置（当前 ${inSubCount} 条）；指定后仅下发所选线路，新建线路需手动勾选` }}</span>
        </div>
        <div class="f-ctl">
          <n-select multiple clearable filterable placeholder="跟随全局设置" max-tag-count="responsive" aria-label="可订阅线路"
            :value="form.routeIds" :options="routeOpts" @update:value="(v) => (form.routeIds = v)" />
        </div>
      </div>
      <div class="field stacked">
        <div class="f-label">
          <span class="f-name">{{ isEdit ? '订阅后缀' : '自定义后缀' }}</span>
          <span class="f-hint">{{ isEdit ? '支持字母、数字、-、_ 和 .；修改后旧链接立即失效' : '留空自动随机生成；支持字母、数字、-、_ 和 .' }}</span>
        </div>
        <div class="f-ctl ctl-col">
          <div class="ctl-row">
            <n-input v-model:value="form.token" :placeholder="isEdit ? '例如 my-phone' : '留空自动生成，如 my-phone'" :clearable="!isEdit" @keyup.enter="submit" />
            <n-button v-if="isEdit" @click="form.token = randSuffix()">随机</n-button>
          </div>
          <div class="sub-preview mono small">{{ urlBase }}/{{ form.token.trim() || (isEdit ? '…' : '<随机后缀>') }}</div>
          <div v-if="form.token.trim() && form.token.trim().length < 8" class="sub-warn small">后缀较短容易被猜到，建议包含不易猜测的字符</div>
        </div>
      </div>
    </div>
    <template #footer>
      <div class="m-foot">
        <n-button @click="emit('update:show', false)">取消</n-button>
        <n-button type="primary" :loading="saving" :disabled="saving" @click="submit">{{ isEdit ? '保存' : '创建' }}</n-button>
      </div>
    </template>
  </n-modal>
</template>

<style scoped>
.sub-preview { padding: 6px 10px; border-radius: 4px; background: var(--c-surface); color: var(--c-text-2); overflow-wrap: anywhere; }
.sub-warn { color: var(--c-warning); line-height: 1.5; }
</style>
