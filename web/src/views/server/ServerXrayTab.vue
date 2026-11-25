<script setup>
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NInput, NInputNumber, NSelect } from 'naive-ui'
import { api } from '../../api'
import { run, ask } from '../../helpers'
import { bytes, dur } from '../../format'

const props = defineProps({
  sv: { type: Object, required: true },
  xray: { type: Object, default: null },
  myRoutes: { type: Array, required: true },
})
const emit = defineEmits(['reload', 'run-tool', 'task', 'preview'])
const router = useRouter()

const reality = ref({})
const landing = ref({})
const editReality = ref(false)
const editLanding = ref(false)
const rotatingKeys = ref(false)
const deploying = ref(false)
const fps = ['chrome', 'firefox', 'safari', 'ios', 'edge', 'android', 'random', 'randomized'].map((v) => ({ label: v, value: v }))
const methods = ['2022-blake3-aes-128-gcm', '2022-blake3-aes-256-gcm', '2022-blake3-chacha20-poly1305'].map((v) => ({ label: v, value: v }))
const proc = computed(() => props.sv.latest?.xray)

watch(() => props.xray, (x) => {
  if (!x) return
  if (x.reality) reality.value = { port: x.reality.port, target: x.reality.target, server_names: x.reality.server_names, short_ids: x.reality.short_ids, address: x.reality.address, fingerprint: x.reality.fingerprint || 'chrome', regen_keys: false }
  if (x.landing) landing.value = { port: x.landing.port, method: x.landing.method, address: x.landing.address || '' }
}, { immediate: true })

async function saveReality() {
  if (await run(() => api.put(`/servers/${props.sv.id}/reality`, reality.value), '已保存，配置将自动下发')) {
    editReality.value = false
    emit('reload')
  }
}
async function rotateKeys() {
  if (rotatingKeys.value) return
  rotatingKeys.value = true
  try {
    if (!(await ask('轮换 Reality 密钥', '会生成新的 X25519 密钥对，所有使用这台入口的客户端需要刷新订阅。确定继续？'))) return
    const r = props.xray.reality
    if (await run(() => api.put(`/servers/${props.sv.id}/reality`, { port: r.port, target: r.target, server_names: r.server_names, short_ids: r.short_ids, address: r.address, fingerprint: r.fingerprint, regen_keys: true }), '密钥已轮换')) {
      emit('reload')
    }
  } finally { rotatingKeys.value = false }
}
async function saveLanding() {
  if (await run(() => api.put(`/servers/${props.sv.id}/landing`, landing.value), '已保存，配置将自动下发')) {
    editLanding.value = false
    emit('reload')
  }
}
async function deploy() {
  if (deploying.value) return
  deploying.value = true
  try {
    const r = await run(() => api.post(`/servers/${props.sv.id}/deploy`))
    if (r) emit('task', { id: r.task_id, title: '下发配置' })
  } finally { deploying.value = false }
}
async function showPreview() {
  const r = await run(() => api.get(`/servers/${props.sv.id}/preview-config`))
  if (r) emit('preview', r)
}
</script>

