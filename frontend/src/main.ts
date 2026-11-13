import { createApp } from 'vue'
import { createPinia } from 'pinia'
import piniaPluginPersistedstate from 'pinia-plugin-persistedstate'
import vuetify from './plugins/vuetify'
import router from './router'
import App from './App.vue'
import './assets/style.css'
import './assets/fonts.css'
import '@/features/clients/styles/agent-config.css'
import { useAuthStore } from '@/shared/stores/auth'

const app = createApp(App)

const pinia = createPinia()
pinia.use(piniaPluginPersistedstate)

app.use(pinia)
app.use(vuetify)
app.use(router)

// 初始化 AuthStore（从 localStorage 恢复状态）
const authStore = useAuthStore()
authStore.initializeAuth()

app.mount('#app')
