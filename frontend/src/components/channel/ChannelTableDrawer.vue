<template>
  <v-dialog
    v-model="open"
    class="channel-table-drawer"
    transition="slide-x-reverse-transition"
    :aria-labelledby="titleId"
  >
    <template #activator="slotProps"><slot name="activator" v-bind="slotProps"></slot></template>
    <v-card class="model-list-sheet">
      <header class="drawer-header">
        <h2 :id="titleId" class="text-h6">{{ protocolLabel }} 模型列表</h2>
        <v-btn icon="mdi-close" variant="text" aria-label="关闭模型列表" @click="open = false" />
      </header>
      <ModelAvailabilityPanel v-if="open" :channel-type="channelType" :channels="channels" />
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import type { ApiTab } from '@/services/channel-api'
import type { Channel } from '@/types/channel'
import { findNavigation } from '@/router/navigation'
import ModelAvailabilityPanel from './ModelAvailabilityPanel.vue'

const props = defineProps<{ channelType: ApiTab; channels: Channel[] }>()
const titleId = useId()
const open = ref(false)
const protocolLabel = computed(() => findNavigation(`/channels/${props.channelType}`)?.label)

// 关闭抽屉即卸载观测面板，终止请求；切换协议后重新打开时使用独立筛选状态。
watch(() => props.channelType, () => { open.value = false })
</script>

<style scoped>
:global(.channel-table-drawer.v-dialog) { align-items: stretch; justify-content: flex-end; }
:global(.channel-table-drawer.v-dialog > .v-overlay__content) { width: min(1360px, 100vw); min-width: 0; max-width: 100vw; height: 100dvh; max-height: 100dvh; margin: 0; }
:global(.channel-table-drawer.v-dialog > .v-overlay__content > .model-list-sheet) { display: flex; flex-direction: column; height: 100%; min-width: 0; min-height: 0; border-radius: 0; overflow: hidden; }
:global(.channel-table-drawer > .slide-x-reverse-transition-enter-from),
:global(.channel-table-drawer > .slide-x-reverse-transition-leave-to) { transform: translateX(100%); }
.drawer-header { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 16px 16px 12px 24px; flex-shrink: 0; }
.drawer-header h2 { min-width: 0; overflow-wrap: anywhere; }
.drawer-header .v-btn { flex-shrink: 0; }
@media (max-width: 700px) {
  .drawer-header { padding: 12px 16px; }
}
@media (prefers-reduced-motion: reduce) {
  :global(.channel-table-drawer > .v-overlay__content) { transition: none !important; animation: none !important; }
}
</style>