<template>
  <div v-if="!xray" class="empty">加载中…</div>
  <div v-else class="sections">
    <section class="s-group">
      <div class="s-head">
        <h2>运行状态</h2>
        <p>Xray 配置由面板生成。改了线路或入站后会自动下发；本地配置被改动时可以手动重新下发。</p>
      </div>
      <div class="card s-card">
        <div class="field f-inline">
          <div class="f-label"><span class="f-name">Xray</span><span v-if="sv.xray_version" class="f-hint">版本 {{ sv.xray_version }}</span></div>
          <div class="f-ctl"><span :class="['tag', sv.xray_running ? 'ok' : 'bad']"><span :class="['dot', sv.xray_running ? 'ok' : 'bad']" />{{ sv.xray_running ? '运行中' : '未运行' }}</span></div>
        </div>
        <div v-if="proc" class="field f-inline">
          <div class="f-label"><span class="f-name">进程</span></div>
          <div class="f-ctl"><span class="f-val">内存 {{ bytes(proc.mem) }} · 已运行 {{ dur(proc.uptime) }}</span></div>
        </div>
        <div class="field f-inline">
          <div class="f-label">
            <span class="f-name">配置</span>
            <span class="f-hint">{{ sv.xray_drift ? '服务器上的配置被改过，重新下发会覆盖' : '与面板一致' }}</span>
          </div>
          <div class="f-ctl"><span :class="['tag', sv.xray_drift ? 'warn' : 'ok']">{{ sv.xray_drift ? '不一致' : 'v' + sv.xray_config_ver }}</span></div>
        </div>
        <div class="f-actions">
          <n-button @click="emit('run-tool', 'xray_log', 'Xray 日志')">查看日志</n-button>
          <n-button @click="showPreview">预览配置</n-button>
          <span class="spacer" />
          <n-button type="primary" :loading="deploying" :disabled="!sv.online || deploying" @click="deploy">重新下发</n-button>
        </div>
      </div>
    </section>

    <section v-if="xray.reality" class="s-group">
      <div class="s-head">
        <h2>Reality 入站</h2>
        <p>客户端直接连接的入站（VLESS · TCP · xtls-rprx-vision）。保存后会重新下发，已导入的订阅需要刷新。</p>
      </div>
      <div class="card s-card">
        <template v-if="!editReality">
          <div class="field f-inline"><div class="f-label"><span class="f-name">端口</span></div><div class="f-ctl"><span class="f-val mono">{{ xray.reality.port }}</span></div></div>
          <div class="field f-inline"><div class="f-label"><span class="f-name">目标站</span></div><div class="f-ctl"><span class="f-val mono">{{ xray.reality.target }}</span></div></div>
          <div class="field f-inline"><div class="f-label"><span class="f-name">SNI</span></div><div class="f-ctl"><span class="f-val mono">{{ xray.reality.server_names }}</span></div></div>
          <div class="field f-inline"><div class="f-label"><span class="f-name">shortIds</span></div><div class="f-ctl"><span class="f-val">{{ (xray.reality.short_ids || '').split(',').filter(Boolean).length }} 个</span></div></div>
          <div class="field f-inline">
            <div class="f-label"><span class="f-name">客户端连接地址</span><span v-if="!xray.reality.address" class="f-hint">未单独设置，使用 IPv4</span></div>
            <div class="f-ctl"><span class="f-val mono">{{ xray.reality.address || sv.ipv4 || '—' }}</span></div>
          </div>
          <div class="field f-inline"><div class="f-label"><span class="f-name">指纹</span></div><div class="f-ctl"><span class="f-val">{{ xray.reality.fingerprint }}</span></div></div>
          <div class="field stacked">
            <div class="f-label"><span class="f-name">公钥</span><span class="f-hint">轮换密钥后，使用这台入口的客户端需要刷新订阅</span></div>
            <div class="f-ctl"><span class="f-val mono key">{{ xray.reality.public_key }}</span></div>
          </div>
          <div class="f-actions">
            <n-button :loading="rotatingKeys" :disabled="rotatingKeys" @click="rotateKeys">轮换密钥</n-button>
            <span class="spacer" />
            <n-button type="primary" @click="editReality = true">编辑</n-button>
          </div>
        </template>
        <template v-else>
          <div class="field"><div class="f-label"><span class="f-name">端口</span></div><div class="f-ctl"><n-input-number v-model:value="reality.port" :min="1" :max="65535" :show-button="false" /></div></div>
          <div class="field"><div class="f-label"><span class="f-name">目标站</span><span class="f-hint">host:port。可在「工具 → Reality 目标扫描」里挑选：需支持 TLS 1.3 与 H2、握手快</span></div><div class="f-ctl"><n-input v-model:value="reality.target" placeholder="www.example.com:443" /></div></div>
          <div class="field"><div class="f-label"><span class="f-name">SNI</span><span class="f-hint">逗号分隔</span></div><div class="f-ctl"><n-input v-model:value="reality.server_names" /></div></div>
          <div class="field"><div class="f-label"><span class="f-name">shortIds</span><span class="f-hint">逗号分隔，留空自动生成</span></div><div class="f-ctl"><n-input v-model:value="reality.short_ids" placeholder="自动生成" /></div></div>
          <div class="field"><div class="f-label"><span class="f-name">客户端连接地址</span><span class="f-hint">订阅里给客户端用的地址，留空用 IPv4</span></div><div class="f-ctl"><n-input v-model:value="reality.address" placeholder="默认 IPv4" clearable /></div></div>
          <div class="field"><div class="f-label"><span class="f-name">指纹</span></div><div class="f-ctl"><n-select v-model:value="reality.fingerprint" :options="fps" /></div></div>
          <div class="f-actions"><span class="spacer" /><n-button @click="editReality = false">取消</n-button><n-button type="primary" @click="saveReality">保存并下发</n-button></div>
        </template>
      </div>
    </section>

    <section v-if="xray.landing" class="s-group">
      <div class="s-head">
        <h2>SS2022 落地</h2>
        <p>只接收入口机转发来的流量，防火墙只放行入口机 IP。</p>
      </div>
      <div class="card s-card">
        <template v-if="!editLanding">
          <div class="field f-inline"><div class="f-label"><span class="f-name">端口</span></div><div class="f-ctl"><span class="f-val mono">{{ xray.landing.port }}</span></div></div>
          <div class="field f-inline"><div class="f-label"><span class="f-name">加密方式</span></div><div class="f-ctl"><span class="f-val mono">{{ xray.landing.method }}</span></div></div>
          <div class="field f-inline">
            <div class="f-label"><span class="f-name">连接地址</span><span v-if="!xray.landing.address" class="f-hint">未单独设置，入口机连公网 IP</span></div>
            <div class="f-ctl"><span class="f-val mono">{{ xray.landing.address || sv.ipv4 || '公网 IP' }}</span></div>
          </div>
          <div class="f-actions"><span class="spacer" /><n-button type="primary" @click="editLanding = true">编辑</n-button></div>
        </template>
        <template v-else>
          <div class="field"><div class="f-label"><span class="f-name">端口</span><span class="f-hint">1024–65535</span></div><div class="f-ctl"><n-input-number v-model:value="landing.port" :min="1024" :max="65535" :show-button="false" /></div></div>
          <div class="field"><div class="f-label"><span class="f-name">加密方式</span></div><div class="f-ctl"><n-select v-model:value="landing.method" :options="methods" /></div></div>
          <div class="field"><div class="f-label"><span class="f-name">连接地址</span><span class="f-hint">入口和落地在同一内网、或落地是 NAT 机端口映射时填写。填内网 IP 时，落地防火墙会同时放行内网网段</span></div><div class="f-ctl"><n-input v-model:value="landing.address" placeholder="默认 公网 IP" clearable /></div></div>
          <div class="f-actions"><span class="spacer" /><n-button @click="editLanding = false">取消</n-button><n-button type="primary" @click="saveLanding">保存并下发</n-button></div>
        </template>
      </div>
    </section>

    <section v-if="xray.landing" class="s-group">
      <div class="s-head"><h2>入口用户</h2><p>连到这台落地的入口机，以及它们本月的流量。</p></div>
      <div class="card s-card">
        <div v-if="!xray.landing_users?.length" class="empty">还没有入口机连到这台落地</div>
        <div v-else class="table-wrap">
          <table class="plain">
            <thead><tr><th>入口</th><th>上行</th><th>下行</th></tr></thead>
            <tbody><tr v-for="u in xray.landing_users" :key="u.id"><td>{{ u.entry_name }}</td><td class="mono">{{ bytes(u.month_up) }}</td><td class="mono">{{ bytes(u.month_down) }}</td></tr></tbody>
          </table>
        </div>
      </div>
    </section>

    <section class="s-group">
      <div class="s-head">
        <h2>本机线路</h2>
        <p>{{ myRoutes.length ? `${myRoutes.length} 条线路经过这台机器。` : '还没有线路经过这台机器。' }}线路在「线路」页统一管理。</p>
      </div>
      <div class="card s-card">
        <div v-if="sv.cap_reality" class="s-card-head">
          <span class="f-hint">以这台为入口新建直连或链式线路</span>
          <span class="spacer" />
          <n-button size="small" type="primary" @click="router.push({ name: 'route-new', query: { entry: sv.id } })">新建线路</n-button>
        </div>
        <div v-if="!myRoutes.length" class="empty">没有线路经过这台机器</div>
        <div v-else class="table-wrap">
          <table class="plain">
            <thead><tr><th>类型</th><th>名称</th><th>延迟</th><th>今日</th><th>状态</th></tr></thead>
            <tbody>
              <tr v-for="r in myRoutes" :key="r.id">
                <td><span class="tag">{{ r.type === 'direct' ? '直连' : '链式' }}</span></td>
                <td>{{ r.name }}<div class="muted small">{{ r.type === 'chain' ? `${r.entry_name} → ${r.landing_name}` : '' }}</div></td>
                <td class="mono small">{{ r.type === 'chain' && r.segment_ms ? '线路段 ' + Math.round(r.segment_ms) + ' ms' : r.latency_ms ? Math.round(r.latency_ms) + ' ms' : '—' }}</td>
                <td class="mono">{{ bytes(r.today_bytes) }}</td>
                <td><span :class="['tag', { ok: 'ok', degraded: 'warn', down: 'bad' }[r.status]]">{{ { ok: '正常', degraded: '降级', down: '中断', disabled: '已停用' }[r.status] || r.status }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.f-val { font-size: 13.5px; text-align: right; overflow-wrap: anywhere; }
.field.stacked .f-val { text-align: left; }
.f-val.key { font-size: 12.5px; word-break: break-all; }
</style>
