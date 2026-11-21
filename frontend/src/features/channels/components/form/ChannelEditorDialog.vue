<template>
  <AddChannelModal
    v-model:channel-type="dialogStore.channelType"
    :show="dialogStore.showAddChannelModal"
    :channel="dialogStore.editingChannel"
    :copy-source="dialogStore.copyingChannel"
    :copy-source-type="dialogStore.copySourceType"
    :initial-pool-id="dialogStore.initialPoolId"
    :allow-protocol-change="dialogStore.allowProtocolChange"
    :saving="channelStore.isSavingChannel || channelStore.isDeletingChannel"
    @update:show="visible => { if (!visible) dialogStore.closeAddChannelModal() }"
    @save="handleSaveChannel"
  />
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'
import type { Channel } from '@/types'
import AddChannelModal from '@/features/channels/components/form/AddChannelModal.vue'
import { useChannelActions } from '@/features/channels/composables/useChannelActions'
import { useChannelStore } from '@/features/channels/stores/channel'
import { useDialogStore } from '@/features/channels/stores/dialog'
import { useToast } from '@/shared/composables/useToast'

const router = useRouter()
const channelStore = useChannelStore()
const dialogStore = useDialogStore()
const { showToast } = useToast()
const { saveChannel, refreshVisionChannelPool, handleAuthError } = useChannelActions(showToast)

async function handleSaveChannel(
  channel: Omit<Channel, 'id' | 'index' | 'latency'>,
  options?: { isQuickAdd?: boolean }
) {
  const copying = !!dialogStore.copyingChannel
  const targetType = dialogStore.channelType
  await saveChannel(channel, options, handleAuthError)
  await refreshVisionChannelPool()
  if (copying && !dialogStore.showAddChannelModal && targetType !== channelStore.activeTab) {
    try {
      await router.push(`/channels/${targetType}`)
    } catch (error) {
      showToast(`渠道已创建，但切换协议失败：${error instanceof Error ? error.message : '未知错误'}`, 'warning')
    }
  }
}
</script>
