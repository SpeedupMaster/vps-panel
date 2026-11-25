<script setup>
import { ref, computed, watch } from 'vue'
import { NButton, NInput, NInputNumber, NSelect, NDatePicker } from 'naive-ui'
import { api } from '../../api'
import { loadServers, ui } from '../../store'
import { run, isHttpUrl } from '../../helpers'
import { dt, daysUntil } from '../../format'
import { useDirtyForm } from '../../composables/useDirtyForm'

const props = defineProps({ sv: { type: Object, required: true } })
const emit = defineEmits(['saved'])

const asset = ref({})
const assetOrig = ref({})
const savingAsset = ref(false)
const countModes = [
  { label: '只计出站', value: 'out' }, { label: '只计入站', value: 'in' },
  { label: '双向合计', value: 'both' }, { label: '取出入较大值', value: 'max' },
]
const currencies = ['USD', 'CNY', 'EUR', 'HKD', 'JPY', 'GBP', 'CAD'].map((v) => ({ label: v, value: v }))
const cycles = [{ label: '月付', value: 'month' }, { label: '季付', value: 'quarter' }, { label: '半年付', value: 'half' }, { label: '年付', value: 'year' }, { label: '一次性', value: 'once' }]
const exceeded = [{ label: '仅告警（80% / 90% / 100%）', value: 'alert' }, { label: '告警并停止 Xray', value: 'stop_xray' }]

const assetDirty = (k) => (asset.value[k] ?? null) !== (assetOrig.value[k] ?? null)
const assetDirtyCount = computed(() => Object.keys(asset.value).filter(assetDirty).length)
useDirtyForm(() => assetDirtyCount.value > 0)

function fillAsset() {
  const s = props.sv
  if (!s) return
  asset.value = {
    name: s.name, group: s.group, tags: s.tags, region: s.region, country_code: s.country_code, provider: s.provider, provider_url: s.provider_url,
    price: s.price, currency: s.currency || 'USD', pay_cycle: s.pay_cycle || 'month', expire_at: s.expire_at ? new Date(s.expire_at).getTime() : null,
    note: s.note, bandwidth_mbps: s.bandwidth_mbps || 0, reset_day: s.reset_day || 1, quota_gb: s.quota_bytes ? +(s.quota_bytes / 1024 ** 3).toFixed(2) : 0,
    count_mode: s.count_mode || 'out', on_exceeded: s.on_exceeded || 'alert', sort: s.sort, mode: s.mode, cap_reality: s.cap_reality, cap_landing: s.cap_landing,
  }
  assetOrig.value = { ...asset.value }
}
watch(() => props.sv, fillAsset, { immediate: true })

async function saveAsset() {
  if (savingAsset.value) return
  const providerUrl = String(asset.value.provider_url || '').trim()
  if (providerUrl && !isHttpUrl(providerUrl)) { ui.message.warning('商家后台链接需以 http:// 或 https:// 开头'); return }
  savingAsset.value = true
  try {
    const a = { ...asset.value, provider_url: providerUrl, expire_at: asset.value.expire_at ? new Date(asset.value.expire_at).toISOString() : null }
    if (await run(() => api.put(`/servers/${props.sv.id}`, a), '已保存')) {
      assetOrig.value = { ...asset.value, provider_url: providerUrl }
      emit('saved')
      loadServers()
    }
  } finally { savingAsset.value = false }
}
</script>

