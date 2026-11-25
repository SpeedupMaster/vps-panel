<script setup>
import { ref, onMounted } from 'vue'
import { NTabs, NTabPane } from 'naive-ui'
import { api } from '../api'
import { state, loadServers } from '../store'
import { run } from '../helpers'
import { useQueryTab } from '../composables/useQueryTab'
import AlertEventsTab from './alerts/AlertEventsTab.vue'
import AlertRulesTab from './alerts/AlertRulesTab.vue'
import AlertChannelsTab from './alerts/AlertChannelsTab.vue'

const tab = useQueryTab(['events', 'rules', 'channels'], 'events')
const firing = ref(state.alertsFiring)
const channels = ref([])

async function loadChannels() { channels.value = (await run(() => api.get('/channels'))) || [] }

onMounted(() => {
  loadChannels()
  if (!state.serversLoaded) loadServers().catch(() => {})
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div class="ph"><h1>告警</h1><p class="page-desc"><template v-if="firing">{{ firing }} 条告警待确认。</template><template v-else>没有待确认的告警。</template>在这里管理触发规则和通知渠道。</p></div>
    </div>

    <n-tabs v-model:value="tab" type="line" animated>
      <n-tab-pane name="events" tab="事件">
        <alert-events-tab @update:firing="firing = $event" />
      </n-tab-pane>
      <n-tab-pane name="rules" tab="规则">
        <alert-rules-tab :channels="channels" />
      </n-tab-pane>
      <n-tab-pane name="channels" tab="通知渠道">
        <alert-channels-tab :channels="channels" @reload-channels="loadChannels" />
      </n-tab-pane>
    </n-tabs>
  </div>
</template>
