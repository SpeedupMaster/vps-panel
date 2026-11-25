<script setup>
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { LineChart, BarChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import VChart from 'vue-echarts'

use([CanvasRenderer, LineChart, BarChart, GridComponent, TooltipComponent, LegendComponent])

defineProps({ option: { type: Object, required: true }, height: { type: String, default: '240px' } })
</script>

<template>
  <v-chart :option="option" :style="{ height, width: '100%' }" autoresize :update-options="{ notMerge: true }" />
</template>
