import { defineStore } from 'pinia'
import { ref, shallowRef, computed } from 'vue'

const STORAGE_KEY = 'api-proxy-auth'

// 凭据恢复与验证分开；每次更换会话都取消上一代请求。
export const useAuthStore = defineStore('auth', () => {
  const apiKey = ref<string | null>(null)
  const verified = ref(false)
  const generation = ref(0)
  let controller = new AbortController()
  const requestSignal = shallowRef(controller.signal)
  const authError = ref('')
  const authAttempts = ref(0)
  const authLockoutTime = ref<number | null>(null)
  const isAutoAuthenticating = ref(true)
  const isInitialized = ref(false)
  const authLoading = ref(false)
  const authKeyInput = ref('')
  const isAuthenticated = computed(() => verified.value && !!apiKey.value)

  function persist() {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({ apiKey: apiKey.value, authAttempts: authAttempts.value, authLockoutTime: authLockoutTime.value }))
    } catch {
      throw new Error('无法保存认证状态，请检查浏览器存储权限')
    }
  }
  function invalidate() {
    verified.value = false
    controller.abort(new DOMException('认证会话已结束', 'AbortError'))
    controller = new AbortController()
    requestSignal.value = controller.signal
    generation.value++
  }
  function setApiKey(key: string | null) {
    invalidate()
    apiKey.value = key
  }
  function confirmAuthentication(expected: number) {
    if (generation.value !== expected || !apiKey.value) return false
    authAttempts.value = 0
    authLockoutTime.value = null
    persist()
    verified.value = true
    return true
  }
  function clearAuth() {
    setApiKey(null)
    authKeyInput.value = ''
    try { persist() } catch (error) { authError.value = (error as Error).message }
  }
  function initializeAuth() {
    try {
      const raw = localStorage.getItem(STORAGE_KEY)
      if (raw !== null) {
        const saved: unknown = JSON.parse(raw)
        if (!saved || typeof saved !== 'object' || !('apiKey' in saved) || (saved.apiKey !== null && typeof saved.apiKey !== 'string')) {
          throw new Error('已保存的认证状态格式无效，请重新登录')
        }
        apiKey.value = saved.apiKey
        if ('authAttempts' in saved && typeof saved.authAttempts === 'number' && Number.isSafeInteger(saved.authAttempts) && saved.authAttempts >= 0) authAttempts.value = saved.authAttempts
        if ('authLockoutTime' in saved && typeof saved.authLockoutTime === 'number' && Number.isFinite(saved.authLockoutTime)) authLockoutTime.value = saved.authLockoutTime
      } else {
        apiKey.value = localStorage.getItem('proxyAccessKey')
        persist()
      }
      // 旧格式只迁移一次，新格式始终是唯一事实源。
      localStorage.removeItem('proxyAccessKey')
      authKeyInput.value = apiKey.value ?? ''
    } catch (error) {
      apiKey.value = null
      authError.value = '恢复认证状态失败：' + (error instanceof Error ? error.message : String(error))
    }
  }
  function handleStorageChange(event: StorageEvent) {
    if (event.key !== STORAGE_KEY && event.key !== null) return
    try {
      const saved = event.newValue === null ? { apiKey: null } : JSON.parse(event.newValue)
      if (!saved || typeof saved !== 'object' || !('apiKey' in saved) || (saved.apiKey !== null && typeof saved.apiKey !== 'string')) {
        throw new Error('其他标签页保存的认证状态格式无效')
      }
      authAttempts.value = Number.isSafeInteger(saved.authAttempts) && saved.authAttempts >= 0 ? saved.authAttempts : 0
      authLockoutTime.value = typeof saved.authLockoutTime === 'number' && Number.isFinite(saved.authLockoutTime) ? saved.authLockoutTime : null
      // 仅锁定计数变化时，不打断其他标签页的有效会话。
      if (saved.apiKey === apiKey.value) return
      setApiKey(saved.apiKey)
      authKeyInput.value = saved.apiKey ?? ''
      authError.value = '认证状态已在其他标签页更改，请重新验证'
    } catch (error) {
      setApiKey(null)
      authError.value = error instanceof Error ? error.message : String(error)
    }
    authLoading.value = false
    isAutoAuthenticating.value = false
    isInitialized.value = true
  }
  // 墙上时钟不是响应式依赖，提交时实时计算。
  function lockoutSeconds() { return Math.max(0, Math.ceil(((authLockoutTime.value ?? 0) - Date.now()) / 1000)) }
  function setAuthError(error: string) { authError.value = error }
  function rejectAuthentication(automatic: boolean, maxAttempts: number) {
    apiKey.value = null
    if (!automatic) authAttempts.value++
    if (authAttempts.value >= maxAttempts) authLockoutTime.value = Date.now() + 5 * 60 * 1000
    persist()
  }
  function clearExpiredLockout() {
    if (authLockoutTime.value === null || lockoutSeconds() > 0) return
    authAttempts.value = 0
    authLockoutTime.value = null
    persist()
  }
  function setAutoAuthenticating(value: boolean) { isAutoAuthenticating.value = value }
  function setInitialized(value: boolean) { isInitialized.value = value }
  function setAuthLoading(value: boolean) { authLoading.value = value }
  function setAuthKeyInput(value: string) { authKeyInput.value = value }

  return { apiKey, generation, requestSignal, authError, authAttempts, authLockoutTime, isAutoAuthenticating, isInitialized, authLoading, authKeyInput, isAuthenticated,
    setApiKey, confirmAuthentication, clearAuth, initializeAuth, handleStorageChange, lockoutSeconds, setAuthError, rejectAuthentication, clearExpiredLockout, setAutoAuthenticating, setInitialized, setAuthLoading, setAuthKeyInput }
})
