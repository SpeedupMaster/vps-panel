import { ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

/**
 * Syncs an active tab ref with a URL query parameter (defaults to `tab`).
 * Preserves deep-linked tabs across reloads and back/forward navigation.
 * @param {string[]} validTabs
 * @param {string} [defaultTab]
 * @param {string} [param]
 */
export function useQueryTab(validTabs, defaultTab = validTabs[0], param = 'tab') {
  const route = useRoute()
  const router = useRouter()
  const normalize = (v) => (typeof v === 'string' && validTabs.includes(v) ? v : defaultTab)
  const tab = ref(normalize(route.query[param]))

  watch(tab, (t) => {
    const next = normalize(t)
    if (normalize(route.query[param]) !== next) {
      router.replace({ query: { ...route.query, [param]: next === defaultTab ? undefined : next } })
    }
  })
  watch(() => route.query[param], (q) => {
    const next = normalize(q)
    if (tab.value !== next) tab.value = next
  })

  return tab
}
