<script setup>
import { ref, computed, onMounted } from 'vue'
import { NButton, NInput } from 'naive-ui'
import QRCode from 'qrcode'
import { api } from '../../api'
import { state, loadMe, ui } from '../../store'
import { run, ask } from '../../helpers'
import { dt, ago, copy } from '../../format'

const pw = ref({ old: '', new: '', again: '' })
const pwBusy = ref(false)
const totpBusy = ref(false)
const kicking = ref(0)
const totp = ref({ step: '', secret: '', qr: '', code: '', codes: [] })
const disableCode = ref('')
const sessions = ref([])
const pwChanged = computed(() => { const t = state.me?.password_at; return t && !t.startsWith('0001') ? ago(t) : '' })

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
    if (await run(() => api.post('/account/password', { old: p.old, new: p.new }), '密码已修改，其他会话已退出')) {
      pw.value = { old: '', new: '', again: '' }
      loadSessions()
    }
  } finally { pwBusy.value = false }
}
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

onMounted(loadSessions)
</script>

<template>
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
              <code class="mono small secret" role="button" tabindex="0" @click="copyText(totp.secret, '已复制密钥')" @keydown.enter="copyText(totp.secret, '已复制密钥')">{{ totp.secret }}</code>
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
</template>

<style scoped>
.secret { cursor: pointer; word-break: break-all; background: var(--c-surface); padding: 6px 8px; border-radius: 4px; }
.codes { display: grid; grid-template-columns: repeat(2, 1fr); gap: 6px 16px; padding: 12px; background: var(--c-surface); border-radius: 6px; margin: 10px 0; font-size: 14px; }
.totp-scan { display: flex; gap: 20px; align-items: flex-start; flex-wrap: wrap; }
.totp-scan > * { width: auto; }
.totp-side { flex: 1; min-width: 200px; display: flex; flex-direction: column; gap: 8px; }
</style>
