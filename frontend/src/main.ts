import { createApp } from 'vue'
import { createPinia } from 'pinia'
import piniaPluginPersistedstate from 'pinia-plugin-persistedstate'
import router from './router'
import App from './App.vue'
import '@/styles/index.css'
import { useAuthStore } from '@/shared/stores/auth'
import { installTheme } from '@/shared/theme'

const app = createApp(App)

const pinia = createPinia()
pinia.use(piniaPluginPersistedstate)

app.use(pinia)
app.onUnmount(installTheme(pinia))
app.use(router)

// 初始化 AuthStore（从 localStorage 恢复状态）
const authStore = useAuthStore()
authStore.initializeAuth()

app.mount('#app')
