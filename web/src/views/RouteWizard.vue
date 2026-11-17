<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NInput, NSteps, NStep, NCheckbox, NSwitch } from 'naive-ui'
import { api } from '../api'
import { state, loadServers, ui } from '../store'
import { run } from '../helpers'
import { flag, copy } from '../format'
import { isMobile } from '../mobile'
import QrModal from '../components/QrModal.vue'

const route = useRoute()
const router = useRouter()
const editId = computed(() => (route.name === 'route-edit' ? Number(route.params.id) : 0))
const step = ref(1)
const topo = ref({ nodes: [] })
const peers = ref([])
const segs = ref([])
const allRoutes = ref([])
const form = ref({ entry_server_id: 0, type: 'direct', landing_server_id: 0, backup_ids: [], name: '', tags: '', in_sub: true, enabled: true, sort: 0 })
const nameTouched = ref(false)
const saving = ref(false)
const result = ref(null) // {route, warning}
const qr = ref(false)

onMounted(async () => {
  await loadServers().catch(() => {})
  const [t, p, s, rs] = await Promise.all([
    api.get('/topology').catch(() => ({ nodes: [] })), api.get('/probes/matrix?kind=peer').catch(() => []),
    api.get('/probes/matrix?kind=segment').catch(() => []), api.get('/routes').catch(() => []),
  ])
  topo.value = t; peers.value = p || []; segs.value = s || []; allRoutes.value = rs || []
  if (editId.value) {
    const r = allRoutes.value.find((x) => x.id === editId.value)
    if (!r) { ui.message.error('线路不存在'); router.replace({ name: 'routes' }); return }
    form.value = { entry_server_id: r.entry_server_id, type: r.type, landing_server_id: r.landing_server_id, backup_ids: String(r.backup_ids || '').split(',').map(Number).filter(Boolean), name: r.name, tags: r.tags, in_sub: r.in_sub, enabled: r.enabled, sort: r.sort }
    nameTouched.value = true
  } else if (route.query.entry) {
    form.value.entry_server_id = Number(route.query.entry)
    step.value = 2
  }
})

const nodes = computed(() => state.servers.filter((s) => s.mode === 'node'))
const entries = computed(() => nodes.value.filter((s) => s.cap_reality))
const landings = computed(() => nodes.value.filter((s) => s.cap_landing && s.id !== form.value.entry_server_id))
const entrySv = computed(() => state.servers.find((s) => s.id === form.value.entry_server_id))
const landingSv = computed(() => state.servers.find((s) => s.id === form.value.landing_server_id))
const backhaul = (id) => topo.value.nodes?.find((n) => n.id === id)?.backhaul_ms || 0
function segment(entryId, landingId) {
  const s = segs.value.find((c) => c.server_id === entryId && c.target === `seg-${landingId}`)
  if (s && s.avg > 0) return { ms: s.avg, loss: s.loss, src: '线路段' }
  const p = peers.value.find((c) => c.server_id === entryId && c.target === `peer-${landingId}`)
  if (p && p.avg > 0) return { ms: p.avg, loss: p.loss, src: 'ping 估算' }
  return null
}
const autoName = computed(() => {
  if (!entrySv.value) return ''
  return form.value.type === 'chain' && landingSv.value ? `${entrySv.value.name} → ${landingSv.value.name}` : `${entrySv.value.name} 直连`
})
watch(autoName, (v) => { if (!nameTouched.value) form.value.name = v }, { immediate: true })
watch(() => form.value.landing_server_id, (l) => { form.value.backup_ids = form.value.backup_ids.filter((b) => b !== l) })

