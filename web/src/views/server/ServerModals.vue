<script setup>
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { NButton, NModal, NSwitch } from 'naive-ui'
import { api } from '../../api'
import { loadServers, ui } from '../../store'
import { run } from '../../helpers'
import { copy, countdown } from '../../format'

const props = defineProps({
  serverId: { type: Number, required: true },
  upgrade: { type: Object, required: true },
  install: { type: Object, required: true },
  cfgView: { type: Object, required: true },
  preview: { type: Object, required: true },
})
const emit = defineEmits(['upgraded'])

const upgrading = ref(false)
const now = ref(Date.now())
let installTimer = null
watch(() => props.install.show, (active) => {
  if (installTimer) { clearInterval(installTimer); installTimer = null }
  if (active) {
    now.value = Date.now()
    installTimer = setInterval(() => { now.value = Date.now() }, 1000)
  }
})
onBeforeUnmount(() => clearInterval(installTimer))
const installLeft = computed(() => countdown(props.install.expires_at, now.value))

async function doUpgrade() {
  if (upgrading.value) return
  const u = props.upgrade
  upgrading.value = true
  try {
    if (await run(() => api.post(`/servers/${props.serverId}/upgrade`, { cap_reality: u.cap_reality, cap_landing: u.cap_landing }), '已升级，Xray 将自动安装')) {
      u.show = false
      emit('upgraded')
      loadServers()
    }
  } finally { upgrading.value = false }
}
async function copyInstall() {
  if (await copy(props.install.command)) ui.message.success('已复制安装命令')
}
function diffClass(line) {
  if (line.startsWith('@@')) return 'diff-hunk'
  if (line.startsWith('+')) return 'diff-add'
  if (line.startsWith('-')) return 'diff-del'
  return ''
}
</script>

<template>
  <n-modal v-model:show="upgrade.show" preset="card" title="升级为节点" style="width: min(460px, 94vw)">
    <div class="m-form">
      <p class="f-note first">会在这台机器上安装 Xray，并由面板生成配置。至少选择一种能力。</p>
      <div class="field f-bool">
        <div class="f-label"><span class="f-name">Reality 入站</span><span class="f-hint">直连或入口：客户端连接的节点，默认端口 443</span></div>
        <div class="f-ctl"><n-switch v-model:value="upgrade.cap_reality" aria-label="Reality 入站" /></div>
      </div>
      <div class="field f-bool">
        <div class="f-label"><span class="f-name">SS2022 落地</span><span class="f-hint">只接收入口机流量，端口随机，防火墙只放行入口机 IP</span></div>
        <div class="f-ctl"><n-switch v-model:value="upgrade.cap_landing" aria-label="SS2022 落地" /></div>
      </div>
    </div>
    <template #footer><div class="m-foot"><n-button @click="upgrade.show = false">取消</n-button><n-button type="primary" :loading="upgrading" :disabled="(!upgrade.cap_reality && !upgrade.cap_landing) || upgrading" @click="doUpgrade">升级</n-button></div></template>
  </n-modal>

  <n-modal v-model:show="install.show" preset="card" title="安装命令" style="width: min(640px, 96vw)">
    <p class="small" style="margin-top: 0">用 root 登录 VPS 执行（{{ installLeft }} 后失效，只能使用一次）：</p>
    <pre class="pre" style="user-select: all">{{ install.command }}</pre>
    <template #footer>
      <div class="m-foot">
        <n-button @click="install.show = false">关闭</n-button>
        <n-button type="primary" @click="copyInstall">复制命令</n-button>
      </div>
    </template>
  </n-modal>

  <n-modal v-model:show="cfgView.show" preset="card" :title="cfgView.title" style="width: min(900px, 96vw)">
    <pre v-if="!cfgView.diff" class="pre light" style="max-height: 70vh">{{ cfgView.body }}</pre>
    <pre v-else class="pre light" style="max-height: 70vh"><span v-for="(l, i) in cfgView.body.split('\n')" :key="i" :class="diffClass(l)">{{ l || ' ' }}</span></pre>
    <p class="small muted">密钥字段已隐藏。</p>
  </n-modal>

  <n-modal v-model:show="preview.show" preset="card" title="配置预览（未下发）" style="width: min(900px, 96vw)">
    <pre class="pre light" style="max-height: 60vh">{{ preview.config }}</pre>
    <template v-if="preview.firewall">
      <h3 class="sub-h">防火墙规则</h3>
      <pre class="pre light">{{ typeof preview.firewall === 'string' ? preview.firewall : JSON.stringify(preview.firewall, null, 2) }}</pre>
    </template>
  </n-modal>
</template>

<style scoped>
.sub-h { font-size: 14px; margin: 18px 0 8px; }
p { margin: 6px 0; }
</style>
