<script setup>
defineProps({ d: { type: String, required: true }, size: { type: Number, default: 18 }, width: { type: Number, default: 1.8 } })
</script>

<template>
  <svg :width="size" :height="size" viewBox="0 0 24 24" fill="none" stroke="currentColor" :stroke-width="width"
    stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" style="flex: none"><path :d="d" /></svg>
</template>
