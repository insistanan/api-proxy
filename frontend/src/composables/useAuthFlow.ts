// useAuthFlow 认证流程 composable（R5，方案 8.1）：
// 把 App.vue 中的自动认证、手动提交、注销与认证失败处理收敛到这里，
// App.vue 只保留页面壳；状态本体在 authStore（唯一来源），本文件只做编排。
import { computed } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { useChannelStore } from '@/stores/channel'
import { ApiError, channelApiByType } from '@/services/api'

/** 认证尝试限制：超过后锁定 5 分钟。 */
export const MAX_AUTH_ATTEMPTS = 5

export function useAuthFlow(notify: (message: string, type: 'success' | 'error' | 'warning' | 'info') => void) {
  const authStore = useAuthStore()
  const channelStore = useChannelStore()

  const isAuthenticated = computed(() => authStore.isAuthenticated)

  // 认证对话框显示：初始化完成、未认证、不在自动认证中。
  const showAuthDialog = computed({
    get: () => authStore.isInitialized && !isAuthenticated.value && !authStore.isAutoAuthenticating,
    set: () => {} // 认证状态只能通过内部逻辑控制
  })

  // 自动验证保存的密钥。返回是否可继续（认证成功或网络类失败保留密钥）。
  const autoAuthenticate = async (): Promise<boolean> => {
    if (!authStore.apiKey) {
      authStore.setAuthError('请输入访问密钥以继续')
      authStore.setAutoAuthenticating(false)
      authStore.setInitialized(true)
      return false
    }

    try {
      await channelApiByType('messages').getChannels()
      authStore.setAuthError('')
      return true
    } catch (error) {
      // 仅在明确 401 时视为密钥无效；其他错误（网络/5xx）不应清除密钥。
      if (error instanceof ApiError && error.status === 401) {
        console.warn('自动认证失败: 认证失败(401)')
        authStore.clearAuth()
        authStore.setAuthError('保存的访问密钥已失效，请重新输入')
        return false
      }
      console.warn('自动认证暂时失败:', error)
      notify(`无法验证访问密钥: ${error instanceof Error ? error.message : '未知错误'}`, 'warning')
      // 非 401：保留密钥，继续尝试连接后端（后续刷新会更新系统状态）。
      return true
    } finally {
      authStore.setAutoAuthenticating(false)
      authStore.setInitialized(true)
    }
  }

  // 处理认证提交：验证密钥 → 加载数据 → 重置计数；401 计入失败与锁定。
  const handleAuthSubmit = async (): Promise<void> => {
    if (!authStore.authKeyInput.trim()) {
      authStore.setAuthError('请输入访问密钥')
      return
    }

    if (authStore.isAuthLocked) {
      const remainingSeconds = Math.ceil((authStore.authLockoutTime! - Date.now()) / 1000)
      authStore.setAuthError(`认证尝试次数过多，请在 ${remainingSeconds} 秒后重试`)
      return
    }

    authStore.setAuthLoading(true)
    authStore.setAuthError('')

    try {
      authStore.setApiKey(authStore.authKeyInput.trim())
      authStore.setAuthError('')
      await channelApiByType('messages').getChannels()

      authStore.resetAuthAttempts()
      authStore.setAuthLockout(null)
      await channelStore.refreshChannels()
      authStore.setAuthKeyInput('')

      if (import.meta.env.DEV) {
        console.info('✅ 认证成功 - 时间:', new Date().toISOString())
      }
    } catch (error) {
      // 仅在明确 401 时计入认证失败；网络/5xx 不计入失败次数，也不清除已保存密钥。
      if (error instanceof ApiError && error.status === 401) {
        authStore.incrementAuthAttempts()
        console.warn('🔒 认证失败 - 尝试次数:', authStore.authAttempts, '时间:', new Date().toISOString())

        if (authStore.authAttempts >= MAX_AUTH_ATTEMPTS) {
          authStore.setAuthLockout(new Date(Date.now() + 5 * 60 * 1000))
          authStore.setAuthError('认证尝试次数过多，请在5分钟后重试')
        } else {
          authStore.setAuthError(`访问密钥验证失败 (剩余尝试次数: ${MAX_AUTH_ATTEMPTS - authStore.authAttempts})`)
        }
        authStore.clearAuth()
        return
      }

      notify(`无法验证访问密钥: ${error instanceof Error ? error.message : '未知错误'}`, 'error')
    } finally {
      authStore.setAuthLoading(false)
    }
  }

  // 注销：清认证 + 清受保护数据。
  const handleLogout = (): void => {
    authStore.clearAuth()
    channelStore.clearChannels()
    authStore.setAuthError('请输入访问密钥以继续')
    notify('已安全注销', 'info')
  }

  // 认证失败（业务操作过程中 401）：提示重新输入。
  const handleAuthError = (error: unknown): void => {
    if (error instanceof Error && error.message.includes('认证失败')) {
      authStore.setAuthError('访问密钥无效或已过期，请重新输入')
    } else {
      notify(`操作失败: ${error instanceof Error ? error.message : '未知错误'}`, 'error')
    }
  }

  return {
    isAuthenticated,
    showAuthDialog,
    autoAuthenticate,
    handleAuthSubmit,
    handleLogout,
    handleAuthError
  }
}
