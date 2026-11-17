<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NTabs, NTabPane, NInput, NInputNumber, NSwitch, NSelect, NCheckboxGroup, NCheckbox, NModal, NUpload } from 'naive-ui'
import QRCode from 'qrcode'
import { api } from '../api'
import { state, loadMe, ui } from '../store'
import { run, ask } from '../helpers'
import { dt, ago, bytes, copy } from '../format'

const route = useRoute()
const router = useRouter()
const tab = ref(route.query.tab || 'account')
watch(tab, (t) => { router.replace({ query: { tab: t } }); if (t === 'backup') loadBackups(); if (t === 'audit') loadAudit(true) })

// ---------- account ----------
const pw = ref({ old: '', new: '', again: '' })
async function changePw() {
  const p = pw.value
  if (p.new.length < 10) { ui.message.warning('新密码至少 10 位'); return }
  if (p.new !== p.again) { ui.message.warning('两次输入的新密码不一致'); return }
  if (await run(() => api.post('/account/password', { old: p.old, new: p.new }), '密码已修改，其他会话已退出')) { pw.value = { old: '', new: '', again: '' }; loadSessions() }
}
const totp = ref({ step: '', secret: '', qr: '', code: '', codes: [] })
async function beginTotp() {
  const r = await run(() => api.post('/account/totp/begin'))
  if (!r) return
  totp.value = { step: 'scan', secret: r.secret, qr: await QRCode.toDataURL(r.uri, { margin: 1, width: 200 }), code: '', codes: [] }
}
async function confirmTotp() {
  const r = await run(() => api.post('/account/totp/confirm', { code: totp.value.code }), '两步验证已开启')
  if (r) { totp.value = { step: 'codes', codes: r.recovery_codes }; loadMe() }
}
const disableCode = ref('')
async function disableTotp() {
  if (!(await ask('关闭两步验证', '关闭后只凭密码即可登录，敏感操作改为验证密码。', { danger: true }))) return
  if (await run(() => api.post('/account/totp/disable', { code: disableCode.value }), '已关闭')) { disableCode.value = ''; loadMe() }
}
async function regenCodes() {
  if (!(await ask('重新生成恢复码', '旧的恢复码会全部作废。'))) return
  const r = await run(() => api.post('/account/totp/recovery'))
  if (r) { totp.value = { step: 'codes', codes: r.recovery_codes }; loadMe() }
}
const sessions = ref([])
async function loadSessions() { sessions.value = (await api.get('/account/sessions').catch(() => [])) || [] }
async function kick(s) { if (await run(() => api.del(`/account/sessions/${s.id}`), '已踢下线')) loadSessions() }
function uaShort(ua) {
  if (!ua) return '—'
  const b = /Edg\//.test(ua) ? 'Edge' : /Chrome\//.test(ua) ? 'Chrome' : /Firefox\//.test(ua) ? 'Firefox' : /Safari\//.test(ua) ? 'Safari' : ua.slice(0, 24)
  const o = /iPhone|iPad/.test(ua) ? 'iOS' : /Android/.test(ua) ? 'Android' : /Mac OS/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : ''
  return o ? `${b} · ${o}` : b
}