const canNext = computed(() => {
  if (step.value === 1) return !!form.value.entry_server_id
  if (step.value === 2) return form.value.type === 'direct' || !!form.value.landing_server_id
  if (step.value === 3) return !!form.value.name.trim()
  return true
})
const sameEntryRoutes = computed(() => allRoutes.value.filter((r) => r.entry_server_id === form.value.entry_server_id && r.id !== editId.value))
const estimate = computed(() => {
  const bh = backhaul(form.value.entry_server_id)
  if (form.value.type === 'direct') return bh
  const s = segment(form.value.entry_server_id, form.value.landing_server_id)
  return bh && s ? bh + s.ms : 0
})
const changes = computed(() => {
  const out = []
  const e = entrySv.value?.name
  if (!e) return out
  if (editId.value) out.push(`${e}：更新 Reality 入站中这条线路的用户与路由规则`)
  else out.push(`${e}：Reality 入站新增 1 个 UUID（不新开端口）`)
  if (form.value.type === 'chain' && landingSv.value) {
    out.push(`${e}：新增 / 复用出站 → ${landingSv.value.name}（SS2022），按用户路由`)
    out.push(`${landingSv.value.name}：新增入口用户（若该入口首次使用），防火墙放行 ${e} 的 IP`)
    if (form.value.backup_ids.length) out.push(`${e}：备用落地 ${form.value.backup_ids.map((b) => state.servers.find((s) => s.id === b)?.name).join('、')}，由 Observatory 探活自动切换`)
  } else {
    out.push(`${e}：该用户直接出站（freedom）`)
  }
  out.push(form.value.in_sub ? '订阅：加入订阅，客户端刷新后可见' : '订阅：不加入订阅（可在线路列表里随时打开）')
  return out
})

function next() { if (canNext.value) step.value++ }
function prev() { if (step.value > 1) step.value-- }
async function submit() {
  saving.value = true
  const body = { ...form.value, name: form.value.name.trim() }
  if (body.type === 'direct') { body.landing_server_id = 0; body.backup_ids = [] }
  const r = await run(() => (editId.value ? api.put(`/routes/${editId.value}`, body) : api.post('/routes', body)))
  saving.value = false
  if (!r) return
  result.value = r
  if (r.warning) ui.message.warning('已保存，但下发未完成：' + r.warning)
  step.value = 5
  pollStatus()
}
const view = ref(null)
let tries = 0
async function pollStatus() {
  const id = result.value?.route?.id
  if (!id) return
  const rs = await api.get('/routes').catch(() => [])
  view.value = rs.find((x) => x.id === id) || null
  if (++tries < 6 && (!view.value || !view.value.share_link || view.value.status !== 'ok')) setTimeout(pollStatus, 5000)
}
const checks = computed(() => {
  const v = view.value
  const ok = (b) => (b ? 'ok' : 'wait')
  return [
    { text: '面板已保存线路', st: result.value ? 'ok' : 'wait' },
    { text: '配置已下发并通过 xray run -test 校验', st: result.value?.warning ? 'bad' : ok(v) },
    { text: `入口 ${v?.entry_name || ''} Xray 运行中`, st: v ? (v.xray_running ? 'ok' : 'bad') : 'wait' },
    ...(form.value.type === 'chain' ? [{ text: `入口 → 落地线路段可达${v?.segment_ms ? `（${Math.round(v.segment_ms)} ms）` : ''}`, st: v ? (v.segment_ms > 0 ? 'ok' : 'wait') : 'wait' }] : []),
    { text: '分享链接已生成', st: ok(v?.share_link) },
  ]
})
async function copyLink() { if (view.value?.share_link && (await copy(view.value.share_link))) ui.message.success('已复制分享链接') }
</script>

