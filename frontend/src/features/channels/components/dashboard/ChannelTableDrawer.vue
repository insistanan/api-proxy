<template>
  <UiDialog
    v-model="open"
    drawer
    :title="`${protocolLabel} 模型列表`"
  >
    <template #activator="slotProps"><slot name="activator" v-bind="slotProps"></slot></template>
    <UiCard class="flex h-full min-h-0 flex-col overflow-hidden rounded-none">
      <header class="flex shrink-0 items-center justify-between gap-4 px-4 py-3 md:pl-6">
        <h2 :id="titleId" class="text-lg">{{ protocolLabel }} 模型列表</h2>
        <UiButton icon="mdi-close" variant="text" aria-label="关闭模型列表" @click="open = false" />
      </header>
      <ModelAvailabilityPanel v-if="open" :channel-type="channelType" :channels="channels" />
    </UiCard>
  </UiDialog>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiDialog from '@/shared/ui/overlay/Dialog.vue'
import { computed, ref, useId, watch } from 'vue'
import type { ApiTab } from '@/features/channels/services/channel-api'
import type { Channel } from '@/types/channel'
import { findNavigation } from '@/router/navigation'
import ModelAvailabilityPanel from '@/features/channels/components/dashboard/ModelAvailabilityPanel.vue'

const props = defineProps<{ channelType: ApiTab; channels: Channel[] }>()
const titleId = useId()
const open = ref(false)
const protocolLabel = computed(() => findNavigation(`/channels/${props.channelType}`)?.label)

// 关闭抽屉即卸载观测面板，终止请求；切换协议后重新打开时使用独立筛选状态。
watch(() => props.channelType, () => { open.value = false })
</script>
