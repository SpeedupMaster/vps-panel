<script setup>
// Shown when the panel can't be reached (the installed app opened without
// network, or the server is down). The session may still be valid, so this
// is not the login page: retry, and go back on our own once online again.
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton } from 'naive-ui'
import { loadMe } from '../store'

const route = useRoute()
const router = useRouter()
const busy = ref(false)
const err = ref('')
const next = () => (typeof route.query.next === 'string' && route.query.next.startsWith('/') ? route.query.next : '/')

async function retry() {
  if (busy.value) return
  busy.value = true
  err.value = ''
  try {
    await loadMe()
    router.replace(next())
  } catch (e) {
    if (e.status === 401) router.replace({ name: 'login', query: { next: next() } })
    else err.value = navigator.onLine ? '面板暂时无法访问，请稍后再试' : '设备当前没有网络'
  } finally {
    busy.value = false
  }
}

onMounted(() => window.addEventListener('online', retry))
onBeforeUnmount(() => window.removeEventListener('online', retry))
</script>

<template>
  <div class="off-wrap">
    <div class="off-card">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M2 8.8a15 15 0 0 1 4.2-2.6M9.5 5.2A15 15 0 0 1 22 8.8M5 12.5a10 10 0 0 1 3.6-2.1M15 10.4a10 10 0 0 1 4 2.1M8.5 16a5 5 0 0 1 7 0M12 20h.01M3 3l18 18" />
      </svg>
      <h1>连不上面板</h1>
      <p class="muted">网络恢复后会自动重新连接，登录状态不受影响。</p>
      <div v-if="err" class="banner" role="alert">{{ err }}</div>
      <n-button type="primary" size="large" block :loading="busy" @click="retry">重试</n-button>
    </div>
  </div>
</template>

<style scoped>
.off-wrap { min-height: 100vh; min-height: 100dvh; display: flex; align-items: center; justify-content: center; padding: 24px 16px calc(24px + var(--safe-bottom)); background: var(--c-bg); }
.off-card { width: 100%; max-width: 400px; text-align: center; color: var(--c-text-2, #555); }
h1 { font-size: 22px; margin: 16px 0 6px; color: var(--c-text, #111); }
p { margin: 0 0 20px; }
.banner { margin-bottom: 14px; text-align: left; }
</style>
