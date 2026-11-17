<script setup>
import { useMessage, useDialog, useNotification } from 'naive-ui'
import { ui } from '../store'

ui.message = useMessage()
ui.dialog = useDialog()
ui.notification = useNotification()
</script>

<template><span style="display: none" /></template>
