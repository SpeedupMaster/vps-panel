<script setup>
// Browser terminal over /api/servers/:id/shell (ticketed websocket, agent-side PTY).
import { ref, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { NButton } from 'naive-ui'
import { api } from '../api'
import { isMobile } from '../mobile'

const props = defineProps({ serverId: { type: Number, required: true } })
const el = ref(null)
const status = ref('idle') // idle | connecting | open | closed
let term = null
let fit = null
let ws = null
let ro = null
let dataDisp = null
let unmounted = false

function send(obj) { if (ws && ws.readyState === 1) ws.send(JSON.stringify(obj)) }
function resize() {
  if (!term || !fit) return
  try { fit.fit() } catch { return }
  send({ t: 'r', c: term.cols, r: term.rows })
}
async function connect() {
  disconnect()
  status.value = 'connecting'
  let ticket
  try {
    ticket = (await api.post(`/servers/${props.serverId}/shell-ticket`, {}, { confirm: '打开终端需要确认身份' })).ticket
  } catch (e) {
    if (unmounted) return
    status.value = 'idle'
    term?.writeln('\r\n\x1b[31m' + e.message + '\x1b[0m')
    return
  }
  if (unmounted) return
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const sock = new WebSocket(`${proto}//${location.host}/api/servers/${props.serverId}/shell?ticket=${encodeURIComponent(ticket)}`)
  sock.binaryType = 'arraybuffer'
  ws = sock
  sock.onopen = () => {
    if (unmounted || ws !== sock) { sock.close(); return }
    status.value = 'open'; term?.focus(); resize()
  }
  sock.onmessage = (e) => {
    if (unmounted || ws !== sock) return
    term?.write(typeof e.data === 'string' ? e.data : new Uint8Array(e.data))
  }
  sock.onerror = () => {
    if (unmounted || ws !== sock) return
    term?.writeln('\r\n\x1b[31m[连接错误]\x1b[0m')
  }
  sock.onclose = () => {
    if (ws === sock) ws = null
    if (unmounted) return
    status.value = 'closed'
    term?.writeln('\r\n\x1b[90m[连接已关闭]\x1b[0m')
  }
}
function disconnect() {
  if (ws) {
    ws.onopen = null; ws.onmessage = null; ws.onerror = null; ws.onclose = null
    ws.close(); ws = null
  }
  status.value = 'idle'
}
function key(seq) { send({ t: 'i', d: seq }); term?.focus() }

onMounted(async () => {
  term = new Terminal({
    fontFamily: "'JetBrains Mono', ui-monospace, Menlo, Consolas, monospace", fontSize: isMobile.value ? 12 : 13, cursorBlink: true,
    theme: { background: '#15181b', foreground: '#d7dade', cursor: '#d7dade', selectionBackground: '#3d4248' }, scrollback: 5000,
  })
  fit = new FitAddon()
  term.loadAddon(fit)
  term.open(el.value)
  await nextTick()
  if (unmounted) return
  resize()
  dataDisp = term.onData((d) => send({ t: 'i', d }))
  ro = new ResizeObserver(() => resize())
  ro.observe(el.value)
  term.writeln('\x1b[90m点击「连接」打开终端。会话会写入审计日志，终端内容不记录。\x1b[0m')
})
onBeforeUnmount(() => {
  unmounted = true
  disconnect()
  ro?.disconnect(); ro = null
  dataDisp?.dispose(); dataDisp = null
  term?.dispose(); term = null
  fit = null
})
const keys = [['Esc', '\x1b'], ['Tab', '\t'], ['Ctrl+C', '\x03'], ['Ctrl+D', '\x04'], ['↑', '\x1b[A'], ['↓', '\x1b[B'], ['←', '\x1b[D'], ['→', '\x1b[C'], ['|', '|'], ['/', '/'], ['-', '-']]
defineExpose({ connect, disconnect })
</script>

<template>
  <div class="shell-wrap">
    <div class="row bar">
      <span :class="['dot', status === 'open' ? 'ok' : status === 'connecting' ? 'warn' : 'off']" />
      <span class="small muted">{{ { idle: '未连接', connecting: '连接中…', open: '已连接', closed: '已断开' }[status] }}</span>
      <span class="spacer" />
      <n-button v-if="status !== 'open'" size="small" type="primary" :loading="status === 'connecting'" @click="connect">连接</n-button>
      <n-button v-else size="small" @click="disconnect">断开</n-button>
    </div>
    <div ref="el" class="term" />
    <div v-if="isMobile" class="keys">
      <button v-for="[l, s] in keys" :key="l" type="button" @click="key(s)">{{ l }}</button>
    </div>
  </div>
</template>

<style scoped>
.shell-wrap { display: flex; flex-direction: column; gap: 8px; }
.term { height: 460px; background: #15181b; border-radius: 6px; padding: 8px; overflow: hidden; }
.keys { display: flex; gap: 6px; overflow-x: auto; padding-bottom: 2px; }
.keys button { flex: none; min-width: 44px; height: 36px; border: 1px solid var(--c-border-strong); background: #fff; border-radius: 6px; font-family: var(--mono); font-size: 12px; }
@media (max-width: 767px) { .term { height: 52vh; } }
</style>
