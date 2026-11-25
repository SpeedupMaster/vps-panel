import { ref } from 'vue'

const mq = typeof window !== 'undefined' && typeof window.matchMedia === 'function'
  ? window.matchMedia('(max-width: 767px)')
  : null
export const isMobile = ref(mq ? mq.matches : false)
if (mq) mq.addEventListener('change', (e) => { isMobile.value = e.matches })

// drawerWidth: full width on phones, the given width elsewhere.
export const drawerWidth = (w) => (isMobile.value ? '100%' : w)
