<script setup>
import { NModal } from 'naive-ui'
import TaskLog from './TaskLog.vue'

defineProps({ show: Boolean, taskId: { type: [String, Number], default: null }, title: { type: String, default: '任务输出' } })
const emit = defineEmits(['update:show', 'done'])
</script>

<template>
  <n-modal :show="show" preset="card" :title="title" style="width: min(760px, 96vw)" @update:show="(v) => emit('update:show', v)">
    <task-log :task-id="taskId" height="60vh" @done="(s) => emit('done', s)" />
  </n-modal>
</template>
