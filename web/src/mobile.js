import { ref } from 'vue'

const mq = window.matchMedia('(max-width: 767px)')
export const isMobile = ref(mq.matches)
mq.addEventListener('change', (e) => { isMobile.value = e.matches })

// drawerWidth: full width on phones, the given width elsewhere.
export const drawerWidth = (w) => (isMobile.value ? '100%' : w)
