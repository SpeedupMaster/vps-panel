import { toValue, onMounted, onBeforeUnmount } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { ask } from '../helpers'

/**
 * Guards unsaved form changes against accidental tab close (`beforeunload`)
 * and in-app route navigation (`onBeforeRouteLeave`).
 * @param {import('vue').MaybeRefOrGetter<boolean>} isDirty
 * @param {string} [message]
 */
export function useDirtyForm(isDirty, message = '当前页面有未保存的修改，离开后修改将会丢失，确定离开？') {
  const check = () => Boolean(toValue(isDirty))

  function onBeforeUnload(e) {
    if (!check()) return
    e.preventDefault()
    e.returnValue = ''
  }

  onMounted(() => {
    if (typeof window !== 'undefined') window.addEventListener('beforeunload', onBeforeUnload)
  })
  onBeforeUnmount(() => {
    if (typeof window !== 'undefined') window.removeEventListener('beforeunload', onBeforeUnload)
  })

  onBeforeRouteLeave(async () => {
    if (!check()) return true
    return await ask('放弃未保存的修改？', message, { danger: true, okText: '离开' })
  })
}
