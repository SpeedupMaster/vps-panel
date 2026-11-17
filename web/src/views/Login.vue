<script setup>
import { ref, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NInput, NButton, NForm, NFormItem } from 'naive-ui'
import { api } from '../api'
import { state, loadMe } from '../store'

const route = useRoute()
const router = useRouter()
const step = ref('password') // password | totp
const username = ref('')
const password = ref('')
const code = ref('')
const recovery = ref(false)
const err = ref('')
const busy = ref(false)
const codeRef = ref(null)

async function submit() {
  err.value = ''
  if (!username.value || !password.value) { err.value = '请输入账号和密码'; return }
  if (step.value === 'totp' && !code.value) { err.value = recovery.value ? '请输入恢复码' : '请输入 6 位动态码'; return }
  busy.value = true
  try {
    const r = await api.post('/auth/login', { username: username.value, password: password.value, code: code.value })
    if (r.need_totp) {
      step.value = 'totp'
      nextTick(() => codeRef.value?.focus())
      return
    }
    state.me = null
    await loadMe()
    const next = typeof route.query.next === 'string' && route.query.next.startsWith('/') ? route.query.next : '/'
    router.replace(next)
  } catch (e) {
    err.value = e.message
    if (step.value === 'totp') code.value = ''
  } finally {
    busy.value = false
  }
}
function back() { step.value = 'password'; code.value = ''; err.value = ''; recovery.value = false }
</script>

<template>
  <div class="login-wrap">
    <div class="login-card">
      <div class="brand">
        <svg width="40" height="40" viewBox="0 0 32 32" aria-hidden="true"><rect width="32" height="32" rx="7" fill="#0f7b45" /><path d="M7 17h4l3-7 4 12 3-5h4" fill="none" stroke="#fff" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" /></svg>
        <div><b>VPS Panel</b><div class="muted small">自用节点管理</div></div>
      </div>

      <n-form v-if="step === 'password'" @submit.prevent="submit">
        <h1>登录</h1>
        <p class="muted">请输入管理员账号</p>
        <n-form-item label="用户名" :show-feedback="false" style="margin-bottom: 14px">
          <n-input v-model:value="username" size="large" :input-props="{ autocomplete: 'username', autocapitalize: 'off' }" autofocus />
        </n-form-item>
        <n-form-item label="密码" :show-feedback="false" style="margin-bottom: 14px">
          <n-input v-model:value="password" size="large" type="password" show-password-on="click" :input-props="{ autocomplete: 'current-password' }" @keyup.enter="submit" />
        </n-form-item>
        <div v-if="err" class="banner" role="alert" style="margin-bottom: 14px">{{ err }}</div>
        <n-button type="primary" size="large" block attr-type="submit" :loading="busy" @click="submit">登录</n-button>
      </n-form>

      <form v-else @submit.prevent="submit">
        <h1>两步验证</h1>
        <p class="muted">{{ recovery ? '输入一枚未用过的恢复码，用后作废' : '打开身份验证器 App，输入 6 位动态码' }}</p>
        <n-input ref="codeRef" v-model:value="code" size="large" :maxlength="recovery ? 32 : 6" class="code-input"
          :placeholder="recovery ? 'xxxx-xxxx' : '000000'"
          :input-props="{ inputmode: recovery ? 'text' : 'numeric', autocomplete: 'one-time-code', 'aria-label': recovery ? '恢复码' : '动态码' }"
          @keyup.enter="submit" />
        <div v-if="err" class="banner" role="alert" style="margin-top: 14px">{{ err }}</div>
        <div class="row" style="margin-top: 16px">
          <n-button size="large" @click="back">返回</n-button>
          <n-button type="primary" size="large" style="flex: 1" :loading="busy" attr-type="submit" @click="submit">验证并进入</n-button>
        </div>
        <button type="button" class="link-btn" style="margin-top: 14px" @click="recovery = !recovery; code = ''">
          {{ recovery ? '使用验证器动态码' : '无法使用验证器？用恢复码' }}
        </button>
      </form>

      <p class="muted small foot">
        新 IP 登录成功后会推送通知。忘记密码时，在面板所在 VPS 上执行
        <code class="mono">vps-panel reset-admin</code> 重置。
      </p>
    </div>
  </div>
</template>

<style scoped>
.login-wrap { min-height: 100vh; min-height: 100dvh; display: flex; align-items: center; justify-content: center; padding: 24px 16px calc(24px + var(--safe-bottom)); background: var(--c-bg); }
.login-card { width: 100%; max-width: 400px; background: #fff; border: 1px solid var(--c-border); border-radius: 8px; padding: 32px 28px; }
.brand { display: flex; align-items: center; gap: 12px; margin-bottom: 28px; font-size: 16px; }
h1 { font-size: 22px; margin: 0 0 4px; }
p { margin: 0 0 20px; }
.code-input :deep(input) { font-family: var(--mono); font-size: 22px; letter-spacing: 6px; text-align: center; }
.foot { margin: 24px 0 0; line-height: 1.6; }
@media (max-width: 767px) { .login-card { padding: 24px 18px; border: none; background: transparent; } }
</style>