<template>
  <div class="sections">
    <section class="s-group">
      <div class="s-head">
        <h2>基本信息</h2>
        <p>显示名称与分类。总览可以按分组筛选。创建于 {{ dt(sv.created_at, true) }}。</p>
      </div>
      <div class="card s-card">
        <div class="field"><div class="f-label"><span class="f-name">名称<i v-if="assetDirty('name')" class="f-dot" /></span></div><div class="f-ctl"><n-input v-model:value="asset.name" placeholder="服务器名称" /></div></div>
        <div class="field"><div class="f-label"><span class="f-name">分组<i v-if="assetDirty('group')" class="f-dot" /></span></div><div class="f-ctl"><n-input v-model:value="asset.group" placeholder="未分组" clearable /></div></div>
        <div class="field"><div class="f-label"><span class="f-name">标签<i v-if="assetDirty('tags')" class="f-dot" /></span><span class="f-hint">逗号分隔，可在总览里搜索</span></div><div class="f-ctl"><n-input v-model:value="asset.tags" placeholder="未设置" clearable /></div></div>
        <div class="field"><div class="f-label"><span class="f-name">排序<i v-if="assetDirty('sort')" class="f-dot" /></span><span class="f-hint">数字小的排在前面</span></div><div class="f-ctl"><n-input-number v-model:value="asset.sort" :show-button="false" placeholder="默认 0" /></div></div>
        <div class="field"><div class="f-label"><span class="f-name">地区<i v-if="assetDirty('region')" class="f-dot" /></span></div><div class="f-ctl"><n-input v-model:value="asset.region" placeholder="自动识别" clearable /></div></div>
        <div class="field"><div class="f-label"><span class="f-name">国家代码<i v-if="assetDirty('country_code')" class="f-dot" /></span><span class="f-hint">两位字母，用于显示国旗</span></div><div class="f-ctl"><n-input v-model:value="asset.country_code" maxlength="2" placeholder="例如 HK" /></div></div>
        <div class="field stacked"><div class="f-label"><span class="f-name">备注<i v-if="assetDirty('note')" class="f-dot" /></span></div><div class="f-ctl"><n-input v-model:value="asset.note" type="textarea" :autosize="{ minRows: 2, maxRows: 6 }" placeholder="未设置" /></div></div>
      </div>
    </section>

    <section class="s-group">
      <div class="s-head"><h2>服务商与费用</h2><p>用于总览里的月度花费和到期提醒。</p></div>
      <div class="card s-card">
        <div class="field"><div class="f-label"><span class="f-name">服务商<i v-if="assetDirty('provider')" class="f-dot" /></span></div><div class="f-ctl"><n-input v-model:value="asset.provider" placeholder="未设置" clearable /></div></div>
        <div class="field">
          <div class="f-label">
            <span class="f-name">商家后台链接<i v-if="assetDirty('provider_url')" class="f-dot" /></span>
            <span v-if="sv.provider_url" class="f-hint">
              <a v-if="isHttpUrl(sv.provider_url)" :href="sv.provider_url.trim()" target="_blank" rel="noopener noreferrer">打开商家后台</a>
              <template v-else>{{ sv.provider_url }}</template>
            </span>
          </div>
          <div class="f-ctl"><n-input v-model:value="asset.provider_url" placeholder="https://" clearable /></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">价格<i v-if="assetDirty('price') || assetDirty('currency')" class="f-dot" /></span></div>
          <div class="f-ctl ctl-row">
            <n-input-number v-model:value="asset.price" :min="0" :precision="2" :show-button="false" placeholder="未设置" />
            <n-select v-model:value="asset.currency" :options="currencies" tag filterable aria-label="币种" class="cur" />
          </div>
        </div>
        <div class="field"><div class="f-label"><span class="f-name">付费周期<i v-if="assetDirty('pay_cycle')" class="f-dot" /></span></div><div class="f-ctl"><n-select v-model:value="asset.pay_cycle" :options="cycles" /></div></div>
        <div class="field">
          <div class="f-label">
            <span class="f-name">到期日<i v-if="assetDirty('expire_at')" class="f-dot" /></span>
            <span v-if="daysUntil(sv.expire_at) !== null" class="f-hint">{{ daysUntil(sv.expire_at) }} 天后到期</span>
          </div>
          <div class="f-ctl"><n-date-picker v-model:value="asset.expire_at" type="date" clearable placeholder="未设置" /></div>
        </div>
      </div>
    </section>

    <section class="s-group">
      <div class="s-head"><h2>流量</h2><p>带宽、本周期流量的计算方式，以及超出额度后的动作。</p></div>
      <div class="card s-card">
        <div class="field">
          <div class="f-label"><span class="f-name">带宽<i v-if="assetDirty('bandwidth_mbps')" class="f-dot" /></span><span class="f-hint">商家标称端口速率，0 = 未设置</span></div>
          <div class="f-ctl"><n-input-number v-model:value="asset.bandwidth_mbps" :min="0" :precision="0" :show-button="false" placeholder="0"><template #suffix><span class="f-unit">Mbps</span></template></n-input-number></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">流量额度<i v-if="assetDirty('quota_gb')" class="f-dot" /></span><span class="f-hint">0 = 不限</span></div>
          <div class="f-ctl"><n-input-number v-model:value="asset.quota_gb" :min="0" :show-button="false" placeholder="0"><template #suffix><span class="f-unit">GB</span></template></n-input-number></div>
        </div>
        <div class="field">
          <div class="f-label"><span class="f-name">每月重置日<i v-if="assetDirty('reset_day')" class="f-dot" /></span></div>
          <div class="f-ctl"><n-input-number v-model:value="asset.reset_day" :min="1" :max="31" :show-button="false" placeholder="默认 1"><template #suffix><span class="f-unit">日</span></template></n-input-number></div>
        </div>
        <div class="field"><div class="f-label"><span class="f-name">计费口径<i v-if="assetDirty('count_mode')" class="f-dot" /></span></div><div class="f-ctl"><n-select v-model:value="asset.count_mode" :options="countModes" /></div></div>
        <div class="field"><div class="f-label"><span class="f-name">超额动作<i v-if="assetDirty('on_exceeded')" class="f-dot" /></span></div><div class="f-ctl"><n-select v-model:value="asset.on_exceeded" :options="exceeded" /></div></div>
      </div>
    </section>
  </div>
  <div class="savebar">
    <span v-if="assetDirtyCount" class="small dirty-note"><i class="f-dot" />{{ assetDirtyCount }} 项未保存</span>
    <span v-else class="small muted">修改后点保存</span>
    <span class="spacer" />
    <n-button :disabled="!assetDirtyCount" @click="fillAsset">撤销</n-button>
    <n-button type="primary" :loading="savingAsset" :disabled="!assetDirtyCount || savingAsset" @click="saveAsset">保存</n-button>
  </div>
</template>

<style scoped>
.ctl-row .cur { flex: none; width: 96px; }
</style>
