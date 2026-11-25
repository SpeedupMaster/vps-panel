<script setup>
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
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
const pwBusy = ref(false)
const totpBusy = ref(false)
const kicking = ref(0)
async function copyText(text, msg = '已复制') {
  if (await copy(text)) ui.message.success(msg)
}
async function changePw() {
  if (pwBusy.value) return
  const p = pw.value
  if (p.new.length < 10) { ui.message.warning('新密码至少 10 位'); return }
  if (p.new !== p.again) { ui.message.warning('两次输入的新密码不一致'); return }
  pwBusy.value = true
  try {
    if (await run(() => api.post('/account/password', { old: p.old, new: p.new }), '密码已修改，其他会话已退出')) { pw.value = { old: '', new: '', again: '' }; loadSessions() }
  } finally { pwBusy.value = false }
}
const totp = ref({ step: '', secret: '', qr: '', code: '', codes: [] })
async function beginTotp() {
  if (totpBusy.value) return
  totpBusy.value = true
  try {
    const r = await run(() => api.post('/account/totp/begin'))
    if (!r) return
    totp.value = { step: 'scan', secret: r.secret, qr: await QRCode.toDataURL(r.uri, { margin: 1, width: 200 }), code: '', codes: [] }
  } finally { totpBusy.value = false }
}
async function confirmTotp() {
  if (totpBusy.value) return
  totpBusy.value = true
  try {
    const r = await run(() => api.post('/account/totp/confirm', { code: totp.value.code }), '两步验证已开启')
    if (r) { totp.value = { step: 'codes', codes: r.recovery_codes }; loadMe() }
  } finally { totpBusy.value = false }
}
const disableCode = ref('')
async function disableTotp() {
  if (totpBusy.value) return
  totpBusy.value = true
  try {
    if (!(await ask('关闭两步验证', '关闭后只凭密码即可登录，敏感操作改为验证密码。', { danger: true }))) return
    if (await run(() => api.post('/account/totp/disable', { code: disableCode.value }), '已关闭')) { disableCode.value = ''; loadMe() }
  } finally { totpBusy.value = false }
}
async function regenCodes() {
  if (totpBusy.value) return
  totpBusy.value = true
  try {
    if (!(await ask('重新生成恢复码', '旧的恢复码会全部作废。'))) return
    const r = await run(() => api.post('/account/totp/recovery'))
    if (r) { totp.value = { step: 'codes', codes: r.recovery_codes }; loadMe() }
  } finally { totpBusy.value = false }
}
const sessions = ref([])
async function loadSessions() { sessions.value = (await api.get('/account/sessions').catch(() => [])) || [] }
async function kick(s) {
  if (kicking.value === s.id) return
  kicking.value = s.id
  try {
    if (await run(() => api.del(`/account/sessions/${s.id}`), '已踢下线')) loadSessions()
  } finally { kicking.value = 0 }
}
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
      S('sub_url', '订阅地址', '', '留空 = 用面板地址；填写独立域名或 CDN 时需先将其解析并反代到面板（独立域名仅暴露订阅接口）'),
      S('admin_path', '后台路径', '', '例如 /x7k2，留空 = 根路径；改后旧路径返回 404'),
      { k: 'allow_ips', label: '允许访问的 IP', type: 'text', def: '', hint: '每行或逗号分隔一个 IP / CIDR；留空 = 不限制。订阅与 Agent 连接不受影响' },
    ] },
    { title: '费用', desc: '服务器月费按所选币种换算后汇总。', fields: [
      { k: 'currency', label: '统计币种', type: 'select', def: 'USD', options: opts(['USD', 'USD 美元'], ['CNY', 'CNY 人民币'], ['EUR', 'EUR 欧元'], ['HKD', 'HKD 港币'], ['JPY', 'JPY 日元'], ['GBP', 'GBP 英镑'], ['CAD', 'CAD 加元']) },
      { k: 'fx_rates', label: '汇率', type: 'fx', def: '', hint: '1 USD 可兑换的数量；留空的币种用默认汇率' },
    ] },
    { title: '流量突增', desc: '「流量异常突增」告警的判定条件。告警开关与通知渠道在「告警」页设置。', fields: [
      N('traffic_spike_factor', '突增倍数', 5, '某小时流量超过近 7 天同一时段均值的这个倍数时告警', '倍'),
      N('traffic_spike_min_gb', '最小流量', 2, '这一小时的流量低于此值时不算突增', 'GB'),
    ] },
    { title: 'Agent 升级签名', desc: 'Agent 升级包的签名校验。', fields: [
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
    { title: '检测工具', desc: '工具页「IP 质量」使用的第三方数据源。proxycheck.io 和 ip-api.com 无需配置。', fields: [
      { k: 'ipapi_is_key', label: 'ipapi.is API Key', type: 'secret', def: '', hint: '在 ipapi.is 免费注册获取；留空则跳过该来源。机房 / VPN / 代理 / 滥用标记需要 Key 才会返回' },
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
const dirtyKeys = computed(() => Object.keys(cfg.value).filter((k) => !backupKeys.includes(k) && isDirty(k)))
const pwChanged = computed(() => { const t = state.me?.password_at; return t && !t.startsWith('0001') ? ago(t) : '' })
function boolVal(f) { const v = cfg.value[f.k]; return v === '' || v == null ? f.def : v === '1' || v === 'true' }
function numVal(f) { const v = cfg.value[f.k]; return v === '' || v == null ? null : Number(v) }
function setVal(k, v) { cfg.value = { ...cfg.value, [k]: v == null ? '' : typeof v === 'boolean' ? (v ? '1' : '0') : String(v) } }
function isDirty(k) { return (cfg.value[k] ?? '') !== (orig.value[k] ?? '') }
function placeholder(f) { return f.def !== '' && f.def != null ? `默认 ${f.def}` : '未设置' }
// fx_rates is stored as JSON and merged over the panel's defaults; a
// saved value always carries every currency.
const fxDefault = { CNY: 7.2, EUR: 0.92, HKD: 7.8, JPY: 150, GBP: 0.79, CAD: 1.38 }
function fxStored() { try { const o = JSON.parse(cfg.value.fx_rates || '{}'); return o && typeof o === 'object' ? o : {} } catch { return {} } }
function fxVal(c) { const v = fxStored()[c]; return v == null || Number(v) === fxDefault[c] ? null : Number(v) }
function setFx(c, v) {
  const o = { ...fxDefault, ...fxStored() }
  o[c] = v == null ? fxDefault[c] : v
  setVal('fx_rates', Object.keys(fxDefault).every((k) => o[k] === fxDefault[k]) && Object.keys(o).length === Object.keys(fxDefault).length ? '' : JSON.stringify(o))
}
const saving = ref(false)
async function saveSettings(keys) {
  if (saving.value) return
  const body = {}
  for (const k of keys || dirtyKeys.value) body[k] = cfg.value[k]
  if (!Object.keys(body).length) { ui.message.info('没有修改'); return }
  if (body.fx_rates) { try { JSON.parse(body.fx_rates) } catch { ui.message.error('汇率不是合法的 JSON'); return } }
  const adminPathChanged = Object.prototype.hasOwnProperty.call(body, 'admin_path') &&
    String(body.admin_path || '').trim().replace(/^\/+|\/+$/g, '') !== String(orig.value.admin_path || '').trim().replace(/^\/+|\/+$/g, '')
  saving.value = true
  try {
    if (await run(() => api.put('/settings', body), adminPathChanged ? '后台路径已更新，正在跳转…' : '已保存')) {
      if (adminPathChanged) {
        const seg = String(body.admin_path || '').trim().replace(/^\/+|\/+$/g, '')
        const target = seg ? `/${seg}/settings?tab=panel` : '/settings?tab=panel'
        try { await window.caches?.delete('pages') } catch { /* ignore */ }
        window.location.assign(target)
        return
      }
      loadSettings()
    }
  } finally { saving.value = false }
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
const backupDirtyKeys = computed(() => backupKeys.filter(isDirty))
function resetKeys(keys) { const c = { ...cfg.value }; for (const k of keys) c[k] = orig.value[k]; cfg.value = c }
async function saveBackupSettings() { await saveSettings(backupDirtyKeys.value); loadBackups() }
const remoteGroups = computed(() => [
  targets.value.includes('s3') && { title: 'S3 / R2', desc: '兼容 S3 的对象存储，例如 Cloudflare R2、AWS S3、MinIO。', fields: [
    S('backup_s3_endpoint', 'Endpoint', '', '例如 https://<账户 ID>.r2.cloudflarestorage.com'),
    S('backup_s3_bucket', 'Bucket', '', ''),
    S('backup_s3_region', 'Region', 'auto', 'R2 用 auto'),
    S('backup_s3_access_key', 'Access Key', '', ''),
    { k: 'backup_s3_secret', label: 'Secret Key', type: 'secret', def: '' },
    S('backup_s3_prefix', '路径前缀', 'vps-panel', '备份放在 Bucket 里的这个目录下'),
  ] },
  targets.value.includes('webdav') && { title: 'WebDAV', desc: '坚果云、Nextcloud、群晖等支持 WebDAV 的网盘。', fields: [
    S('backup_webdav_url', '地址', '', '备份上传到的目录，例如 https://dav.example.com/backup/'),
    S('backup_webdav_user', '用户名', '', ''),
    { k: 'backup_webdav_password', label: '密码', type: 'secret', def: '' },
  ] },
  targets.value.includes('telegram') && { title: 'Telegram', desc: '用告警渠道里配置的 Bot，把备份文件发到对应的 Chat。', fields: [
    { k: 'backup_tg_channel', label: '发送到', type: 'tg', def: '', hint: '在「告警」页添加 Telegram 渠道' },
  ] },
].filter(Boolean))
const backing = ref(false)
const downloading = ref('')
const deletingBackup = ref('')
const restoring = ref(false)
async function backupNow() {
  if (backing.value) return
  backing.value = true
  try {
    const r = await run(() => api.post('/backups'))
    if (r) { r.warning ? ui.message.warning('本地备份已完成，远程上传失败：' + r.warning) : ui.message.success('备份完成：' + r.name); loadBackups() }
  } finally { backing.value = false }
}
async function downloadBackup(b) {
  if (downloading.value) return
  downloading.value = b.name
  try {
    const blob = await run(() => api.blob(`/backups/${encodeURIComponent(b.name)}`, { confirm: '下载备份需要确认身份' }))
    if (!blob) return
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = b.name
    document.body.appendChild(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } finally { downloading.value = '' }
}
async function delBackup(b) {
  if (deletingBackup.value) return
  deletingBackup.value = b.name
  try {
    if (!(await ask('删除备份', `删除 ${b.name}？只删除面板本地的副本。`, { danger: true }))) return
    if (await run(() => api.del(`/backups/${encodeURIComponent(b.name)}`), '已删除')) loadBackups()
  } finally { deletingBackup.value = '' }
}
const restore = ref({ show: false, name: '', file: null, password: '' })
function openRestore(b) { restore.value = { show: true, name: b?.name || '', file: null, password: '' } }
async function doRestore() {
  if (restoring.value) return
  const r = restore.value
  if (!r.name && !r.file) { ui.message.warning('选择备份文件'); return }
  restoring.value = true
  try {
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
  } finally { restoring.value = false }
}

// ---------- audit ----------
const audit = ref({ type: 'all', items: [], offset: 0, more: false })
const auditLoading = ref(false)
let auditAbort = null
const auditTypes = [{ label: '全部', value: 'all' }, { label: '登录', value: 'login' }, { label: '服务器', value: 'server' }, { label: '线路', value: 'route' }, { label: '订阅', value: 'sub' }, { label: '告警', value: 'alert' }, { label: '系统', value: 'system' }]
const typeLabel = Object.fromEntries(auditTypes.map((t) => [t.value, t.label]))
async function loadAudit(reset) {
  if (!reset && auditLoading.value) return
  auditAbort?.abort()
  const ac = new AbortController()
  auditAbort = ac
  if (reset) audit.value.offset = 0
  const a = audit.value
  auditLoading.value = true
  try {
    const rows = (await api.get(`/audit?type=${a.type}&limit=100&offset=${a.offset}`, { signal: ac.signal }).catch(() => null))
    if (ac.signal.aborted || !rows) return
    audit.value.items = reset ? rows : [...a.items, ...rows]
    audit.value.more = rows.length === 100
    audit.value.offset += rows.length
  } finally {
    if (auditAbort === ac) auditLoading.value = false
  }
}
watch(() => audit.value.type, () => loadAudit(true))
onBeforeUnmount(() => auditAbort?.abort())

onMounted(() => {
  loadSettings(); loadSessions()
  if (tab.value === 'backup') loadBackups()
  if (tab.value === 'audit') loadAudit(true)
})
</script>

<template>
  <div class="page">
    <div class="page-head"><div class="ph"><h1>设置</h1><p class="page-desc">账号安全、面板、Agent、Xray、数据保留与备份的全局设置。</p></div></div>
    <n-tabs v-model:value="tab" type="line" animated>
      <n-tab-pane name="account" tab="账号与安全">
        <div class="sections">
          <section class="s-group">
            <div class="s-head">
              <h2>登录密码</h2>
              <p>修改后，其他设备上的登录会全部退出。</p>
            </div>
            <div class="card s-card">
              <div class="field f-inline">
                <div class="f-label"><span class="f-name">账号</span><span v-if="pwChanged" class="f-hint">{{ pwChanged }}修改过密码</span></div>
                <div class="f-ctl"><span class="mono">{{ state.me?.username }}</span></div>
              </div>
              <div class="field">
                <div class="f-label"><span class="f-name">当前密码</span></div>
                <div class="f-ctl"><n-input v-model:value="pw.old" type="password" show-password-on="click" placeholder="" aria-label="当前密码" :input-props="{ autocomplete: 'current-password' }" /></div>
              </div>
              <div class="field">
                <div class="f-label"><span class="f-name">新密码</span><span class="f-hint">至少 10 位</span></div>
                <div class="f-ctl"><n-input v-model:value="pw.new" type="password" show-password-on="click" placeholder="" aria-label="新密码" :input-props="{ autocomplete: 'new-password' }" /></div>
              </div>
              <div class="field">
                <div class="f-label"><span class="f-name">确认新密码</span></div>
                <div class="f-ctl"><n-input v-model:value="pw.again" type="password" show-password-on="click" placeholder="" aria-label="确认新密码" :input-props="{ autocomplete: 'new-password' }" @keyup.enter="changePw" /></div>
              </div>
              <div class="f-actions">
                <span class="f-hint">忘记密码时，在面板所在机器上执行 <code class="mono">vps-panel reset-admin</code></span>
                <span class="spacer" />
                <n-button type="primary" :loading="pwBusy" :disabled="!pw.old || !pw.new || !pw.again || pwBusy" @click="changePw">修改密码</n-button>
              </div>
            </div>
          </section>

          <section class="s-group">
            <div class="s-head">
              <h2>两步验证</h2>
              <p>面板能控制所有 VPS，强烈建议开启。开启后，登录和敏感操作（删除服务器、执行脚本、打开终端、恢复备份等）都要输入验证码。</p>
            </div>
            <div class="card s-card">
              <div v-if="totp.step === 'scan'" class="field stacked">
                <div class="f-label"><span class="f-name">扫描二维码</span><span class="f-hint">用 Google Authenticator、1Password 等扫描，然后输入 App 里显示的 6 位验证码。</span></div>
                <div class="f-ctl totp-scan">
                  <img :src="totp.qr" alt="两步验证二维码" width="160" height="160" />
                  <div class="totp-side">
                    <span class="f-hint">无法扫码时手动输入（点击复制）：</span>
                    <code class="mono small secret" @click="copyText(totp.secret, '已复制密钥')">{{ totp.secret }}</code>
                    <n-input v-model:value="totp.code" placeholder="6 位验证码" maxlength="6" :input-props="{ inputmode: 'numeric', autocomplete: 'one-time-code' }" @keyup.enter="confirmTotp" />
                    <div class="row"><n-button @click="totp.step = ''">取消</n-button><n-button type="primary" :loading="totpBusy" :disabled="totp.code.length < 6 || totpBusy" @click="confirmTotp">确认开启</n-button></div>
                  </div>
                </div>
              </div>
              <div v-else-if="totp.step === 'codes'" class="field stacked">
                <div class="f-label"><span class="f-name">恢复码</span><span class="f-hint">只显示这一次。手机丢失时可用其中任意一个登录，每个只能用一次。</span></div>
                <div class="f-ctl">
                  <div class="codes mono"><span v-for="c in totp.codes" :key="c">{{ c }}</span></div>
                  <div class="row"><n-button @click="copyText(totp.codes.join('\n'), '已复制全部恢复码')">复制全部</n-button><n-button type="primary" @click="totp.step = ''">我已保存</n-button></div>
                </div>
              </div>
              <template v-else>
                <div class="field f-inline">
                  <div class="f-label"><span class="f-name">状态</span><span v-if="state.me?.totp" class="f-hint">剩余恢复码 {{ state.me.recovery_left }} 个</span></div>
                  <div class="f-ctl"><span :class="['tag', state.me?.totp ? 'ok' : 'warn']">{{ state.me?.totp ? '已开启' : '未开启' }}</span></div>
                </div>
                <template v-if="state.me?.totp">
                  <div class="field f-inline">
                    <div class="f-label"><span class="f-name">恢复码</span><span class="f-hint">重新生成后，旧的恢复码全部作废</span></div>
                    <div class="f-ctl"><n-button :loading="totpBusy" :disabled="totpBusy" @click="regenCodes">重新生成</n-button></div>
                  </div>
                  <div class="field">
                    <div class="f-label"><span class="f-name">关闭两步验证</span><span class="f-hint">输入当前验证码确认。关闭后只凭密码即可登录</span></div>
                    <div class="f-ctl ctl-row">
                      <n-input v-model:value="disableCode" placeholder="6 位验证码" maxlength="6" :input-props="{ inputmode: 'numeric', autocomplete: 'one-time-code' }" />
                      <n-button type="error" ghost :loading="totpBusy" :disabled="disableCode.length < 6 || totpBusy" @click="disableTotp">关闭</n-button>
                    </div>
                  </div>
                </template>
                <div v-else class="f-actions">
                  <span class="spacer" />
                  <n-button type="primary" :loading="totpBusy" :disabled="totpBusy" @click="beginTotp">开启两步验证</n-button>
                </div>
              </template>
            </div>
          </section>

          <section class="s-group">
            <div class="s-head">
              <h2>登录会话</h2>
              <p>当前登录着这个面板的设备。不认识的设备请踢下线并修改密码。</p>
            </div>
            <div class="card s-card">
              <div v-if="!sessions.length" class="empty">没有会话</div>
              <template v-else>
              <div class="table-wrap hide-mobile">
                <table class="plain">
                  <thead><tr><th>设备</th><th>IP</th><th>登录时间</th><th>最近活动</th><th /></tr></thead>
                  <tbody>
                    <tr v-for="s in sessions" :key="s.id">
                      <td :title="s.ua">{{ uaShort(s.ua) }} <span v-if="s.current" class="tag ok">当前</span></td>
                      <td class="mono">{{ s.ip }}</td><td class="nowrap">{{ dt(s.created_at) }}</td><td class="nowrap">{{ ago(s.last_seen) }}</td>
                      <td style="text-align: right"><n-button v-if="!s.current" size="tiny" quaternary type="error" :loading="kicking === s.id" :disabled="kicking === s.id" @click="kick(s)">踢下线</n-button></td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <div class="m-list only-mobile">
                <div v-for="s in sessions" :key="s.id" class="m-item">
                  <div class="m-row"><b>{{ uaShort(s.ua) }}</b><span v-if="s.current" class="tag ok">当前</span><span class="spacer" /><n-button v-if="!s.current" size="small" quaternary type="error" :loading="kicking === s.id" :disabled="kicking === s.id" @click="kick(s)">踢下线</n-button></div>
                  <div class="m-row small muted"><span class="mono">{{ s.ip }}</span><span>· {{ ago(s.last_seen) }}</span></div>
                </div>
              </div>
              </template>
            </div>
          </section>
        </div>
      </n-tab-pane>

      <n-tab-pane v-for="t in settingsTabs" :key="t.name" :name="t.name" :tab="t.label">
        <div class="sections">
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
                  <n-input v-else-if="f.type === 'secret'" :value="cfg[f.k]" type="password" show-password-on="click" :placeholder="placeholder(f)" :input-props="{ autocomplete: 'off' }" @update:value="(v) => setVal(f.k, v)" />
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
          <n-button type="primary" :loading="saving" :disabled="!dirtyKeys.length || saving" @click="saveSettings()">保存</n-button>
        </div>
      </n-tab-pane>

      <n-tab-pane name="backup" tab="备份">
        <div class="sections">
          <section class="s-group">
            <div class="s-head">
              <h2>备份文件</h2>
              <p>备份包含数据库和密钥，保存在面板所在的机器上。</p>
            </div>
            <div class="card s-card">
              <div class="s-card-head">
                <span class="f-hint">{{ bk.items?.length || 0 }} 个备份<template v-if="bk.last"> · 上次备份 {{ ago(bk.last) }}</template></span>
                <span class="spacer" />
                <n-button size="small" @click="openRestore(null)">上传并恢复</n-button>
                <n-button size="small" type="primary" :loading="backing" @click="backupNow">立即备份</n-button>
              </div>
              <div v-if="bk.last_error" class="banner warn">上次远程备份失败：{{ bk.last_error }}</div>
              <div v-if="!bk.encrypted" class="banner info">未设置备份密码，备份里的密钥是明文。建议在下方「自动备份」里设置。</div>
              <div v-if="!bk.items?.length" class="empty">还没有备份</div>
              <div v-for="b in bk.items" :key="b.name" class="b-item">
                <div class="b-main">
                  <span class="mono small">{{ b.name }}</span>
                  <span class="f-hint">{{ dt(b.at) }} · {{ bytes(b.size) }}{{ b.encrypted ? ' · 已加密' : '' }}</span>
                </div>
                <div class="row b-act">
                  <n-button size="tiny" quaternary :loading="downloading === b.name" :disabled="!!downloading" @click="downloadBackup(b)">下载</n-button>
                  <n-button size="tiny" quaternary @click="openRestore(b)">恢复</n-button>
                  <n-button size="tiny" quaternary type="error" :loading="deletingBackup === b.name" :disabled="!!deletingBackup" @click="delBackup(b)">删除</n-button>
                </div>
              </div>
            </div>
          </section>

          <section class="s-group">
            <div class="s-head">
              <h2>自动备份</h2>
              <p>每天定时备份一次，可以同时上传到远程存储。</p>
            </div>
            <div class="card s-card">
              <div :class="['field', 'f-bool', { dirty: isDirty('backup_enabled') }]">
                <div class="f-label"><span class="f-name">开启自动备份<i v-if="isDirty('backup_enabled')" class="f-dot" /></span></div>
                <div class="f-ctl"><n-switch :value="boolVal({ k: 'backup_enabled', def: true })" aria-label="开启自动备份" @update:value="(v) => setVal('backup_enabled', v)" /></div>
              </div>
              <div class="field">
                <div class="f-label"><span class="f-name">备份时间<i v-if="isDirty('backup_time')" class="f-dot" /></span><span class="f-hint">每天这个时间执行，24 小时制</span></div>
                <div class="f-ctl"><n-input :value="cfg.backup_time" placeholder="默认 03:30" clearable @update:value="(v) => setVal('backup_time', v)" /></div>
              </div>
              <div class="field">
                <div class="f-label"><span class="f-name">保留份数<i v-if="isDirty('backup_keep')" class="f-dot" /></span><span class="f-hint">面板本机保留最近的几份，更早的自动删除</span></div>
                <div class="f-ctl">
                  <n-input-number :value="numVal({ k: 'backup_keep' })" placeholder="默认 7" :min="1" :show-button="false" clearable @update:value="(v) => setVal('backup_keep', v)">
                    <template #suffix><span class="f-unit">份</span></template>
                  </n-input-number>
                </div>
              </div>
              <div class="field">
                <div class="f-label"><span class="f-name">备份密码<i v-if="isDirty('backup_password')" class="f-dot" /></span><span class="f-hint">恢复时需要这个密码，忘了就无法从加密备份恢复</span></div>
                <div class="f-ctl"><n-input :value="cfg.backup_password" type="password" show-password-on="click" placeholder="留空 = 不加密" :input-props="{ autocomplete: 'new-password' }" @update:value="(v) => setVal('backup_password', v)" /></div>
              </div>
              <div class="field">
                <div class="f-label"><span class="f-name">同时上传到<i v-if="isDirty('backup_target')" class="f-dot" /></span><span class="f-hint">勾选后在下方填写对应的配置</span></div>
                <div class="f-ctl">
                  <n-checkbox-group v-model:value="targets">
                    <div class="row"><n-checkbox value="s3">S3 / R2</n-checkbox><n-checkbox value="webdav">WebDAV</n-checkbox><n-checkbox value="telegram">Telegram</n-checkbox></div>
                  </n-checkbox-group>
                </div>
              </div>
            </div>
          </section>

          <section v-for="g in remoteGroups" :key="g.title" class="s-group">
            <div class="s-head">
              <h2>{{ g.title }}</h2>
              <p>{{ g.desc }}</p>
            </div>
            <div class="card s-card">
              <div v-for="f in g.fields" :key="f.k" :class="['field', { dirty: isDirty(f.k) }]">
                <div class="f-label"><span class="f-name">{{ f.label }}<i v-if="isDirty(f.k)" class="f-dot" /></span><span v-if="f.hint" class="f-hint">{{ f.hint }}</span></div>
                <div class="f-ctl">
                  <n-select v-if="f.type === 'tg'" :value="cfg[f.k] || ''" :options="[{ label: '第一个 Telegram 渠道', value: '' }, ...tgChannels.map((c) => ({ label: c.name, value: String(c.id) }))]" @update:value="(v) => setVal(f.k, v)" />
                  <n-input v-else-if="f.type === 'secret'" :value="cfg[f.k]" type="password" show-password-on="click" :placeholder="placeholder(f)" :input-props="{ autocomplete: 'off' }" @update:value="(v) => setVal(f.k, v)" />
                  <n-input v-else :value="cfg[f.k]" :placeholder="placeholder(f)" clearable @update:value="(v) => setVal(f.k, v)" />
                </div>
              </div>
            </div>
          </section>
        </div>
        <div class="savebar">
          <span v-if="backupDirtyKeys.length" class="small dirty-note"><i class="f-dot" />{{ backupDirtyKeys.length }} 项未保存</span>
          <span v-else class="small muted">留空的项使用默认值</span>
          <span class="spacer" />
          <n-button :disabled="!backupDirtyKeys.length" @click="resetKeys(backupKeys)">撤销</n-button>
          <n-button type="primary" :loading="saving" :disabled="!backupDirtyKeys.length || saving" @click="saveBackupSettings">保存</n-button>
        </div>
      </n-tab-pane>

      <n-tab-pane name="audit" tab="审计日志">
        <div class="sec-head">
          <div class="s-head">
            <h2>审计日志</h2>
            <p>登录和面板上的修改操作都记录在这里。</p>
          </div>
          <span class="spacer" />
          <n-select v-model:value="audit.type" :options="auditTypes" aria-label="类别" style="width: 120px" />
          <n-button tag="a" :href="`/api/audit?format=csv&type=${audit.type}`" download>导出 CSV</n-button>
        </div>
        <div class="card s-card">
          <div v-if="!audit.items.length" class="empty">暂无记录</div>
          <template v-else>
            <div class="table-wrap hide-mobile">
              <table class="plain audit">
                <thead><tr><th>时间</th><th>类别</th><th>操作</th><th>对象</th><th>详情</th><th>IP</th></tr></thead>
                <tbody>
                  <tr v-for="l in audit.items" :key="l.id">
                    <td>{{ dt(l.ts) }}</td><td>{{ typeLabel[l.type] || l.type }}</td><td class="mono small">{{ l.action }}</td>
                    <td class="small">{{ l.target }}</td><td class="small muted detail" :title="l.detail">{{ l.detail }}</td><td class="mono small">{{ l.ip }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div class="m-list only-mobile">
              <div v-for="l in audit.items" :key="l.id" class="m-item">
                <div class="m-row"><span class="mono">{{ l.action }}</span><span class="tag">{{ typeLabel[l.type] || l.type }}</span></div>
                <div v-if="l.target || l.detail" class="audit-text small">{{ [l.target, l.detail].filter(Boolean).join(' · ') }}</div>
                <div class="m-row small"><span>{{ dt(l.ts) }}</span><span class="mono muted">{{ l.ip }}</span></div>
              </div>
            </div>
          </template>
          <div v-if="audit.more" class="f-actions more"><n-button :loading="auditLoading" :disabled="auditLoading" @click="loadAudit(false)">加载更多</n-button></div>
        </div>
      </n-tab-pane>
    </n-tabs>

    <n-modal v-model:show="restore.show" preset="card" title="恢复备份" style="width: min(460px, 96vw)">
      <div class="m-form">
        <div v-if="restore.name" class="field f-inline">
          <div class="f-label"><span class="f-name">备份</span></div>
          <div class="f-ctl"><span class="mono small b-name">{{ restore.name }}</span></div>
        </div>
        <div class="field f-inline" v-else>
          <div class="f-label"><span class="f-name">备份文件</span><span class="f-hint">.vpb 或 .gz</span></div>
          <div class="f-ctl"><n-upload :max="1" :default-upload="false" accept=".vpb,.gz" @change="({ fileList }) => (restore.file = fileList[0]?.file || null)">
            <n-button>选择备份文件</n-button>
          </n-upload></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">备份密码</span><span class="f-hint">加密备份需要，未加密的留空</span></div>
          <div class="f-ctl"><n-input v-model:value="restore.password" type="password" show-password-on="click" placeholder="未设置" :input-props="{ autocomplete: 'off' }" /></div>
        </div>
        <div class="banner warn">恢复会覆盖数据库与密钥，面板随后自动重启。</div>
      </div>
      <template #footer><div class="m-foot"><n-button @click="restore.show = false">取消</n-button><n-button type="error" :loading="restoring" :disabled="restoring" @click="doRestore">恢复</n-button></div></template>
    </n-modal>
  </div>
</template>

<style scoped>
.secret { cursor: pointer; word-break: break-all; background: var(--c-surface); padding: 6px 8px; border-radius: 4px; }
.codes { display: grid; grid-template-columns: repeat(2, 1fr); gap: 6px 16px; padding: 12px; background: var(--c-surface); border-radius: 6px; margin: 10px 0; font-size: 14px; }
.totp-scan { display: flex; gap: 20px; align-items: flex-start; flex-wrap: wrap; }
.totp-scan > * { width: auto; }
.totp-side { flex: 1; min-width: 200px; display: flex; flex-direction: column; gap: 8px; }
.table-wrap td.detail { white-space: normal; min-width: 260px; overflow-wrap: anywhere; }
.audit-text { color: var(--c-text-2); line-height: 1.55; overflow-wrap: anywhere; }
.fx { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 8px; }
.fx-code { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 12px; margin-right: 4px; }
.b-item { display: flex; align-items: center; gap: 10px; padding: 10px 0; border-top: 1px solid var(--c-border); flex-wrap: wrap; }
.b-main { display: flex; flex-direction: column; gap: 2px; flex: 1; min-width: 200px; }
.b-act { gap: 4px; }
.b-name { overflow-wrap: anywhere; text-align: right; }
.more { justify-content: center; }
p { line-height: 1.6; }
@media (max-width: 767px) {
  .fx { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
</style>