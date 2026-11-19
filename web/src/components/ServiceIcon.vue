<script setup>
// Official logo of a service in the unlock check. Logos: Iconify "logos"
// set (CC0) and Wikimedia Commons (Disney+); trademarks belong to their owners.
import { computed } from 'vue'
import netflix from '../assets/services/netflix.svg'
import youtube from '../assets/services/youtube.svg'
import openai from '../assets/services/openai.svg'
import claude from '../assets/services/claude.svg'
import gemini from '../assets/services/gemini.svg'
import disneyplus from '../assets/services/disneyplus.svg'
import tiktok from '../assets/services/tiktok.svg'

const props = defineProps({ name: { type: String, required: true } })
const logos = [
  [/^netflix/i, netflix], [/^youtube/i, youtube], [/^chatgpt|^openai/i, openai], [/^claude/i, claude],
  [/^gemini/i, gemini], [/^disney/i, disneyplus, 'wide'], [/^tiktok/i, tiktok],
]
const logo = computed(() => logos.find(([re]) => re.test(props.name)))
</script>

<template>
  <span class="si">
    <img v-if="logo" :src="logo[1]" :class="{ wide: logo[2] }" alt="" />
    <b v-else>{{ name.slice(0, 1) }}</b>
  </span>
</template>

<style scoped>
.si { width: 36px; height: 36px; border-radius: 8px; border: 1px solid var(--c-border); background: #fff; display: inline-flex; align-items: center; justify-content: center; flex: none; overflow: hidden; }
.si img { width: 22px; height: 22px; object-fit: contain; }
.si img.wide { width: 30px; }
.si b { font-size: 16px; font-weight: 700; color: var(--c-text-2); }
</style>
