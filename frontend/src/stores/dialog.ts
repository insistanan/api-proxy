import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { ApiTab, Channel } from '@/services/api'

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
  const channelType = ref<ApiTab>('messages')

  // ===== 操作方法 =====

  /**
   * 打开添加渠道对话框
   */
  function openAddChannelModal(type: ApiTab) {
    channelType.value = type
    editingChannel.value = null
    showAddChannelModal.value = true
  }

  /**
   * 打开编辑渠道对话框
   */
  function openEditChannelModal(channel: Channel, type: ApiTab) {
    channelType.value = type
    editingChannel.value = channel
    showAddChannelModal.value = true
  }

  /**
   * 关闭渠道对话框
   */
  function closeAddChannelModal() {
    showAddChannelModal.value = false
    editingChannel.value = null
  }

  /**
   * 重置所有对话框状态
   */
  function resetDialogState() {
    showAddChannelModal.value = false
    editingChannel.value = null
  }

  return {
    // 状态
    showAddChannelModal,
    editingChannel,
    channelType,

    // 方法
    openAddChannelModal,
    openEditChannelModal,
    closeAddChannelModal,
    resetDialogState,
  }
})
