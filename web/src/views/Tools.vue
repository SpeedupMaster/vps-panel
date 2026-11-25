<script setup>
import { ref, computed, defineAsyncComponent, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NSelect, NTabs, NTabPane } from 'naive-ui'
import { state, loadServers } from '../store'
import { useQueryTab } from '../composables/useQueryTab'
import ToolsCheckTab from './tools/ToolsCheckTab.vue'
import ToolsBbrTab from './tools/ToolsBbrTab.vue'
import ToolsHistoryTab from './tools/ToolsHistoryTab.vue'
import ToolsScriptsTab from './tools/ToolsScriptsTab.vue'
import ToolsSchedulesTab from './tools/ToolsSchedulesTab.vue'

const WebShell = defineAsyncComponent(() => import('../components/WebShell.vue'))

const route = useRoute()
const router = useRouter()
const server = ref(Number(route.query.server) || null)
const tab = useQueryTab(['check', 'bbr', 'history', 'shell', 'scripts', 'schedules'], 'check')
const shellOpened = ref(tab.value === 'shell')
const sv = computed(() => state.servers.find((s) => s.id === server.value))
const serverOpts = computed(() => state.servers.filter((s) => s.status !== 'pending').map((s) => ({ label: `${s.name}${s.online ? '' : '（离线）'}`, value: s.id })))

onMounted(async () => {
  if (!state.serversLoaded) await loadServers().catch(() => {})
  if (!server.value && state.servers.length) server.value = (state.servers.find((s) => s.online) || state.servers[0]).id
})
watch(server, (v) => router.replace({ query: { ...route.query, server: v, run: undefined } }))
watch(tab, (t) => { if (t === 'shell') shellOpened.value = true })
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div class="ph"><h1>工具</h1><p class="page-desc">在选定的服务器上执行检测、网络优化、终端和脚本。</p></div>
      <span class="spacer" />
      <n-select v-model:value="server" :options="serverOpts" filterable style="width: 200px" placeholder="选择服务器" />
    </div>
    <div v-if="sv?.blocked" class="banner">
      <span>{{ sv.name }} 疑似 IP 被封。</span><span class="spacer" />
      <n-button size="small" @click="router.push({ name: 'latency' })">查看回程延迟</n-button>
      <n-button size="small" @click="router.push({ name: 'routes' })">管理线路</n-button>
    </div>

    <n-tabs v-model:value="tab" type="line" animated>
      <n-tab-pane name="check" tab="检测">
        <tools-check-tab :server="server" :sv="sv" :server-opts="serverOpts" />
      </n-tab-pane>
      <n-tab-pane name="bbr" tab="网络优化">
        <tools-bbr-tab :server="server" :sv="sv" />
      </n-tab-pane>
      <n-tab-pane name="history" tab="历史结果">
        <tools-history-tab :server="server" :sv="sv" />
      </n-tab-pane>
      <n-tab-pane name="shell" tab="终端" display-directive="show">
        <div class="sec-head"><div class="s-head"><h2>终端</h2><p>打开终端需要二次验证。每次会话写入审计日志（不记录终端内容），关闭页面即断开。</p></div></div>
        <div class="card s-card">
          <div v-if="!sv" class="empty">选择服务器</div>
          <template v-else-if="!sv.switches?.allow_remote_exec">
            <div class="field stacked">
              <div class="f-label"><span class="f-name">远程命令未开启</span><span class="f-hint">为了防止面板被攻破后控制所有 VPS，远程命令默认关闭，只能在 VPS 本机上开启。SSH 登录这台机器后执行：</span></div>
              <div class="f-ctl"><pre class="pre">vpsagent config set allow_remote_exec true &amp;&amp; systemctl restart vpsagent</pre></div>
            </div>
            <p class="f-note">开启后刷新本页即可使用；关闭方法相同，把 true 改为 false。</p>
          </template>
          <div v-else-if="shellOpened" class="s-pad"><web-shell :key="sv.id" :server-id="sv.id" /></div>
        </div>
      </n-tab-pane>
      <n-tab-pane name="scripts" tab="脚本">
        <tools-scripts-tab />
      </n-tab-pane>
      <n-tab-pane name="schedules" tab="定时任务">
        <tools-schedules-tab :server-opts="serverOpts" />
      </n-tab-pane>
    </n-tabs>
  </div>
</template>

<style scoped>
p { line-height: 1.6; }
</style>
