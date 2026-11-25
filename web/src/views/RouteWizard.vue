<script setup>
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NInput, NSteps, NStep, NSwitch } from 'naive-ui'
import { api } from '../api'
import { state, loadServers, serverName, ui } from '../store'
import { run, idsCsv } from '../helpers'
import { flag, copy } from '../format'
import { isMobile } from '../mobile'
import QrModal from '../components/QrModal.vue'

const route = useRoute()
const router = useRouter()
const editId = computed(() => (route.name === 'route-edit' ? Number(route.params.id) : 0))
const step = ref(1)
const stepNames = ['入口机', '线路类型', '名称', '预览变更', '自检']
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
    form.value = { entry_server_id: r.entry_server_id, type: r.type, landing_server_id: r.landing_server_id, backup_ids: idsCsv(r.backup_ids), name: r.name, tags: r.tags, in_sub: r.in_sub, enabled: r.enabled, sort: r.sort }
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
// Latest unlock check per landing (from the tools page), to help pick one.
const unlocks = ref({})
watch(landings, async (ls) => {
  for (const l of ls) {
    if (unlocks.value[l.id]) continue
    const r = await api.get(`/servers/${l.id}/tool-results`).catch(() => null)
    let items = []
    try { items = JSON.parse(r?.unlock?.data || '[]') } catch { /* ignore */ }
    unlocks.value = { ...unlocks.value, [l.id]: Array.isArray(items) ? items : [] }
  }
}, { immediate: true })
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
    if (form.value.backup_ids.length) out.push(`${e}：备用落地 ${form.value.backup_ids.map(serverName).join('、')}，由 Observatory 探活自动切换`)
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
  tries = 0
  pollDone.value = false
  pollStatus()
}
const view = ref(null)
const pollDone = ref(false)
let tries = 0
let pollTimer = null
let unmounted = false
onBeforeUnmount(() => { unmounted = true; clearTimeout(pollTimer) })
// Chain routes wait for the entry's first segment probe (pushed on save).
const segmentKnown = (v) => v.type !== 'chain' || v.segment_ms > 0 || v.segment_down
async function pollStatus() {
  if (unmounted) return
  const id = result.value?.route?.id
  if (!id) return
  const rs = await api.get('/routes').catch(() => [])
  if (unmounted) return
  view.value = rs.find((x) => x.id === id) || null
  const v = view.value
  const settled = v && v.share_link && (v.config_synced || v.deploy_error) && segmentKnown(v) && (v.status === 'ok' || v.segment_down)
  if (++tries < 24 && !settled) {
    clearTimeout(pollTimer)
    pollTimer = setTimeout(pollStatus, 5000)
  } else {
    pollDone.value = true
  }
}
function segmentCheck(v) {
  const base = '入口 → 落地线路段可达'
  if (!v) return { text: base, st: 'wait' }
  if (v.segment_down) return { text: `${base}：落地 SS 端口不通`, st: 'bad' }
  if (v.segment_ms > 0) return { text: `${base}（${Math.round(v.segment_ms)} ms）`, st: 'ok' }
  if (pollDone.value) return { text: `${base}：未收到入口的探测结果，稍后可在线路列表查看`, st: 'warn' }
  return { text: base, st: 'wait' }
}
const checks = computed(() => {
  const v = view.value
  const ok = (b) => (b ? 'ok' : 'wait')
  return [
    { text: '面板已保存线路', st: result.value ? 'ok' : 'wait' },
    {
      text: v?.deploy_error ? `配置下发失败：${v.deploy_error}` : '配置已下发并通过 xray run -test 校验',
      st: result.value?.warning || v?.deploy_error ? 'bad' : ok(v?.config_synced),
    },
    { text: `入口 ${v?.entry_name || ''} Xray 运行中`, st: v ? (v.xray_running ? 'ok' : 'bad') : 'wait' },
    ...(form.value.type === 'chain' ? [segmentCheck(v)] : []),
    { text: '分享链接已生成', st: ok(v?.share_link) },
  ]
})
async function copyLink() { if (view.value?.share_link && (await copy(view.value.share_link))) ui.message.success('已复制分享链接') }
</script>