// ---------- settings ----------
const cfg = ref({})
const orig = ref({})
async function loadSettings() { const s = (await api.get('/settings').catch(() => ({}))) || {}; cfg.value = { ...s }; orig.value = { ...s } }
const B = (k, label, def, hint) => ({ k, label, type: 'bool', def, hint })
const N = (k, label, def, hint, unit) => ({ k, label, type: 'num', def, hint, unit })
const S = (k, label, def, hint) => ({ k, label, type: 'str', def, hint })
const opts = (...xs) => xs.map(([value, label]) => ({ value, label }))
const groups = {
  panel: [
    { title: '访问', desc: 'Agent、安装命令和订阅链接使用的地址，以及谁能打开面板。', fields: [
      S('panel_url', '面板地址', '', 'Agent 连接和安装命令里使用，例如 https://panel.example.com'),
      S('panel_urls_backup', '备用面板地址', '', '逗号分隔；主地址不可达时 Agent 依次尝试'),
      S('sub_url', '订阅地址', '', '留空 = 用面板地址；可填 CDN 或另一个域名'),
      S('admin_path', '后台路径', '', '例如 /x7k2，留空 = 根路径；改后旧路径返回 404'),
      S('allow_ips', '允许访问的 IP', '', '逗号分隔，支持 CIDR；留空 = 不限制。订阅与 Agent 连接不受影响'),
    ] },
    { title: '费用', desc: '服务器月费按所选币种换算后汇总。', fields: [
      { k: 'currency', label: '统计币种', type: 'select', def: 'USD', options: opts(['USD', 'USD 美元'], ['CNY', 'CNY 人民币'], ['EUR', 'EUR 欧元'], ['HKD', 'HKD 港币'], ['JPY', 'JPY 日元']) },
      { k: 'fx_rates', label: '汇率', type: 'fx', def: '', hint: '1 USD 可兑换的数量；留空的币种用默认汇率' },
    ] },
    { title: '流量突增', desc: '「流量异常突增」告警的判定条件。告警开关与通知渠道在「告警」页设置。', fields: [
      N('traffic_spike_factor', '突增倍数', 5, '某小时流量超过近 7 天同一时段均值的这个倍数时告警', '倍'),
      N('traffic_spike_min_gb', '最小流量', 2, '这一小时的流量低于此值时不算突增', 'GB'),
    ] },
    { title: '更新', desc: '检查面板新版本的来源，以及 Agent 升级包的签名校验。', fields: [
      S('panel_repo', '面板发布仓库', '', 'GitHub 的 owner/repo，用于检查面板新版本'),
      { k: 'sign_pubkey', label: 'Agent 签名公钥', type: 'text', def: '', hint: 'Ed25519 公钥（base64）。设置后 Agent 只接受签名正确的升级包' },
    ] },
    { title: '公开状态页', desc: '不用登录的只读页面，可以分享给别人。', link: 'status', fields: [
      B('status_enabled', '开启', false, '任何人都能打开 /status 查看；页面不显示 IP、服务商、版本，也不链接到面板'),
      S('status_title', '标题', '服务状态', ''),
      { k: 'status_servers', label: '显示的服务器', type: 'servers', def: '', hint: '不选 = 全部' },
      B('status_show_latency', '显示三网回程延迟', true, ''),
    ] },
  ],
  agent: [
    { title: 'Agent', desc: 'Agent 的上报频率与离线判定。', fields: [
      N('agent_report_interval', '上报间隔', 10, '', '秒'),
      N('offline_after_sec', '离线判定', 90, '超过此时间没有心跳视为离线', '秒'),
    ] },
    { title: '回程探测', desc: '每台 VPS 到三网目标的延迟与丢包探测。', fields: [
      N('probe_interval', '探测间隔', 60, '每台 VPS 对每个目标的探测周期', '秒'),
      N('probe_count', '每轮包数', 5, '', '个'),
      N('block_rounds', '判定被封', 3, '连续几轮三网全丢包才标记为疑似被封', '轮'),
      B('probe_peer', 'VPS 互测', false, '每台 VPS 互相 ping，用于新建线路时估算入口→落地延迟'),
    ] },
  ],
  xray: [
    { title: '部署', desc: '节点机上 Xray 的安装、版本与配置管理。', fields: [
      B('auto_install_xray', '自动安装 Xray', true, '节点机接入后自动安装并下发配置'),
      B('drift_autofix', '配置漂移自动修复', true, '发现 VPS 上的配置被改动时，自动恢复为面板版本'),
      S('xray_pinned_version', '默认 Xray 版本', 'latest', 'latest 或具体版本号；单台机器可另行锁定'),
      B('xray_relay', '通过面板中转下载', false, 'VPS 访问 GitHub 困难时开启'),
      { k: 'geo_source', label: '规则文件来源', type: 'select', def: 'loyalsoldier', hint: 'geoip / geosite 规则文件的下载来源', options: opts(['loyalsoldier', 'Loyalsoldier'], ['v2fly', 'v2fly'], ['metacubex', 'MetaCubeX']) },
    ] },
    { title: '入站与安全', desc: '新建入口的默认值，以及落地机和出站的防护。', fields: [
      S('reality_default_target', 'Reality 默认目标', 'www.microsoft.com', '新建入口时的默认 dest / serverName'),
      B('landing_firewall', '落地机防火墙', true, '落地端口只放行入口机 IP'),
      B('xray_block_bt', '屏蔽 BT', true, '拦截 BitTorrent 流量'),
      B('xray_access_log', '访问日志', false, '默认关闭，只保留错误日志'),
      S('xray_probe_url', '线路自检地址', 'https://www.gstatic.com/generate_204', '线路自检时访问的地址'),
    ] },
  ],
  retention: [
    { title: '保留时长', desc: '超过保留时长的历史数据会被自动清理。', fields: [
      N('retention_metrics_1m_days', '分钟级指标', 7, '', '天'),
      N('retention_metrics_1h_days', '小时级指标', 90, '', '天'),
      N('retention_probe_raw_days', '探测原始数据', 3, '', '天'),
      N('retention_probe_1h_days', '探测小时聚合', 90, '', '天'),
      N('config_versions_keep', '配置历史', 50, '每台机器保留的版本数', '个'),
    ] },
  ],
}
const dirtyKeys = computed(() => Object.keys(cfg.value).filter((k) => cfg.value[k] !== orig.value[k]))
function boolVal(f) { const v = cfg.value[f.k]; return v === '' || v == null ? f.def : v === '1' || v === 'true' }
function numVal(f) { const v = cfg.value[f.k]; return v === '' || v == null ? null : Number(v) }
function setVal(k, v) { cfg.value = { ...cfg.value, [k]: v == null ? '' : typeof v === 'boolean' ? (v ? '1' : '0') : String(v) } }
function isDirty(k) { return (cfg.value[k] ?? '') !== (orig.value[k] ?? '') }
function placeholder(f) { return f.def !== '' && f.def != null ? `默认 ${f.def}` : '未设置' }
// fx_rates is stored as JSON; the panel replaces its defaults wholesale, so a
// saved value always carries every currency.
const fxDefault = { CNY: 7.2, EUR: 0.92, HKD: 7.8, JPY: 150 }
function fxStored() { try { const o = JSON.parse(cfg.value.fx_rates || '{}'); return o && typeof o === 'object' ? o : {} } catch { return {} } }
function fxVal(c) { const v = fxStored()[c]; return v == null || Number(v) === fxDefault[c] ? null : Number(v) }
function setFx(c, v) {
  const o = { ...fxDefault, ...fxStored() }
  o[c] = v == null ? fxDefault[c] : v
  setVal('fx_rates', Object.keys(fxDefault).every((k) => o[k] === fxDefault[k]) && Object.keys(o).length === 4 ? '' : JSON.stringify(o))
}
async function saveSettings(keys) {
  const body = {}
  for (const k of keys || dirtyKeys.value) body[k] = cfg.value[k]
  if (!Object.keys(body).length) { ui.message.info('没有修改'); return }
  if (body.fx_rates) { try { JSON.parse(body.fx_rates) } catch { ui.message.error('汇率不是合法的 JSON'); return } }
  if (await run(() => api.put('/settings', body), '已保存')) loadSettings()
}
const settingsTabs = [{ name: 'panel', label: '面板' }, { name: 'agent', label: 'Agent 与探测' }, { name: 'xray', label: 'Xray' }, { name: 'retention', label: '数据保留' }]

