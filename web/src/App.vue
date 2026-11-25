<script setup>
import { ref, nextTick } from 'vue'
import { NConfigProvider, NMessageProvider, NDialogProvider, NNotificationProvider, NModal, NInput, NButton, zhCN, dateZhCN } from 'naive-ui'
import UiBridge from './components/UiBridge.vue'
import { setConfirmHook } from './api'
import { state } from './store'

const theme = {
  common: {
    primaryColor: '#0f7b45', primaryColorHover: '#0a5c33', primaryColorPressed: '#0a5c33', primaryColorSuppl: '#0f7b45',
    errorColor: '#b4233a', errorColorHover: '#9a1d31', errorColorPressed: '#9a1d31',
    warningColor: '#b8650a', infoColor: '#2f6fd0', successColor: '#0f7b45',
    fontFamily: "Inter, 'Noto Sans SC', system-ui, -apple-system, 'Segoe UI', 'PingFang SC', 'Microsoft YaHei', sans-serif",
    fontFamilyMono: "'JetBrains Mono', ui-monospace, Menlo, Consolas, monospace",
    borderRadius: '4px', textColorBase: '#1f2225', textColor1: '#1f2225', textColor2: '#3d4248', textColor3: '#5f6368',
    borderColor: '#d5d8dd', dividerColor: '#e4e6ea',
  },
  Card: { borderColor: '#e4e6ea', borderRadius: '6px' },
  DataTable: { thColor: '#fff', thTextColor: '#5f6368', thFontWeight: '500' },
  Menu: { itemHeight: '40px' },
}

// Sensitive actions: ask for a TOTP code (or the password when 2FA is off).
const show = ref(false)
const code = ref('')
const hint = ref('')
const inputRef = ref(null)
let resolver = null
setConfirmHook((why) => new Promise((resolve) => {
  if (resolver) { const prev = resolver; resolver = null; prev(null) }
  resolver = resolve
  code.value = ''
  hint.value = why
  show.value = true
  nextTick(() => inputRef.value?.focus())
}))
function done(v) {
  show.value = false
  const r = resolver; resolver = null
  r && r(v)
}
</script>

<template>
  <n-config-provider :theme-overrides="theme" :locale="zhCN" :date-locale="dateZhCN">
    <n-notification-provider placement="top-right">
      <n-message-provider placement="top">
        <n-dialog-provider>
          <ui-bridge />
          <router-view />
          <n-modal :show="show" preset="card" :title="state.me?.totp ? '输入两步验证码' : '输入登录密码'" style="width: min(420px, 94vw)"
            :mask-closable="false" @close="done(null)" @update:show="(v) => !v && done(null)">
            <p class="muted" style="margin-top: 0">{{ hint || '这是敏感操作，需要再次确认身份。' }}</p>
            <n-input ref="inputRef" v-model:value="code" :type="state.me?.totp ? 'text' : 'password'" :maxlength="state.me?.totp ? 6 : 128"
              :input-props="{ inputmode: state.me?.totp ? 'numeric' : 'text', autocomplete: state.me?.totp ? 'one-time-code' : 'current-password' }"
              :placeholder="state.me?.totp ? '6 位验证码' : '密码'" @keyup.enter="code && done(code)" />
            <template #footer>
              <div class="row" style="justify-content: flex-end">
                <n-button @click="done(null)">取消</n-button>
                <n-button type="primary" :disabled="!code" @click="done(code)">确认</n-button>
              </div>
            </template>
          </n-modal>
        </n-dialog-provider>
      </n-message-provider>
    </n-notification-provider>
  </n-config-provider>
</template>
