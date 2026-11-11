<template>
  <v-menu>
    <template #activator="{ props: menuProps }">
      <v-btn
        icon
        size="x-small"
        variant="text"
        title="打开快捷菜单"
        :aria-label="`打开 ${channel.name} 的快捷菜单`"
        v-bind="menuProps"
      >
        <v-icon size="small">mdi-dots-vertical</v-icon>
      </v-btn>
    </template>

    <v-list density="compact">
      <v-list-item @click="emit('action', 'edit')">
        <template #prepend><v-icon size="small">mdi-pencil</v-icon></template>
        <v-list-item-title>编辑</v-list-item-title>
      </v-list-item>
      <v-list-item @click="emit('action', 'duplicate')">
        <template #prepend><v-icon size="small">mdi-content-copy</v-icon></template>
        <v-list-item-title>复制渠道</v-list-item-title>
      </v-list-item>
      <v-list-item @click="emit('action', 'toggleVision')">
        <template #prepend>
          <v-icon size="small" :color="channel.visionCapable ? 'success' : 'primary'">
            {{ channel.visionCapable ? 'mdi-check-circle' : 'mdi-image-search-outline' }}
          </v-icon>
        </template>
        <v-list-item-title>
          {{ channel.visionCapable ? '取消支持图片理解' : '设为支持图片理解' }}
        </v-list-item-title>
      </v-list-item>
      <v-list-item @click="emit('action', 'copyConfig')">
        <template #prepend>
          <v-icon size="small" :color="copied ? 'success' : 'primary'">
            {{ copied ? 'mdi-check' : 'mdi-content-copy' }}
          </v-icon>
        </template>
        <v-list-item-title>{{ copied ? '已复制配置' : '复制配置' }}</v-list-item-title>
      </v-list-item>
      <v-list-item
        v-if="supportsCcs"
        aria-label="导入到 CCS"
        @click="emit('action', 'importCcs')"
      >
        <template #prepend><v-icon size="small" color="primary">mdi-application-export</v-icon></template>
        <v-list-item-title>导入到 CCS</v-list-item-title>
      </v-list-item>
      <v-list-item @click="emit('action', 'quickTest')">
        <template #prepend><v-icon size="small" color="success">mdi-test-tube</v-icon></template>
        <v-list-item-title>快捷测试</v-list-item-title>
      </v-list-item>
      <v-list-item @click="emit('action', 'ping')">
        <template #prepend><v-icon size="small">mdi-speedometer</v-icon></template>
        <v-list-item-title>测试延迟</v-list-item-title>
      </v-list-item>
      <v-list-item @click="emit('action', 'logs')">
        <template #prepend><v-icon size="small">mdi-text-box-search-outline</v-icon></template>
        <v-list-item-title>请求日志</v-list-item-title>
      </v-list-item>
      <v-list-item @click="emit('action', 'promotion')">
        <template #prepend><v-icon size="small" color="info">mdi-rocket-launch</v-icon></template>
        <v-list-item-title>抢优先级</v-list-item-title>
      </v-list-item>
      <v-list-item v-if="allowReorder && position > 0" @click="emit('moveTop')">
        <template #prepend><v-icon size="small" color="primary">mdi-arrow-collapse-up</v-icon></template>
        <v-list-item-title>置顶</v-list-item-title>
      </v-list-item>
      <v-list-item v-if="allowReorder && position < total - 1" @click="emit('moveBottom')">
        <template #prepend><v-icon size="small" color="primary">mdi-arrow-collapse-down</v-icon></template>
        <v-list-item-title>置底</v-list-item-title>
      </v-list-item>

      <v-divider />
      <v-list-item v-if="channel.status === 'suspended'" @click="emit('action', 'resume')">
        <template #prepend><v-icon size="small" color="success">mdi-play-circle</v-icon></template>
        <v-list-item-title>恢复 (重置指标)</v-list-item-title>
      </v-list-item>
      <v-list-item v-else @click="emit('action', 'suspend')">
        <template #prepend><v-icon size="small" color="warning">mdi-pause-circle</v-icon></template>
        <v-list-item-title>暂停</v-list-item-title>
      </v-list-item>
      <v-list-item @click="emit('action', 'disable')">
        <template #prepend><v-icon size="small" color="error">mdi-stop-circle</v-icon></template>
        <v-list-item-title>移至备用池</v-list-item-title>
      </v-list-item>
      <v-list-item @click="emit('action', 'deprecate')">
        <template #prepend><v-icon size="small" color="grey">mdi-archive-clock-outline</v-icon></template>
        <v-list-item-title>移至弃用池</v-list-item-title>
      </v-list-item>
      <v-list-item :disabled="!canDelete" @click="emit('delete')">
        <template #prepend>
          <v-icon size="small" :color="canDelete ? 'error' : 'grey'">mdi-delete</v-icon>
        </template>
        <v-list-item-title>
          删除
          <span v-if="!canDelete" class="text-caption text-disabled ml-1">(至少保留一个)</span>
        </v-list-item-title>
      </v-list-item>
    </v-list>
  </v-menu>
</template>

<script lang="ts">
export const channelMenuActions = [
  'edit', 'duplicate', 'toggleVision', 'copyConfig', 'importCcs', 'quickTest', 'ping',
  'logs', 'promotion', 'resume', 'suspend', 'disable', 'deprecate'
] as const
export type ChannelMenuAction = (typeof channelMenuActions)[number]
</script>

<script setup lang="ts">
import type { Channel } from '../services/api'

withDefaults(defineProps<{
  channel: Channel
  position?: number
  total?: number
  copied?: boolean
  canDelete?: boolean
  allowReorder?: boolean
  /** 该渠道类型是否支持导入 CC Switch（弹窗中选择目标应用类型） */
  supportsCcs?: boolean
}>(), {
  position: 0,
  total: 1,
  copied: false,
  canDelete: true,
  allowReorder: true,
  supportsCcs: false
})

// 不涉及排序与删除约束的操作统一走 action；排序和删除因两处用法规则不同而单独发出。
const emit = defineEmits<{
  action: [action: ChannelMenuAction]
  moveTop: []
  moveBottom: []
  delete: []
}>()
</script>