<template>
  <div class="page wizard">
    <div class="crumb small muted"><router-link :to="{ name: 'routes' }">线路</router-link> / {{ editId ? '编辑线路' : '新建线路' }}</div>
    <div class="page-head"><h1>{{ editId ? '编辑线路' : '新建线路' }}</h1></div>

    <n-steps :current="step" size="small" :vertical="isMobile" class="steps">
      <n-step title="入口机" />
      <n-step title="线路类型" />
      <n-step title="名称" />
      <n-step title="预览变更" />
      <n-step title="自检" />
    </n-steps>

    <div class="card">
      <template v-if="step === 1">
        <h2>选择入口机</h2>
        <p class="muted small">客户端会连接这台机器的 Reality 端口。新线路只在它的 Reality 入站里新增一个 UUID，不会新开端口。</p>
        <div v-if="!entries.length" class="empty">没有开启 Reality 的节点。先在总览里添加节点，或把已有机器升级为节点。</div>
        <div class="pick-grid">
          <button v-for="s in entries" :key="s.id" type="button" :class="['pick', { on: form.entry_server_id === s.id }]" @click="form.entry_server_id = s.id">
            <span class="row"><span class="flag">{{ flag(s.country_code) }}</span><b>{{ s.name }}</b><span class="spacer" /><span :class="['dot', s.blocked ? 'bad' : s.online ? 'ok' : 'off']" /></span>
            <span class="muted small">{{ s.region || '—' }}</span>
            <span class="small">回程 {{ backhaul(s.id) ? Math.round(backhaul(s.id)) + ' ms' : '—' }}<span v-if="s.blocked" class="bad-text"> · 疑似被封</span></span>
          </button>
        </div>
      </template>

      <template v-else-if="step === 2">
        <h2>线路类型</h2>
        <div class="pick-grid two">
          <button type="button" :class="['pick', { on: form.type === 'direct' }]" @click="form.type = 'direct'">
            <b>直连</b><span class="muted small">客户端 → {{ entrySv?.name }} → 目标网站。出口 IP 是入口机的 IP。</span>
          </button>
          <button type="button" :class="['pick', { on: form.type === 'chain' }]" :disabled="!landings.length" @click="form.type = 'chain'">
            <b>经落地（链式）</b><span class="muted small">{{ landings.length ? `客户端 → ${entrySv?.name} → 落地 → 目标网站。出口 IP 是落地机的 IP。` : '还没有落地机（开启 SS2022 落地能力的节点）。' }}</span>
          </button>
        </div>
        <template v-if="form.type === 'chain'">
          <h3>落地机</h3>
          <div class="pick-grid">
            <button v-for="l in landings" :key="l.id" type="button" :class="['pick', { on: form.landing_server_id === l.id }]" @click="form.landing_server_id = l.id">
              <span class="row"><span class="flag">{{ flag(l.country_code) }}</span><b>{{ l.name }}</b><span class="spacer" /><span :class="['dot', l.online ? 'ok' : 'off']" /></span>
              <span class="muted small">{{ l.region || '—' }}</span>
              <span class="small" v-if="segment(form.entry_server_id, l.id)">{{ segment(form.entry_server_id, l.id).src }} {{ Math.round(segment(form.entry_server_id, l.id).ms) }} ms · 丢包 {{ Math.round(segment(form.entry_server_id, l.id).loss) }}%</span>
              <span class="small muted" v-else>线路段延迟暂无数据</span>
            </button>
          </div>
          <h3>备用落地</h3>
          <p v-if="landings.length < 2" class="muted small">目前只有 1 台落地机，暂不可用。以后添加第二台落地后，可在这里设置自动切换。</p>
          <div v-else class="col">
            <n-checkbox v-for="l in landings.filter((x) => x.id !== form.landing_server_id)" :key="l.id"
              :checked="form.backup_ids.includes(l.id)" @update:checked="(v) => form.backup_ids = v ? [...form.backup_ids, l.id] : form.backup_ids.filter((b) => b !== l.id)">
              {{ l.name }}<span class="muted small" v-if="segment(form.entry_server_id, l.id)"> · {{ Math.round(segment(form.entry_server_id, l.id).ms) }} ms</span>
            </n-checkbox>
            <p class="muted small">主落地不可达时，入口机的 Xray 会按 Observatory 探活结果自动切到可用的备用落地，并发送告警。</p>
          </div>
        </template>
      </template>

      <template v-else-if="step === 3">
        <h2>名称与选项</h2>
        <div class="field"><label>名称</label>
          <n-input v-model:value="form.name" maxlength="60" @update:value="nameTouched = true" />
          <span class="muted small">默认按「入口 → 落地」生成，可以修改。订阅里显示的名字按订阅设置里的命名模板生成。</span>
        </div>
        <div class="field"><label>标签</label><n-input v-model:value="form.tags" placeholder="可选，逗号分隔" /></div>
        <div class="row field"><n-switch v-model:value="form.in_sub" /><span>加入订阅</span></div>
        <div v-if="editId" class="row field"><n-switch v-model:value="form.enabled" /><span>启用</span></div>
      </template>

      <template v-else-if="step === 4">
        <h2>预览变更</h2>
        <div class="kv" style="margin-bottom: 14px">
          <span>名称</span><span>{{ form.name }}</span>
          <span>路径</span><span>客户端 → {{ entrySv?.name }}{{ form.type === 'chain' ? ' → ' + landingSv?.name : '' }} → 互联网</span>
          <span>估算延迟</span><span>{{ estimate ? Math.round(estimate) + ' ms' : '—' }}<span class="muted small">（入口回程{{ form.type === 'chain' ? ' + 线路段' : '' }}）</span></span>
          <span>同入口线路</span><span>{{ sameEntryRoutes.length }} 条</span>
        </div>
        <ul class="changes"><li v-for="(c, i) in changes" :key="i">{{ c }}</li></ul>
        <p class="muted small">确认后才会下发。每台机器下发前都会先用 xray run -test 校验配置；失败会自动保留旧配置。</p>
        <p class="muted small">路由规则优先通过 Xray 的路由 API 热添加；当前 Xray 版本不支持时，会改为重启 Xray（约 1 秒断流）。</p>
      </template>

      <template v-else>
        <h2>自检</h2>
        <ul class="checks">
          <li v-for="(c, i) in checks" :key="i"><span :class="['ck', c.st]">{{ c.st === 'ok' ? '✓' : c.st === 'bad' ? '✕' : '…' }}</span>{{ c.text }}</li>
        </ul>
        <div class="row" style="margin-top: 14px">
          <n-button :disabled="!view?.share_link" @click="copyLink">复制分享链接</n-button>
          <n-button :disabled="!view?.share_link" @click="qr = true">二维码</n-button>
        </div>
      </template>

      <div class="row actions">
        <template v-if="step < 5">
          <n-button @click="router.push({ name: 'routes' })">取消</n-button>
          <span class="spacer" />
          <n-button v-if="step > 1" @click="prev">上一步</n-button>
          <n-button v-if="step < 4" type="primary" :disabled="!canNext" @click="next">下一步</n-button>
          <n-button v-else type="primary" :loading="saving" @click="submit">{{ editId ? '保存并下发' : '确认并下发' }}</n-button>
        </template>
        <template v-else>
          <span class="spacer" />
          <n-button type="primary" @click="router.push({ name: 'routes' })">完成，返回线路</n-button>
        </template>
      </div>
    </div>
    <qr-modal v-model:show="qr" :text="view?.share_link || ''" :title="form.name" />
  </div>
