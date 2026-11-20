import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { ApiTab, Channel } from '@/types'

/**
 * 对话框状态管理 Store
 *
 * 职责：
 * - 管理添加/编辑渠道对话框状态
 * - 管理对话框相关的临时数据（编辑中的渠道等）
 */
export const useDialogStore = defineStore('dialog', () => {
  // ===== 状态 =====

  // 添加/编辑渠道对话框
  const showAddChannelModal = ref(false)
  const editingChannel = ref<Channel | null>(null)
  const copyingChannel = ref<Channel | null>(null)
  const copySourceType = ref<ApiTab>('messages')
  const channelType = ref<ApiTab>('messages')
  const initialPoolId = ref('default')

  // ===== 操作方法 =====

  /**
   * 打开添加渠道对话框
   */
  function openAddChannelModal(type: ApiTab, poolId = 'default') {
    channelType.value = type
    initialPoolId.value = poolId
    editingChannel.value = null
    copyingChannel.value = null
    showAddChannelModal.value = true
  }

  /**
   * 打开编辑渠道对话框
   */
  function openEditChannelModal(channel: Channel, type: ApiTab) {
    channelType.value = type
    initialPoolId.value = 'default'
    editingChannel.value = channel
    copyingChannel.value = null
    showAddChannelModal.value = true
  }

  /** 复制只打开独立草稿，保存时按目标协议创建新渠道。 */
  function openCopyChannelModal(channel: Channel, type: ApiTab) {
    channelType.value = type
    copySourceType.value = type
    initialPoolId.value = 'default'
    editingChannel.value = null
    copyingChannel.value = channel
    showAddChannelModal.value = true
  }

  /**
   * 关闭渠道对话框
   */
  function closeAddChannelModal() {
    showAddChannelModal.value = false
    editingChannel.value = null
    copyingChannel.value = null
    initialPoolId.value = 'default'
  }

  /**
   * 重置所有对话框状态
   */
  function resetDialogState() {
    showAddChannelModal.value = false
    editingChannel.value = null
    copyingChannel.value = null
    initialPoolId.value = 'default'
  }

  return {
    // 状态
    showAddChannelModal,
    editingChannel,
    copyingChannel,
    copySourceType,
    channelType,
    initialPoolId,

    // 方法
    openAddChannelModal,
    openEditChannelModal,
    openCopyChannelModal,
    closeAddChannelModal,
    resetDialogState,
  }
})
