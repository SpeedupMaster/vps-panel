<script setup>
import { NButton, NInput, NInputNumber, NSwitch, NSelect } from 'naive-ui'
import { state } from '../../store'
import { idsCsv } from '../../helpers'

const props = defineProps({
  section: { type: String, required: true },
  cfg: { type: Object, required: true },
  orig: { type: Object, required: true },
  dirtyKeys: { type: Array, required: true },
  saving: { type: Boolean, default: false },
})
const emit = defineEmits(['set-val', 'reset', 'save'])

const B = (k, label, def, hint) => ({ k, label, type: 'bool', def, hint })
const N = (k, label, def, hint, unit) => ({ k, label, type: 'num', def, hint, unit })
const S = (k, label, def, hint) => ({ k, label, type: 'str', def, hint })
const opts = (...xs) => xs.map(([value, label]) => ({ value, label }))
const fxDefault = { CNY: 7.2, EUR: 0.92, HKD: 7.8, JPY: 150, GBP: 0.79, CAD: 1.38 }

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

const isDirty = (k) => (props.cfg[k] ?? '') !== (props.orig[k] ?? '')
const boolVal = (f) => { const v = props.cfg[f.k]; return v === '' || v == null ? f.def : v === '1' || v === 'true' }
const numVal = (f) => { const v = props.cfg[f.k]; return v === '' || v == null ? null : Number(v) }
const placeholder = (f) => (f.def !== '' && f.def != null ? `默认 ${f.def}` : '未设置')
function fxStored() { try { const o = JSON.parse(props.cfg.fx_rates || '{}'); return o && typeof o === 'object' ? o : {} } catch { return {} } }
function fxVal(c) { const v = fxStored()[c]; return v == null || Number(v) === fxDefault[c] ? null : Number(v) }
function setFx(c, v) {
  const o = { ...fxDefault, ...fxStored() }
  o[c] = v == null ? fxDefault[c] : v
  const isDefault = Object.keys(fxDefault).every((k) => o[k] === fxDefault[k]) && Object.keys(o).length === Object.keys(fxDefault).length
  emit('set-val', 'fx_rates', isDefault ? '' : JSON.stringify(o))
}
</script>

<template>
  <div class="sections">
    <section v-for="g in groups[section]" :key="g.title" class="s-group">
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
            <n-switch v-if="f.type === 'bool'" :value="boolVal(f)" :aria-label="f.label" @update:value="(v) => emit('set-val', f.k, v)" />
            <n-input-number v-else-if="f.type === 'num'" :value="numVal(f)" :placeholder="placeholder(f)" :min="0" :show-button="false" clearable @update:value="(v) => emit('set-val', f.k, v)">
              <template v-if="f.unit" #suffix><span class="f-unit">{{ f.unit }}</span></template>
            </n-input-number>
            <n-select v-else-if="f.type === 'select'" :value="cfg[f.k] || f.def" :options="f.options" @update:value="(v) => emit('set-val', f.k, v)" />
            <n-select v-else-if="f.type === 'servers'" multiple clearable placeholder="全部服务器" max-tag-count="responsive"
              :value="idsCsv(cfg[f.k])" :options="state.servers.map((s) => ({ label: s.name, value: s.id }))"
              @update:value="(v) => emit('set-val', f.k, v.join(','))" />
            <div v-else-if="f.type === 'fx'" class="fx">
              <n-input-number v-for="(d, c) in fxDefault" :key="c" :value="fxVal(c)" :placeholder="String(d)" :min="0" :show-button="false" clearable @update:value="(v) => setFx(c, v)">
                <template #prefix><span class="f-unit fx-code">{{ c }}</span></template>
              </n-input-number>
            </div>
            <n-input v-else-if="f.type === 'text'" :value="cfg[f.k]" type="textarea" :autosize="{ minRows: 2, maxRows: 4 }" class="mono" :placeholder="placeholder(f)" @update:value="(v) => emit('set-val', f.k, v)" />
            <n-input v-else-if="f.type === 'secret'" :value="cfg[f.k]" type="password" show-password-on="click" :placeholder="placeholder(f)" :input-props="{ autocomplete: 'off' }" @update:value="(v) => emit('set-val', f.k, v)" />
            <n-input v-else :value="cfg[f.k]" :placeholder="placeholder(f)" clearable @update:value="(v) => emit('set-val', f.k, v)" />
          </div>
        </div>
      </div>
    </section>
  </div>
  <div class="savebar">
    <span v-if="dirtyKeys.length" class="small dirty-note"><i class="f-dot" />{{ dirtyKeys.length }} 项未保存</span>
    <span v-else class="small muted">留空的项使用默认值</span>
    <span class="spacer" />
    <n-button :disabled="!dirtyKeys.length" @click="emit('reset')">撤销</n-button>
    <n-button type="primary" :loading="saving" :disabled="!dirtyKeys.length || saving" @click="emit('save')">保存</n-button>
  </div>
</template>

<style scoped>
.fx { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 8px; }
.fx-code { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 12px; margin-right: 4px; }
@media (max-width: 767px) { .fx { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
</style>
