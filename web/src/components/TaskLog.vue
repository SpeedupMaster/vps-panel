<script setup>
// Streams a task's output; emits "done" with the final status.
import { ref, watch, onBeforeUnmount, nextTick } from 'vue'

const props = defineProps({ taskId: { type: [String, Number], default: null }, height: { type: String, default: '320px' } })
const emit = defineEmits(['done'])
const text = ref('')
const status = ref('')
const box = ref(null)
let es = null

function close() { if (es) { es.close(); es = null } }
function start(id) {
  close()
  text.value = ''
  status.value = id ? 'running' : ''
  if (!id) return
  es = new EventSource(`/api/tasks/${id}/stream`)
  es.addEventListener('output', (e) => {
    if (!e.data) return
    text.value += e.data.endsWith('\n') ? e.data : e.data + '\n'
    nextTick(() => { if (box.value) box.value.scrollTop = box.value.scrollHeight })
  })
  es.addEventListener('done', (e) => { status.value = e.data; close(); emit('done', e.data) })
  es.onerror = () => { if (status.value === 'running') status.value = 'disconnected'; close() }
}
watch(() => props.taskId, start, { immediate: true })
onBeforeUnmount(close)
const label = { running: '运行中…', success: '完成', failed: '失败', timeout: '超时', finished: '完成', disconnected: '连接断开' }
</script>

<template>
  <div>
    <div v-if="status" class="row small" style="margin-bottom: 6px">
      <span :class="['dot', status === 'running' ? 'warn' : status === 'success' || status === 'finished' ? 'ok' : 'bad']" />
      <span class="muted">任务 #{{ taskId }} · {{ label[status] || status }}</span>
    </div>
    <pre ref="box" class="pre" :style="{ maxHeight: height, minHeight: '80px' }">{{ text || (status === 'running' ? '等待输出…' : '') }}</pre>
  </div>
</template>
