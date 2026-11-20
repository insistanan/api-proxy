<template>
  <UiMenu>
    <template #activator="{ props: menuProps }">
      <UiButton
        icon
        size="x-small"
        variant="text"
        title="打开快捷菜单"
        :aria-label="`打开 ${channel.name} 的快捷菜单`"
        v-bind="menuProps"
      >
        <UiIcon size="small">mdi-dots-vertical</UiIcon>
      </UiButton>
    </template>

    <UiList density="compact">
      <UiListItem @click="emit('action', 'edit')">
        <template #prepend><UiIcon size="small">mdi-pencil</UiIcon></template>
        <span class="block">编辑</span>
      </UiListItem>
      <UiListItem @click="emit('action', 'duplicate')">
        <template #prepend><UiIcon size="small">mdi-content-copy</UiIcon></template>
        <span class="block">复制渠道</span>
      </UiListItem>
      <UiListItem @click="emit('action', 'toggleVision')">
        <template #prepend>
          <UiIcon size="small" :color="channel.visionCapable ? 'success' : 'primary'">
            {{ channel.visionCapable ? 'mdi-check-circle' : 'mdi-image-search-outline' }}
          </UiIcon>
        </template>
        <span class="block">
          {{ channel.visionCapable ? '取消支持图片理解' : '设为支持图片理解' }}
        </span>
      </UiListItem>
      <UiListItem @click="emit('action', 'copyConfig')">
        <template #prepend>
          <UiIcon size="small" :color="copied ? 'success' : 'primary'">
            {{ copied ? 'mdi-check' : 'mdi-content-copy' }}
          </UiIcon>
        </template>
        <span class="block">{{ copied ? '已复制配置' : '复制配置' }}</span>
      </UiListItem>
      <UiListItem @click="emit('action', 'quickTest')">
        <template #prepend><UiIcon size="small" color="success">mdi-test-tube</UiIcon></template>
        <span class="block">快捷测试</span>
      </UiListItem>
      <UiListItem @click="emit('action', 'ping')">
        <template #prepend><UiIcon size="small">mdi-speedometer</UiIcon></template>
        <span class="block">测试延迟</span>
      </UiListItem>
      <UiListItem @click="emit('action', 'logs')">
        <template #prepend><UiIcon size="small">mdi-text-box-search-outline</UiIcon></template>
        <span class="block">请求日志</span>
      </UiListItem>
      <UiListItem @click="emit('action', 'promotion')">
        <template #prepend><UiIcon size="small" color="info">mdi-rocket-launch</UiIcon></template>
        <span class="block">抢优先级</span>
      </UiListItem>
      <UiListItem v-if="allowReorder && position > 0" @click="emit('moveTop')">
        <template #prepend><UiIcon size="small" color="primary">mdi-arrow-collapse-up</UiIcon></template>
        <span class="block">置顶</span>
      </UiListItem>
      <UiListItem v-if="allowReorder && position < total - 1" @click="emit('moveBottom')">
        <template #prepend><UiIcon size="small" color="primary">mdi-arrow-collapse-down</UiIcon></template>
        <span class="block">置底</span>
      </UiListItem>

      <hr class="border-0 border-t border-border" />
      <UiListItem v-if="channel.status === 'suspended'" @click="emit('action', 'resume')">
        <template #prepend><UiIcon size="small" color="success">mdi-play-circle</UiIcon></template>
        <span class="block">恢复 (重置指标)</span>
      </UiListItem>
      <UiListItem v-else @click="emit('action', 'suspend')">
        <template #prepend><UiIcon size="small" color="warning">mdi-pause-circle</UiIcon></template>
        <span class="block">暂停</span>
      </UiListItem>
      <UiListItem @click="emit('action', 'disable')">
        <template #prepend><UiIcon size="small" color="error">mdi-stop-circle</UiIcon></template>
        <span class="block">移至备用池</span>
      </UiListItem>
      <UiListItem @click="emit('action', 'deprecate')">
        <template #prepend><UiIcon size="small" color="grey">mdi-archive-clock-outline</UiIcon></template>
        <span class="block">移至弃用池</span>
      </UiListItem>
      <UiListItem @click="emit('delete')">
        <template #prepend>
          <UiIcon size="small" color="error">mdi-delete</UiIcon>
        </template>
        <span class="block">删除</span>
      </UiListItem>
    </UiList>
  </UiMenu>
</template>

<script lang="ts">
export const channelMenuActions = [
  'edit', 'duplicate', 'toggleVision', 'copyConfig', 'quickTest', 'ping',
  'logs', 'promotion', 'resume', 'suspend', 'disable', 'deprecate'
] as const
export type ChannelMenuAction = (typeof channelMenuActions)[number]
</script>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiList from '@/shared/ui/navigation/List.vue'
import UiListItem from '@/shared/ui/navigation/ListItem.vue'
import UiMenu from '@/shared/ui/overlay/Menu.vue'
import type { Channel } from '@/api'

withDefaults(defineProps<{
  channel: Channel
  position?: number
  total?: number
  copied?: boolean
  allowReorder?: boolean
}>(), {
  position: 0,
  total: 1,
  copied: false,
  allowReorder: true
})

// 通用操作统一走 action；排序和删除单独发出事件。
const emit = defineEmits<{
  action: [action: ChannelMenuAction]
  moveTop: []
  moveBottom: []
  delete: []
}>()
</script>
