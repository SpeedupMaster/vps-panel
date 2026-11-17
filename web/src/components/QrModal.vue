<script setup>
import { ref, watch } from 'vue'
import { NModal, NButton } from 'naive-ui'
import QRCode from 'qrcode'
import { copy } from '../format'
import { ui } from '../store'

const props = defineProps({ show: Boolean, text: { type: String, default: '' }, title: { type: String, default: '二维码' }, hint: { type: String, default: '' } })
const emit = defineEmits(['update:show'])
const img = ref('')
watch(() => [props.show, props.text], async () => {
  if (props.show && props.text) img.value = await QRCode.toDataURL(props.text, { margin: 1, width: 280, errorCorrectionLevel: 'M' })
}, { immediate: true })
async function doCopy() { if (await copy(props.text)) ui.message.success('已复制') }
</script>

<template>
  <n-modal :show="show" preset="card" :title="title" style="width: min(360px, 94vw)" @update:show="(v) => emit('update:show', v)">
    <div style="display: flex; flex-direction: column; align-items: center; gap: 12px">
      <img v-if="img" :src="img" width="280" height="280" alt="二维码" style="max-width: 100%; height: auto">
      <p v-if="hint" class="small muted" style="margin: 0; text-align: center">{{ hint }}</p>
      <pre class="pre light" style="width: 100%; max-height: 90px; font-size: 11px">{{ text }}</pre>
      <n-button type="primary" block @click="doCopy">复制</n-button>
    </div>
  </n-modal>
</template>
