// useChannelActions 渠道业务操作（R5，方案 8.1：App.vue 的渠道业务
// 逐步归入 store 与 composable，页面壳只保留导航和全局组件）。
// 数据操作经 channelStore（服务端数据唯一来源）；committed 错误
// （R3）在此统一消费：提示刷新实际状态、不自动重试。
import { useChannelStore } from '@/stores/channel'
import { useDialogStore } from '@/stores/dialog'
import { ApiError, type Channel } from '@/services/api'

type ToastFn = (message: string, type: 'success' | 'error' | 'warning' | 'info') => void

export function useChannelActions(notify: ToastFn) {
  const channelStore = useChannelStore()
  const dialogStore = useDialogStore()

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
    try {
      const result = await channelStore.saveChannel(channel, dialogStore.editingChannel?.index ?? null, options)
      notify(result.message, 'success')
      if (result.quickAddMessage) {
        notify(result.quickAddMessage, 'info')
      }
      dialogStore.closeAddChannelModal()
      await refreshChannels(onAuthError ?? (() => {}))
    } catch (error) {
      // 配置已过提交点（文件已写入，收尾失败）：不得自动重试，
      // 提示用户刷新查看实际状态（R3 committed 语义）。
      if (error instanceof ApiError && error.committed) {
        notify(`${error.message}（变更已写入，请刷新查看实际状态，请勿重复提交）`, 'warning')
        dialogStore.closeAddChannelModal()
        await refreshChannels(onAuthError ?? (() => {}))
        return
      }
      onAuthError?.(error)
    }
  }

  const deleteChannel = async (
    channelId: number,
    onAuthError?: (error: unknown) => void
  ): Promise<void> => {
    if (!confirm('确定要删除这个渠道吗？')) return

    try {
      const result = await channelStore.deleteChannel(channelId)
      notify(result.message, 'success')
    } catch (error) {
      if (error instanceof ApiError && error.committed) {
        notify(`${error.message}（删除已写入，请刷新查看实际状态，请勿重复提交）`, 'warning')
        return
      }
      onAuthError?.(error)
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
    refreshChannels,
    saveChannel,
    deleteChannel,
    pingChannel,
    pingAllChannels
  }
}