</template>

<style scoped>
.wizard { max-width: 920px; }
.crumb a { color: var(--c-muted); }
.steps { margin: 4px 0; }
h2 { font-size: 17px; margin: 0 0 6px; }
h3 { font-size: 14px; margin: 20px 0 8px; }
.pick-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 10px; margin-top: 12px; }
.pick-grid.two { grid-template-columns: 1fr 1fr; }
.pick { text-align: left; font: inherit; color: inherit; background: #fff; border: 1px solid var(--c-border-strong); border-radius: 6px; padding: 12px; display: flex; flex-direction: column; gap: 4px; cursor: pointer; min-height: 44px; }
.pick:hover:not(:disabled) { border-color: var(--c-primary); }
.pick.on { border-color: var(--c-primary); background: var(--c-primary-soft); box-shadow: inset 0 0 0 1px var(--c-primary); }
.pick:disabled { opacity: .5; cursor: not-allowed; }
.flag { font-size: 18px; }
.bad-text { color: var(--c-danger); }
.col { display: flex; flex-direction: column; gap: 8px; }
.field { display: flex; flex-direction: column; gap: 6px; margin-top: 14px; }
.field.row { flex-direction: row; }
.field label { font-size: 13px; color: var(--c-text-2); }
.changes { margin: 0 0 12px; padding-left: 20px; line-height: 1.9; font-size: 13px; }
.checks { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 10px; font-size: 14px; }
.ck { display: inline-flex; width: 22px; height: 22px; border-radius: 50%; align-items: center; justify-content: center; margin-right: 10px; font-size: 12px; background: var(--c-surface); color: var(--c-muted); }
.ck.ok { background: var(--c-primary-soft); color: var(--c-primary); }
.ck.bad { background: var(--c-danger-soft); color: var(--c-danger); }
.actions { margin-top: 24px; padding-top: 16px; border-top: 1px solid var(--c-border); }
@media (max-width: 767px) { .pick-grid.two { grid-template-columns: 1fr; } }
</style>