// ---------- backups ----------
const bk = ref({ items: [], last: '', last_error: '', encrypted: false })
const tgChannels = ref([])
async function loadBackups() {
  bk.value = (await api.get('/backups').catch(() => null)) || bk.value
  tgChannels.value = ((await api.get('/channels').catch(() => [])) || []).filter((c) => c.type === 'telegram')
}
const targets = computed({
  get: () => (cfg.value.backup_target || '').split(',').filter(Boolean),
  set: (v) => setVal('backup_target', v.join(',')),
})
const backupKeys = ['backup_enabled', 'backup_time', 'backup_keep', 'backup_target', 'backup_password', 'backup_s3_endpoint', 'backup_s3_bucket', 'backup_s3_region', 'backup_s3_access_key', 'backup_s3_secret', 'backup_s3_prefix', 'backup_webdav_url', 'backup_webdav_user', 'backup_webdav_password', 'backup_tg_channel']
const backupDirty = computed(() => backupKeys.some((k) => cfg.value[k] !== orig.value[k]))
async function saveBackupSettings() { await saveSettings(backupKeys.filter((k) => cfg.value[k] !== orig.value[k])); loadBackups() }
const backing = ref(false)
async function backupNow() {
  backing.value = true
  const r = await run(() => api.post('/backups'))
  backing.value = false
  if (r) { r.warning ? ui.message.warning('本地备份已完成，远程上传失败：' + r.warning) : ui.message.success('备份完成：' + r.name); loadBackups() }
}
async function delBackup(b) {
  if (!(await ask('删除备份', `删除 ${b.name}？只删除面板本地的副本。`, { danger: true }))) return
  if (await run(() => api.del(`/backups/${encodeURIComponent(b.name)}`), '已删除')) loadBackups()
}
const restore = ref({ show: false, name: '', file: null, password: '' })
function openRestore(b) { restore.value = { show: true, name: b?.name || '', file: null, password: '' } }
async function doRestore() {
  const r = restore.value
  if (!r.name && !r.file) { ui.message.warning('选择备份文件'); return }
  if (!(await ask('恢复备份', '当前数据会被备份文件覆盖（恢复前自动再备份一次），面板随后重启，所有会话需要重新登录。', { danger: true, okText: '恢复' }))) return
  const fd = new FormData()
  if (r.name) fd.append('name', r.name); else fd.append('file', r.file)
  if (r.password) fd.append('password', r.password)
  const res = await run(() => api.post('/backups/restore', fd, { confirm: '恢复备份需要确认身份' }))
  if (res) {
    restore.value.show = false
    ui.message.loading('面板重启中…', { duration: 0 })
    setTimeout(() => location.reload(), 6000)
  }
}

