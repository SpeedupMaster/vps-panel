import { createApp } from 'vue'
import App from './App.vue'
import router from './router'
import { setupPWA } from './pwa'
import './style.css'

createApp(App).use(router).mount('#app')
setupPWA()