<template>
  <div class="page wizard">
    <div class="crumb small muted"><router-link :to="{ name: 'routes' }">线路</router-link> / {{ editId ? '编辑线路' : '新建线路' }}</div>
    <div class="page-head"><div class="ph"><h1>{{ editId ? '编辑线路' : '新建线路' }}</h1><p class="page-desc">按步骤选择入口机、线路类型和名称，下发前会先预览变更并在完成后自检。</p></div></div>

    <n-steps v-if="!isMobile" :current="step" size="small" class="steps">
      <n-step title="入口机" />
      <n-step title="线路类型" />
      <n-step title="名称" />
      <n-step title="预览变更" />
      <n-step title="自检" />
    </n-steps>
    <div v-else class="step-mini">
      <div class="step-mini-text"><b>第 {{ step }} / {{ stepNames.length }} 步</b><span>{{ stepNames[step - 1] }}</span><span v-if="step < stepNames.length" class="muted">下一步：{{ stepNames[step] }}</span></div>
      <div class="step-mini-bar"><div :style="{ width: (step / stepNames.length * 100) + '%' }" /></div>
    </div>

    <div class="w-sections">
      <template v-if="step === 1">
        <section class="l-sec">
          <div class="sec-head"><div class="s-head"><h2>选择入口机</h2><p>客户端会连接这台机器的 Reality 端口。新线路只在它的 Reality 入站里新增一个 UUID，不会新开端口。</p></div></div>
          <div v-if="!entries.length" class="card s-card"><div class="empty">没有开启 Reality 的节点。先在总览里添加节点，或把已有机器升级为节点。</div></div>
          <div v-else class="pick-grid">
            <button v-for="s in entries" :key="s.id" type="button" :class="['pick', { on: form.entry_server_id === s.id }]" :aria-pressed="form.entry_server_id === s.id" @click="form.entry_server_id = s.id">
              <span class="row"><span class="flag">{{ flag(s.country_code) }}</span><b>{{ s.name }}</b><span class="spacer" /><span :class="['dot', s.blocked ? 'bad' : s.online ? 'ok' : 'off']" /></span>
              <span class="muted small">{{ s.region || '地区未知' }}</span>
              <span class="small">回程 {{ backhaul(s.id) ? Math.round(backhaul(s.id)) + ' ms' : '—' }}<span v-if="s.blocked" class="bad-text"> · 疑似被封</span></span>
            </button>
          </div>
        </section>
      </template>

      <template v-else-if="step === 2">
        <section class="l-sec">
          <div class="sec-head"><div class="s-head"><h2>线路类型</h2><p>直连用入口机的 IP 出网；链式经落地机转发，出口 IP 是落地机的。</p></div></div>
          <div class="pick-grid two">
            <button type="button" :class="['pick', { on: form.type === 'direct' }]" :aria-pressed="form.type === 'direct'" @click="form.type = 'direct'">
              <b>直连</b><span class="muted small">客户端 → {{ entrySv?.name }} → 目标网站。出口 IP 是入口机的 IP。</span>
            </button>
            <button type="button" :class="['pick', { on: form.type === 'chain' }]" :aria-pressed="form.type === 'chain'" :disabled="!landings.length" @click="form.type = 'chain'">
              <b>经落地（链式）</b><span class="muted small">{{ landings.length ? `客户端 → ${entrySv?.name} → 落地 → 目标网站。出口 IP 是落地机的 IP。` : '还没有落地机（开启 SS2022 落地能力的节点）。' }}</span>
            </button>
          </div>
        </section>
        <template v-if="form.type === 'chain'">
          <section class="l-sec">
            <div class="sec-head"><div class="s-head"><h2>落地机</h2><p>入口机到各落地的线路段延迟，以及最近一次流媒体和 AI 解锁检测结果。</p></div></div>
            <div class="pick-grid">
              <button v-for="l in landings" :key="l.id" type="button" :class="['pick', { on: form.landing_server_id === l.id }]" :aria-pressed="form.landing_server_id === l.id" @click="form.landing_server_id = l.id">
                <span class="row"><span class="flag">{{ flag(l.country_code) }}</span><b>{{ l.name }}</b><span class="spacer" /><span :class="['dot', l.online ? 'ok' : 'off']" /></span>
                <span class="muted small">{{ l.region || '地区未知' }}</span>
                <span class="small" v-if="segment(form.entry_server_id, l.id)">{{ segment(form.entry_server_id, l.id).src }} {{ Math.round(segment(form.entry_server_id, l.id).ms) }} ms · 丢包 {{ Math.round(segment(form.entry_server_id, l.id).loss) }}%</span>
                <span class="small muted" v-else>线路段延迟暂无数据</span>
                <span v-if="unlocks[l.id]?.length" class="unlock">
                  <span v-for="u in unlocks[l.id]" :key="u.name" :class="['tag', u.status === 'yes' ? 'ok' : u.status === 'partial' ? 'warn' : u.status === 'no' ? 'bad' : '']">{{ u.name }}</span>
                </span>
                <span v-else-if="unlocks[l.id]" class="small muted">还没做过解锁检测</span>
              </button>
            </div>
          </section>
          <section class="l-sec">
            <div class="sec-head"><div class="s-head"><h2>备用落地</h2><p>主落地不可达时，入口机的 Xray 会按 Observatory 探活结果自动切到可用的备用落地，并发送告警。</p></div></div>
            <div class="card s-card">
              <div v-if="landings.length < 2" class="empty">目前只有 1 台落地机。添加第二台落地后，可以在这里设置自动切换。</div>
              <div v-for="l in landings.filter((x) => x.id !== form.landing_server_id)" v-else :key="l.id" class="field f-bool">
                <div class="f-label">
                  <span class="f-name">{{ flag(l.country_code) }} {{ l.name }}</span>
                  <span class="f-hint">{{ segment(form.entry_server_id, l.id) ? '线路段 ' + Math.round(segment(form.entry_server_id, l.id).ms) + ' ms' : '线路段延迟暂无数据' }}</span>
                </div>
                <div class="f-ctl"><n-switch :value="form.backup_ids.includes(l.id)" :aria-label="'用 ' + l.name + ' 作备用'" @update:value="(v) => form.backup_ids = v ? [...form.backup_ids, l.id] : form.backup_ids.filter((b) => b !== l.id)" /></div>
              </div>
            </div>
          </section>
        </template>
      </template>

      <section v-else-if="step === 3" class="l-sec">
        <div class="sec-head"><div class="s-head"><h2>名称与选项</h2><p>这里的名称用于面板内识别；订阅里显示的名字按订阅设置里的命名模板生成。</p></div></div>
        <div class="card s-card">
          <div class="field">
            <div class="f-label"><span class="f-name">名称</span><span class="f-hint">默认按「入口 → 落地」生成，可以修改</span></div>
            <div class="f-ctl"><n-input v-model:value="form.name" maxlength="60" placeholder="" @update:value="nameTouched = true" /></div>
          </div>
          <div class="field">
            <div class="f-label"><span class="f-name">标签</span><span class="f-hint">可选，逗号分隔</span></div>
            <div class="f-ctl"><n-input v-model:value="form.tags" placeholder="" /></div>
          </div>
          <div class="field f-bool">
            <div class="f-label"><span class="f-name">加入订阅</span><span class="f-hint">关闭后线路照常下发，只是不出现在订阅里</span></div>
            <div class="f-ctl"><n-switch v-model:value="form.in_sub" aria-label="加入订阅" /></div>
          </div>
          <div v-if="editId" class="field f-bool">
            <div class="f-label"><span class="f-name">启用</span><span class="f-hint">停用后从入口机配置中移除</span></div>
            <div class="f-ctl"><n-switch v-model:value="form.enabled" aria-label="启用" /></div>
          </div>
        </div>
      </section>

      <template v-else-if="step === 4">
        <section class="l-sec">
          <div class="sec-head"><div class="s-head"><h2>预览变更</h2><p>确认后才会下发。每台机器下发前都会先用 xray run -test 校验配置，失败会自动保留旧配置。</p></div></div>
          <div class="card s-card">
            <div class="field f-inline">
              <div class="f-label"><span class="f-name">名称</span></div>
              <div class="f-ctl"><span class="f-val">{{ form.name }}</span></div>
            </div>
            <div class="field f-inline">
              <div class="f-label"><span class="f-name">路径</span></div>
              <div class="f-ctl"><span class="f-val">客户端 → {{ entrySv?.name }}{{ form.type === 'chain' ? ' → ' + landingSv?.name : '' }} → 互联网</span></div>
            </div>
            <div v-if="form.type === 'chain' && form.backup_ids.length" class="field f-inline">
              <div class="f-label"><span class="f-name">备用落地</span></div>
              <div class="f-ctl"><span class="f-val">{{ form.backup_ids.map(serverName).join('、') }}</span></div>
            </div>
            <div class="field f-inline">
              <div class="f-label"><span class="f-name">估算延迟</span><span class="f-hint">入口回程{{ form.type === 'chain' ? ' + 线路段' : '' }}</span></div>
              <div class="f-ctl"><span class="f-val mono">{{ estimate ? Math.round(estimate) + ' ms' : '—' }}</span></div>
            </div>
            <div class="field f-inline">
              <div class="f-label"><span class="f-name">订阅</span></div>
              <div class="f-ctl"><span :class="['tag', form.in_sub ? 'ok' : '']">{{ form.in_sub ? '加入订阅' : '不加入' }}</span></div>
            </div>
            <div class="field f-inline">
              <div class="f-label"><span class="f-name">同入口线路</span><span class="f-hint">这台入口上已有的线路</span></div>
              <div class="f-ctl"><span class="f-val">{{ sameEntryRoutes.length }} 条</span></div>
            </div>
          </div>
        </section>
        <section class="l-sec">
          <div class="sec-head"><div class="s-head"><h2>将要执行的变更</h2><p>路由规则优先通过 Xray 的路由 API 热添加；当前 Xray 版本不支持时，会改为重启 Xray（约 1 秒断流）。</p></div></div>
          <div class="card s-card">
            <div v-for="(c, i) in changes" :key="i" class="change"><span class="change-n">{{ i + 1 }}</span><span>{{ c }}</span></div>
          </div>
        </section>
      </template>

      <section v-else class="l-sec">
        <div class="sec-head"><div class="s-head"><h2>自检</h2><p>下发完成后逐项检查，结果会自动刷新。全部通过后就可以导入客户端使用了。</p></div></div>
        <div class="card s-card">
          <div v-for="(c, i) in checks" :key="i" class="field f-inline">
            <div class="f-label"><span class="f-name">{{ c.text }}</span></div>
            <div class="f-ctl"><span :class="['tag', { ok: 'ok', bad: 'bad', warn: 'warn' }[c.st]]"><span :class="['dot', { ok: 'ok', bad: 'bad', warn: 'warn' }[c.st] || 'off']" />{{ { ok: '通过', bad: '失败', warn: '未知' }[c.st] || '检查中' }}</span></div>
          </div>
          <div class="f-actions">
            <span class="f-hint">单条线路的分享链接，可以直接导入客户端</span>
            <span class="spacer" />
            <n-button :disabled="!view?.share_link" @click="qr = true">二维码</n-button>
            <n-button :disabled="!view?.share_link" @click="copyLink">复制分享链接</n-button>
          </div>
        </div>
      </section>
    </div>

    <div class="savebar w-bar">
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
    <qr-modal v-model:show="qr" :text="view?.share_link || ''" :title="form.name" />
  </div>