// ---------- audit ----------
const audit = ref({ type: 'all', items: [], offset: 0, more: false })
const auditTypes = [{ label: '全部', value: 'all' }, { label: '登录', value: 'login' }, { label: '服务器', value: 'server' }, { label: '线路', value: 'route' }, { label: '订阅', value: 'sub' }, { label: '告警', value: 'alert' }, { label: '系统', value: 'system' }]
const typeLabel = Object.fromEntries(auditTypes.map((t) => [t.value, t.label]))
async function loadAudit(reset) {
  if (reset) audit.value.offset = 0
  const a = audit.value
  const rows = (await api.get(`/audit?type=${a.type}&limit=100&offset=${a.offset}`).catch(() => [])) || []
  audit.value.items = reset ? rows : [...a.items, ...rows]
  audit.value.more = rows.length === 100
  audit.value.offset += rows.length
}
watch(() => audit.value.type, () => loadAudit(true))

onMounted(() => {
  loadSettings(); loadSessions()
  if (tab.value === 'backup') loadBackups()
  if (tab.value === 'audit') loadAudit(true)
})
</script>

<template>
  <div class="page">
    <div class="page-head"><h1>设置</h1></div>
    <n-tabs v-model:value="tab" type="line" animated>
      <n-tab-pane name="account" tab="账号与安全">
        <div class="grid-2">
          <div class="card">
            <div class="card-title"><h2>登录密码</h2><span class="muted small">{{ state.me?.username }}<template v-if="state.me?.password_at && !state.me.password_at.startsWith('0001')"> · {{ ago(state.me.password_at) }}修改</template></span></div>
            <div class="form">
              <n-input v-model:value="pw.old" type="password" show-password-on="click" placeholder="当前密码" />
              <n-input v-model:value="pw.new" type="password" show-password-on="click" placeholder="新密码（至少 10 位）" />
              <n-input v-model:value="pw.again" type="password" show-password-on="click" placeholder="再输一次" @keyup.enter="changePw" />
              <div><n-button type="primary" @click="changePw">修改密码</n-button></div>
              <p class="small muted">修改后其他设备上的会话全部退出。忘记密码时在面板机器上执行 <code class="mono">vps-panel reset-admin</code>。</p>
            </div>
          </div>
          <div class="card">
            <div class="card-title"><h2>两步验证</h2><span :class="['tag', state.me?.totp ? 'ok' : 'warn']">{{ state.me?.totp ? '已开启' : '未开启' }}</span></div>
            <template v-if="totp.step === 'scan'">
              <p class="small">用 Google Authenticator、1Password 等扫描二维码，然后输入 6 位验证码。</p>
              <div class="row" style="align-items: flex-start">
                <img :src="totp.qr" alt="两步验证二维码" width="160" height="160" />
                <div class="form" style="flex: 1; min-width: 180px">
                  <span class="small muted">无法扫码时手动输入：</span>
                  <code class="mono small secret" @click="copy(totp.secret)">{{ totp.secret }}</code>
                  <n-input v-model:value="totp.code" placeholder="6 位验证码" maxlength="6" @keyup.enter="confirmTotp" />
                  <div class="row"><n-button type="primary" @click="confirmTotp">确认开启</n-button><n-button @click="totp.step = ''">取消</n-button></div>
                </div>
              </div>
            </template>
            <template v-else-if="totp.step === 'codes'">
              <div class="banner warn">恢复码只显示这一次。手机丢失时可用其中任意一个登录，每个只能用一次。</div>
              <div class="codes mono">
                <span v-for="c in totp.codes" :key="c">{{ c }}</span>
              </div>
              <div class="row"><n-button size="small" @click="copy(totp.codes.join('\n'))">复制全部</n-button><n-button size="small" type="primary" @click="totp.step = ''">我已保存</n-button></div>
            </template>
            <template v-else-if="state.me?.totp">
              <p class="small">登录与敏感操作（删除服务器、执行脚本、打开终端、恢复备份等）都需要验证码。</p>
              <p class="small muted">剩余恢复码：{{ state.me.recovery_left }} 个</p>
              <div class="row">
                <n-button size="small" @click="regenCodes">重新生成恢复码</n-button>
                <span class="spacer" />
                <n-input v-model:value="disableCode" size="small" placeholder="验证码" style="width: 110px" maxlength="6" />
                <n-button size="small" type="error" ghost :disabled="disableCode.length < 6" @click="disableTotp">关闭</n-button>
              </div>
            </template>
            <template v-else>
              <p class="small">面板能控制所有 VPS，强烈建议开启。开启后登录与敏感操作都要输入验证码。</p>
              <n-button type="primary" @click="beginTotp">开启两步验证</n-button>
            </template>
          </div>
        </div>
        <div class="card" style="margin-top: 16px">
          <div class="card-title"><h2>登录会话</h2></div>
          <div class="table-wrap hide-mobile">
            <table class="plain">
              <thead><tr><th>设备</th><th>IP</th><th>登录时间</th><th>最近活动</th><th /></tr></thead>
              <tbody>
                <tr v-for="s in sessions" :key="s.id">
                  <td :title="s.ua">{{ uaShort(s.ua) }} <span v-if="s.current" class="tag ok">当前</span></td>
                  <td class="mono">{{ s.ip }}</td><td class="nowrap">{{ dt(s.created_at) }}</td><td class="nowrap">{{ ago(s.last_seen) }}</td>
                  <td style="text-align: right"><n-button v-if="!s.current" size="tiny" quaternary type="error" @click="kick(s)">踢下线</n-button></td>
                </tr>
              </tbody>
            </table>
          </div>
          <div class="m-list only-mobile">
            <div v-for="s in sessions" :key="s.id" class="m-item">
              <div class="m-row"><b>{{ uaShort(s.ua) }}</b><span v-if="s.current" class="tag ok">当前</span><span class="spacer" /><n-button v-if="!s.current" size="small" quaternary type="error" @click="kick(s)">踢下线</n-button></div>
              <div class="m-row small muted"><span class="mono">{{ s.ip }}</span><span>· {{ ago(s.last_seen) }}</span></div>
            </div>
          </div>
        </div>
      </n-tab-pane>

      <n-tab-pane v-for="t in settingsTabs" :key="t.name" :name="t.name" :tab="t.label">
        <div class="settings">
          <section v-for="g in groups[t.name]" :key="g.title" class="s-group">
            <div class="s-head">
              <h2>{{ g.title }}</h2>
              <p v-if="g.desc">{{ g.desc }}</p>
              <a v-if="g.link === 'status' && orig.status_enabled === '1'" class="small" href="/status" target="_blank" rel="noopener">打开状态页</a>
            </div>
            <div class="card s-card">
              <div v-for="f in g.fields" :key="f.k" :class="['field', 'f-' + f.type, { stacked: f.type === 'text' || f.type === 'fx', dirty: isDirty(f.k) }]">
                <div class="f-label">
                  <span class="f-name">{{ f.label }}<i v-if="isDirty(f.k)" class="f-dot" title="未保存" /></span>
                  <span v-if="f.hint" class="f-hint">{{ f.hint }}</span>
                </div>
                <div class="f-ctl">
                  <n-switch v-if="f.type === 'bool'" :value="boolVal(f)" :aria-label="f.label" @update:value="(v) => setVal(f.k, v)" />
                  <n-input-number v-else-if="f.type === 'num'" :value="numVal(f)" :placeholder="placeholder(f)" :min="0" :show-button="false" clearable @update:value="(v) => setVal(f.k, v)">
                    <template v-if="f.unit" #suffix><span class="f-unit">{{ f.unit }}</span></template>
                  </n-input-number>
                  <n-select v-else-if="f.type === 'select'" :value="cfg[f.k] || f.def" :options="f.options" @update:value="(v) => setVal(f.k, v)" />
                  <n-select v-else-if="f.type === 'servers'" multiple clearable placeholder="全部服务器" max-tag-count="responsive"
                    :value="(cfg[f.k] || '').split(',').filter((x) => x && x !== 'all').map(Number)"
                    :options="state.servers.map((s) => ({ label: s.name, value: s.id }))"
                    @update:value="(v) => setVal(f.k, v.join(','))" />
                  <div v-else-if="f.type === 'fx'" class="fx">
                    <n-input-number v-for="(d, c) in fxDefault" :key="c" :value="fxVal(c)" :placeholder="String(d)" :min="0" :show-button="false" clearable @update:value="(v) => setFx(c, v)">
                      <template #prefix><span class="f-unit fx-code">{{ c }}</span></template>
                    </n-input-number>
                  </div>
                  <n-input v-else-if="f.type === 'text'" :value="cfg[f.k]" type="textarea" :autosize="{ minRows: 2, maxRows: 4 }" class="mono" :placeholder="placeholder(f)" @update:value="(v) => setVal(f.k, v)" />
                  <n-input v-else :value="cfg[f.k]" :placeholder="placeholder(f)" clearable @update:value="(v) => setVal(f.k, v)" />
                </div>
              </div>
            </div>
          </section>
        </div>
        <div class="savebar">
          <span v-if="dirtyKeys.length" class="small dirty-note"><i class="f-dot" />{{ dirtyKeys.length }} 项未保存</span>
          <span v-else class="small muted">留空的项使用默认值</span>
          <span class="spacer" />
          <n-button :disabled="!dirtyKeys.length" @click="cfg = { ...orig }">撤销</n-button>
          <n-button type="primary" :disabled="!dirtyKeys.length" @click="saveSettings()">保存</n-button>
        </div>
      </n-tab-pane>

      <n-tab-pane name="backup" tab="备份">
        <div class="grid-2">
          <div class="card">
            <div class="card-title">
              <h2>备份文件</h2><span class="spacer" />
              <n-button size="small" @click="openRestore(null)">上传并恢复</n-button>
              <n-button size="small" type="primary" :loading="backing" @click="backupNow">立即备份</n-button>
            </div>
            <div v-if="bk.last_error" class="banner warn">上次远程备份失败：{{ bk.last_error }}</div>
            <div v-if="!bk.encrypted" class="banner info">未设置备份密码：备份里的密钥是明文，建议在右侧设置。</div>
            <div v-if="!bk.items?.length" class="empty">还没有备份</div>
            <div v-for="b in bk.items" :key="b.name" class="b-item">
              <div class="b-main">
                <span class="mono small">{{ b.name }}</span>
                <span class="small muted">{{ dt(b.at) }} · {{ bytes(b.size) }}{{ b.encrypted ? ' · 已加密' : '' }}</span>
              </div>
              <div class="row b-act">
                <a class="link-btn" :href="`/api/backups/${encodeURIComponent(b.name)}`" download>下载</a>
                <n-button size="tiny" quaternary @click="openRestore(b)">恢复</n-button>
                <n-button size="tiny" quaternary type="error" @click="delBackup(b)">删除</n-button>
              </div>
            </div>
          </div>
          <div class="card">
            <div class="card-title"><h2>自动备份</h2><span class="spacer" /><n-switch :value="boolVal({ k: 'backup_enabled', def: true })" @update:value="(v) => setVal('backup_enabled', v)" /></div>
            <div class="form">
              <div class="row">
                <span class="small">每天</span><n-input :value="cfg.backup_time" placeholder="03:30" style="width: 90px" @update:value="(v) => setVal('backup_time', v)" />
                <span class="small">保留</span><n-input-number :value="numVal({ k: 'backup_keep' })" placeholder="7" :min="1" style="width: 100px" @update:value="(v) => setVal('backup_keep', v)" /><span class="small muted">份</span>
              </div>
              <label>备份密码</label>
              <n-input :value="cfg.backup_password" type="password" show-password-on="click" placeholder="留空 = 不加密" @update:value="(v) => setVal('backup_password', v)" />
              <span class="small muted">恢复时需要这个密码，忘了就无法从加密备份恢复。</span>
              <label>同时上传到</label>
              <n-checkbox-group v-model:value="targets">
                <div class="row"><n-checkbox value="s3">S3 / R2</n-checkbox><n-checkbox value="webdav">WebDAV</n-checkbox><n-checkbox value="telegram">Telegram</n-checkbox></div>
              </n-checkbox-group>
              <template v-if="targets.includes('s3')">
                <label>S3 兼容存储</label>
                <n-input :value="cfg.backup_s3_endpoint" placeholder="Endpoint，如 https://xxx.r2.cloudflarestorage.com" @update:value="(v) => setVal('backup_s3_endpoint', v)" />
                <div class="row">
                  <n-input :value="cfg.backup_s3_bucket" placeholder="Bucket" style="flex: 1" @update:value="(v) => setVal('backup_s3_bucket', v)" />
                  <n-input :value="cfg.backup_s3_region" placeholder="Region（auto）" style="width: 130px" @update:value="(v) => setVal('backup_s3_region', v)" />
                </div>
                <n-input :value="cfg.backup_s3_access_key" placeholder="Access Key" @update:value="(v) => setVal('backup_s3_access_key', v)" />
                <n-input :value="cfg.backup_s3_secret" type="password" show-password-on="click" placeholder="Secret Key" @update:value="(v) => setVal('backup_s3_secret', v)" />
                <n-input :value="cfg.backup_s3_prefix" placeholder="路径前缀（vps-panel）" @update:value="(v) => setVal('backup_s3_prefix', v)" />
              </template>
              <template v-if="targets.includes('webdav')">
                <label>WebDAV</label>
                <n-input :value="cfg.backup_webdav_url" placeholder="https://dav.example.com/backup/" @update:value="(v) => setVal('backup_webdav_url', v)" />
                <div class="row">
                  <n-input :value="cfg.backup_webdav_user" placeholder="用户名" style="flex: 1" @update:value="(v) => setVal('backup_webdav_user', v)" />
                  <n-input :value="cfg.backup_webdav_password" type="password" show-password-on="click" placeholder="密码" style="flex: 1" @update:value="(v) => setVal('backup_webdav_password', v)" />
                </div>
              </template>
              <template v-if="targets.includes('telegram')">
                <label>Telegram</label>
                <n-select :value="cfg.backup_tg_channel || ''" :options="[{ label: '第一个 Telegram 渠道', value: '' }, ...tgChannels.map((c) => ({ label: c.name, value: String(c.id) }))]" @update:value="(v) => setVal('backup_tg_channel', v)" />
                <span class="small muted">用告警渠道里配置的 Bot 和 Chat 发送备份文件。</span>
              </template>
              <div class="row" style="margin-top: 8px">
                <span class="small muted">{{ bk.last ? '上次备份 ' + ago(bk.last) : '' }}</span><span class="spacer" />
                <n-button type="primary" :disabled="!backupDirty" @click="saveBackupSettings">保存</n-button>
              </div>
            </div>
          </div>
        </div>
      </n-tab-pane>

      <n-tab-pane name="audit" tab="审计日志">
        <div class="card">
          <div class="card-title">
            <n-select v-model:value="audit.type" :options="auditTypes" size="small" style="width: 120px" />
            <span class="spacer" />
            <a class="link-btn" :href="`/api/audit?format=csv&type=${audit.type}`" download>导出 CSV</a>
          </div>
          <div v-if="!audit.items.length" class="empty">暂无记录</div>
          <div v-else class="table-wrap">
            <table class="plain">
              <thead><tr><th>时间</th><th>类别</th><th>操作</th><th>对象</th><th>详情</th><th>IP</th></tr></thead>
              <tbody>
                <tr v-for="l in audit.items" :key="l.id">
                  <td class="nowrap">{{ dt(l.ts) }}</td><td class="nowrap">{{ typeLabel[l.type] || l.type }}</td><td class="mono small">{{ l.action }}</td>
                  <td class="small">{{ l.target }}</td><td class="small muted">{{ l.detail }}</td><td class="mono small">{{ l.ip }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-if="audit.more" style="text-align: center; margin-top: 10px"><n-button size="small" @click="loadAudit(false)">加载更多</n-button></div>
        </div>
      </n-tab-pane>
    </n-tabs>

    <n-modal v-model:show="restore.show" preset="card" title="恢复备份" style="width: min(460px, 96vw)">
      <div class="form">
        <template v-if="restore.name"><span class="small">从 <code class="mono">{{ restore.name }}</code> 恢复</span></template>
        <template v-else>
          <n-upload :max="1" :default-upload="false" accept=".vpb,.gz" @change="({ fileList }) => (restore.file = fileList[0]?.file || null)">
            <n-button>选择备份文件</n-button>
          </n-upload>
        </template>
        <n-input v-model:value="restore.password" type="password" show-password-on="click" placeholder="备份密码（加密备份需要）" />
        <p class="small muted">恢复会覆盖数据库与密钥，面板随后自动重启。</p>
      </div>
      <template #footer><div class="row" style="justify-content: flex-end"><n-button @click="restore.show = false">取消</n-button><n-button type="error" @click="doRestore">恢复</n-button></div></template>
    </n-modal>
  </div>
</template>

<style scoped>
.form { display: flex; flex-direction: column; gap: 8px; }
.form label { font-size: 13px; color: var(--c-text-2); margin-top: 6px; }
.secret { cursor: pointer; word-break: break-all; background: var(--c-surface); padding: 6px 8px; border-radius: 4px; }
.codes { display: grid; grid-template-columns: repeat(2, 1fr); gap: 6px 16px; padding: 12px; background: var(--c-surface); border-radius: 6px; margin: 10px 0; font-size: 14px; }
.settings { display: flex; flex-direction: column; gap: 28px; max-width: 1120px; padding-top: 4px; }
.s-group { display: grid; grid-template-columns: 220px minmax(0, 1fr); gap: 32px; align-items: start; }
.s-head { padding-top: 14px; display: flex; flex-direction: column; gap: 6px; }
.s-head h2 { margin: 0; font-size: 15px; font-weight: 600; }
.s-head p { margin: 0; font-size: 13px; line-height: 1.6; color: var(--c-muted); }
.s-card { padding: 2px 20px; }
.field { display: grid; grid-template-columns: minmax(0, 1fr) 300px; gap: 10px 32px; padding: 14px 0; border-top: 1px solid var(--c-border); align-items: center; }
.field:first-child { border-top: 0; }
.field.stacked { grid-template-columns: minmax(0, 1fr); }
.f-label { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.f-name { font-size: 14px; color: var(--c-text); }
.f-hint { font-size: 12.5px; line-height: 1.55; color: var(--c-muted); overflow-wrap: anywhere; }
.f-dot { display: inline-block; width: 6px; height: 6px; border-radius: 50%; background: var(--c-primary); margin-left: 6px; vertical-align: 2px; }
.f-ctl { min-width: 0; }
.f-ctl > * { width: 100%; }
.f-bool .f-ctl { justify-self: end; }
.f-bool .f-ctl > * { width: auto; }
.f-unit { color: var(--c-muted); font-size: 13px; }
.fx { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 8px; }
.fx-code { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 12px; margin-right: 4px; }
.savebar { position: sticky; bottom: 0; z-index: 2; display: flex; align-items: center; gap: 8px; max-width: 1120px; padding: 12px 0; margin-top: 4px; background: var(--c-bg, #f7f8fa); border-top: 1px solid var(--c-border); }
.dirty-note { color: var(--c-primary); font-weight: 500; }
.dirty-note .f-dot { margin: 0 6px 0 0; vertical-align: 1px; }
.b-item { display: flex; align-items: center; gap: 10px; padding: 10px 0; border-top: 1px solid var(--c-border); flex-wrap: wrap; }
.b-main { display: flex; flex-direction: column; gap: 2px; flex: 1; min-width: 200px; }
.b-act { gap: 4px; }
p { line-height: 1.6; }
@media (max-width: 1279px) {
  .s-group { grid-template-columns: minmax(0, 1fr); gap: 10px; }
  .s-head { padding-top: 0; }
  .field { grid-template-columns: minmax(0, 1fr) 260px; gap: 10px 20px; }
  .field.stacked { grid-template-columns: minmax(0, 1fr); }
}
@media (max-width: 767px) {
  .settings { gap: 22px; }
  .s-card { padding: 2px 16px; }
  .field { grid-template-columns: minmax(0, 1fr); gap: 8px; }
  .field.f-bool { grid-template-columns: minmax(0, 1fr) auto; gap: 12px; }
  .fx { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .savebar { bottom: calc(56px + env(safe-area-inset-bottom)); }
}
</style>
