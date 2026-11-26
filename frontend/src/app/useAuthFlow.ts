// 应用层组合认证和受保护数据；shared 不反向依赖业务 Store。
import { computed, watch } from 'vue'
import { useAuthStore } from '@/shared/stores/auth'
import { useToast } from '@/shared/composables/useToast'
import { useChannelStore } from '@/features/channels/stores/channel'
import { useDialogStore } from '@/features/channels/stores/dialog'
import { useIntentStore } from '@/features/channels/stores/intent'
import { useModelAvailabilityStore } from '@/features/channels/stores/modelAvailability'
import { useLogsStore } from '@/features/observability/stores/logs'
import { useDatasetsStore } from '@/features/extensions/datasets/stores/datasets'
import { useUsageJournal } from '@/features/usage-journal/store'
import { useForest } from '@/features/usage-journal/forest/store'
import { ApiError, channelApiByType } from '@/api'

export const MAX_AUTH_ATTEMPTS = 5

export function useAuthFlow(notify: (message: string, type: 'success' | 'error' | 'warning' | 'info') => void) {
  const authStore = useAuthStore()
  const channelStore = useChannelStore()
  const dialogStore = useDialogStore()
  const intentStore = useIntentStore()
  const availability = useModelAvailabilityStore()
  const logs = useLogsStore()
  const datasets = useDatasetsStore()
  const journal = useUsageJournal()
  const forest = useForest()
  const { clearToasts } = useToast()
  const isAuthenticated = computed(() => authStore.isAuthenticated)
  const showAuthDialog = computed({
    get: () => authStore.isInitialized && !isAuthenticated.value && !authStore.isAutoAuthenticating,
    set: () => {} // 认证对话框仅由会话状态关闭。
  })

  watch(() => authStore.generation, () => {
    clearToasts()
    channelStore.clearChannels()
    dialogStore.resetDialogState()
    intentStore.reset()
    availability.reset()
    logs.reset()
    datasets.reset()
    journal.reset()
    forest.reset()
  }, { flush: 'sync' })

  async function verify(key: string, automatic: boolean) {
    authStore.setApiKey(key)
    const generation = authStore.generation
    authStore.setAuthLoading(true)
    authStore.setAuthError('')
    try {
      const channels = await channelApiByType('messages').getChannels()
      if (!Array.isArray(channels.channels)) throw new Error('认证接口返回的渠道列表格式无效')
      if (generation !== authStore.generation) return false
      if (!authStore.confirmAuthentication(generation)) return false
      authStore.setAuthKeyInput('')
      return true
    } catch (error) {
      if (generation !== authStore.generation) return false
      if (error instanceof ApiError && error.status === 401) {
        try {
          authStore.rejectAuthentication(automatic, MAX_AUTH_ATTEMPTS)
        } catch (storageError) {
          authStore.setAuthError('访问密钥验证失败；' + (storageError instanceof Error ? storageError.message : String(storageError)))
          return false
        }
        if (authStore.authAttempts >= MAX_AUTH_ATTEMPTS) {
          authStore.setAuthError('认证尝试次数过多，请在 5 分钟后重试')
        } else {
          authStore.setAuthError(automatic ? '保存的访问密钥已失效，请重新输入' : '访问密钥验证失败（剩余 ' + (MAX_AUTH_ATTEMPTS - authStore.authAttempts) + ' 次）')
        }
      } else {
        authStore.setAuthError('无法验证访问密钥：' + (error instanceof Error ? error.message : String(error)))
      }
      return false
    } finally {
      if (generation === authStore.generation) {
        authStore.setAuthLoading(false)
        authStore.setAutoAuthenticating(false)
        authStore.setInitialized(true)
      }
    }
  }

  async function autoAuthenticate() {
    if (authStore.apiKey) return verify(authStore.apiKey, true)
    if (!authStore.authError) authStore.setAuthError('请输入访问密钥以继续')
    authStore.setAutoAuthenticating(false)
    authStore.setInitialized(true)
    return false
  }
  async function handleAuthSubmit() {
    if (authStore.authLoading) return
    const key = authStore.authKeyInput.trim()
    if (!key) { authStore.setAuthError('请输入访问密钥'); return }
    const remaining = authStore.lockoutSeconds()
    if (remaining) { authStore.setAuthError('认证尝试次数过多，请在 ' + remaining + ' 秒后重试'); return }
    try {
      authStore.clearExpiredLockout()
    } catch (error) {
      authStore.setAuthError(error instanceof Error ? error.message : String(error))
      return
    }
    await verify(key, false)
  }
  function handleLogout() {
    authStore.clearAuth()
    authStore.setAutoAuthenticating(false)
    authStore.setInitialized(true)
    notify('已注销', 'info')
  }
  return { isAuthenticated, showAuthDialog, autoAuthenticate, handleAuthSubmit, handleLogout }
}
