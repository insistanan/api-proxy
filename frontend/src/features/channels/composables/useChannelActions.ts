// useChannelActions 编排渠道页及全局渠道弹窗的业务操作。
// 已保存的渠道数据由 channelStore 管理，修改也通过 store 提交。
// committed 错误表示配置已提交，但后续处理失败；此处统一提示用户刷新
// 并查看实际状态，不自动重试，避免重复提交（R3）。
import { useChannelStore } from '@/features/channels/stores/channel'
import { useDialogStore } from '@/features/channels/stores/dialog'
import { ApiError, type ApiTab, type Channel } from '@/api'
import type { ToastType } from '@/shared/composables/useToast'
import { useAuthStore } from '@/shared/stores/auth'

type ToastFn = (message: string, type: ToastType) => void

export function useChannelActions(notify: ToastFn) {
  const channelStore = useChannelStore()
  const dialogStore = useDialogStore()
  const authStore = useAuthStore()
  const handleAuthError = (error: unknown) => {
    if (error instanceof Error && error.message.includes('认证失败')) {
      authStore.setAuthError('访问密钥无效或已过期，请重新输入')
    } else {
      notify(`操作失败: ${error instanceof Error ? error.message : '未知错误'}`, 'error')
    }
  }

  // 公用图片理解池与文本摘要池跨协议共享，任一协议的渠道修改都可能改变它们。
  const refreshVisionChannelPool = async () => {
    try {
      await Promise.all([channelStore.refreshVisionChannels(), channelStore.refreshSummaryChannels()])
    } catch (error) {
      handleAuthError(error)
    }
  }

  const reportError = (error: unknown, onAuthError?: (error: unknown) => void) => {
    if (onAuthError) onAuthError(error)
    else notify(error instanceof Error ? error.message : '渠道操作失败', 'error')
  }

  // 写操作已提交，刷新失败只能影响显示，不能被包装成写入失败诱导重试。
  const refreshAfterMutation = async (tab: ApiTab, onAuthError?: (error: unknown) => void) => {
    try {
      await channelStore.refreshChannels(tab)
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) onAuthError?.(error)
      notify(`变更已写入，但刷新渠道失败: ${error instanceof Error ? error.message : '未知错误'}。请刷新查看实际状态`, 'warning')
    }
  }

  const refreshChannels = async (onAuthError: (error: unknown) => void): Promise<void> => {
    try {
      await channelStore.refreshChannels()
    } catch (error) {
      onAuthError(error)
    }
  }

  const saveChannel = async (
    channel: Omit<Channel, 'id' | 'index' | 'latency'>,
    options?: { isQuickAdd?: boolean },
    onAuthError?: (error: unknown) => void
  ): Promise<void> => {
    if (channelStore.isSavingChannel || channelStore.isDeletingChannel) return
    const tab = dialogStore.channelType
    const editingIndex = dialogStore.editingChannel?.index ?? null
    const generation = authStore.generation
    channelStore.isSavingChannel = true
    try {
      const result = await channelStore.saveChannel(channel, editingIndex, { ...options, expectedChannelId: dialogStore.editingChannel?.id }, tab)
      if (generation !== authStore.generation) return
      notify(result.message, 'success')
      if (result.quickAddMessage) {
        notify(result.quickAddMessage, 'info')
      }
      if (result.warning) notify(result.warning, 'warning')
      dialogStore.closeAddChannelModal()
      await refreshAfterMutation(tab, onAuthError)
    } catch (error) {
      // 配置已过提交点（文件已写入，收尾失败）：不得自动重试，
      // 提示用户刷新查看实际状态。
      if (generation !== authStore.generation) return
      if (error instanceof ApiError && error.committed) {
        notify(`${error.message}（变更已写入，请刷新查看实际状态，请勿重复提交）`, 'warning')
        dialogStore.closeAddChannelModal()
        await refreshAfterMutation(tab, onAuthError)
        return
      }
      reportError(error, onAuthError)
    } finally {
      if (generation === authStore.generation) channelStore.isSavingChannel = false
    }
  }

  const deleteChannel = async (
    channelId: number,
    expectedId: string,
    onAuthError?: (error: unknown) => void,
    tab: ApiTab = channelStore.activeTab
  ): Promise<void> => {
    if (channelStore.isSavingChannel || channelStore.isDeletingChannel) return
    const generation = authStore.generation
    channelStore.isDeletingChannel = true
    try {
      const result = await channelStore.deleteChannel(channelId, expectedId, tab)
      if (generation !== authStore.generation) return
      notify(result.message, 'success')
      await refreshAfterMutation(tab, onAuthError)
    } catch (error) {
      if (generation !== authStore.generation) return
      if (error instanceof ApiError && error.committed) {
        notify(`${error.message}（删除已写入，请刷新查看实际状态，请勿重复提交）`, 'warning')
        await refreshAfterMutation(tab, onAuthError)
        return
      }
      reportError(error, onAuthError)
    } finally {
      if (generation === authStore.generation) channelStore.isDeletingChannel = false
    }
  }

  const pingChannel = async (channelId: number): Promise<void> => {
    try {
      await channelStore.pingChannel(channelId)
      // 不再使用 Toast，延迟结果直接显示在渠道列表中
    } catch (error) {
      notify(`延迟测试失败: ${error instanceof Error ? error.message : '未知错误'}`, 'error')
    }
  }

  const pingAllChannels = async (): Promise<void> => {
    try {
      await channelStore.pingAllChannels()
    } catch (error) {
      notify(`批量延迟测试失败: ${error instanceof Error ? error.message : '未知错误'}`, 'error')
    }
  }

  return {
    handleAuthError,
    refreshChannels,
    refreshVisionChannelPool,
    saveChannel,
    deleteChannel,
    pingChannel,
    pingAllChannels
  }
}