</template>

<style scoped>
.wizard { max-width: 960px; }
.crumb a { color: var(--c-muted); }
.steps { margin: 4px 0; }
.w-sections { display: flex; flex-direction: column; gap: 28px; }
.pick-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(210px, 1fr)); gap: 10px; }
.pick-grid.two { grid-template-columns: 1fr 1fr; }
.pick { text-align: left; font: inherit; color: inherit; background: #fff; border: 1px solid var(--c-border); border-radius: 6px; padding: 14px 16px; display: flex; flex-direction: column; gap: 4px; cursor: pointer; min-height: 44px; }
.pick:hover:not(:disabled) { border-color: var(--c-primary); }
.pick.on { border-color: var(--c-primary); background: var(--c-primary-soft); box-shadow: inset 0 0 0 1px var(--c-primary); }
.pick:disabled { opacity: .5; cursor: not-allowed; }
.flag { font-size: 18px; }
.bad-text { color: var(--c-danger); }
.f-val { font-size: 13.5px; text-align: right; overflow-wrap: anywhere; }
.f-ctl .tag { gap: 6px; }
.change { display: flex; gap: 12px; align-items: baseline; padding: 12px 0; border-top: 1px solid var(--c-border); font-size: 13.5px; line-height: 1.6; }
.change:first-child { border-top: 0; }
.change-n { flex: none; width: 20px; height: 20px; border-radius: 50%; background: var(--c-surface); color: var(--c-muted); font-size: 11.5px; font-weight: 600; display: inline-flex; align-items: center; justify-content: center; }
.unlock { display: flex; flex-wrap: wrap; gap: 4px; margin-top: 2px; }
.unlock .tag { font-size: 11px; }
.w-bar { max-width: none; margin-top: 8px; }
.step-mini { display: flex; flex-direction: column; gap: 8px; }
.step-mini-text { display: flex; gap: 8px; align-items: baseline; flex-wrap: wrap; font-size: 13px; }
.step-mini-bar { height: 4px; border-radius: 2px; background: var(--c-surface); overflow: hidden; }
.step-mini-bar div { height: 100%; background: var(--c-primary); border-radius: 2px; transition: width .2s; }
@media (max-width: 767px) { .pick-grid.two { grid-template-columns: 1fr; } .w-sections { gap: 22px; } }
</style>
