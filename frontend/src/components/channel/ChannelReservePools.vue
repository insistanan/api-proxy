<template>
  <div>
    <!-- 备用资源池 (disabled only) -->
    <div class="pt-2 pb-3">
      <div class="inactive-pool-header">
        <div class="text-subtitle-2 text-medium-emphasis d-flex align-center">
          <v-icon size="small" class="mr-1" color="grey">mdi-archive-outline</v-icon>
          备用资源池
          <v-chip size="x-small" class="ml-2">{{ inactiveChannels.length }}</v-chip>
        </div>
        <span class="section-inline-label">待启用</span>
      </div>

      <div v-if="inactiveChannels.length > 0" class="inactive-pool">
        <div v-for="channel in inactiveChannels" :key="channel.index" class="inactive-channel-row">
          <!-- 渠道信息 -->
          <div class="channel-info">
            <div class="channel-info-main">
              <span
                class="font-weight-medium channel-name-link"
                tabindex="0"
                role="button"
                @click="$emit('edit', channel)"
                @keydown.enter="$emit('edit', channel)"
                @keydown.space.prevent="$emit('edit', channel)"
              >{{ channel.name }}</span>
              <span class="text-caption text-disabled ml-2">{{ channelServiceLabel(channelType, channel.serviceType) }}</span>
              <v-chip v-if="channel.temporary" size="x-small" color="warning" variant="tonal" class="ml-2">
                临时 {{ formatDateTime(channel.temporaryUntil) }}
              </v-chip>
              <v-tooltip
                v-if="supportsVisionCapability && channel.visionCapable"
                text="支持图片理解"
                location="top"
                :open-delay="150"
                :open-on-focus="false"
              >
                <template #activator="{ props: tooltipProps }">
                  <v-chip
                    v-bind="tooltipProps"
                    size="x-small"
                    color="primary"
                    variant="flat"
                    class="ml-2 vision-default-chip"
                  >
                    <v-icon start size="12">mdi-image-search-outline</v-icon>
                    图片
                  </v-chip>
                </template>
              </v-tooltip>
            </div>
            <div class="channel-info-meta">
              <ChannelMappingBlock
                :channel="channel"
                :channel-type="channelType"
                :preview-limit="26"
                @edit="$emit('edit', channel)"
              />
            </div>
            <div v-if="channel.description" class="channel-info-desc text-caption text-disabled">
              {{ channel.description }}
            </div>
          </div>

          <!-- API密钥数量 -->
          <div class="channel-keys">
            <v-chip size="x-small" variant="outlined" color="grey" class="keys-chip" @click="$emit('edit', channel)">
              <v-icon start size="x-small">mdi-key</v-icon>
              {{ channel.apiKeys?.length || 0 }}
            </v-chip>
          </div>

          <!-- 操作按钮 -->
          <div class="channel-actions">
            <v-btn size="small" color="success" variant="tonal" @click="emit('enable', channel.index)">
              <v-icon start size="small">mdi-play-circle</v-icon>
              启用
            </v-btn>

            <v-menu>
              <template #activator="{ props: menuProps }">
                <v-btn icon size="x-small" variant="text" v-bind="menuProps">
                  <v-icon size="small">mdi-dots-vertical</v-icon>
                </v-btn>
              </template>
              <v-list density="compact">
                <v-list-item @click="$emit('edit', channel)">
                  <template #prepend>
                    <v-icon size="small">mdi-pencil</v-icon>
                  </template>
                  <v-list-item-title>编辑</v-list-item-title>
                </v-list-item>
                <v-list-item @click="emit('duplicate', channel.index)">
                  <template #prepend>
                    <v-icon size="small">mdi-content-copy</v-icon>
                  </template>
                  <v-list-item-title>复制渠道</v-list-item-title>
                </v-list-item>
                <v-list-item v-if="supportsVisionCapability" @click="emit('vision', channel)">
                  <template #prepend>
                    <v-icon size="small" :color="channel.visionCapable ? 'success' : 'primary'">
                      {{ channel.visionCapable ? 'mdi-check-circle' : 'mdi-image-search-outline' }}
                    </v-icon>
                  </template>
                  <v-list-item-title>
                    {{ channel.visionCapable ? '取消支持图片理解' : '设为支持图片理解' }}
                  </v-list-item-title>
                </v-list-item>
                <v-divider />
                <v-list-item @click="emit('enable', channel.index)">
                  <template #prepend>
                    <v-icon size="small" color="success">mdi-play-circle</v-icon>
                  </template>
                  <v-list-item-title>启用</v-list-item-title>
                </v-list-item>
                <v-list-item @click="emit('status', channel.index, 'deprecated')">
                  <template #prepend>
                    <v-icon size="small" color="grey">mdi-archive-clock-outline</v-icon>
                  </template>
                  <v-list-item-title>移至弃用池</v-list-item-title>
                </v-list-item>
                <v-list-item @click="$emit('delete', channel.index)">
                  <template #prepend>
                    <v-icon size="small" color="error">mdi-delete</v-icon>
                  </template>
                  <v-list-item-title>删除</v-list-item-title>
                </v-list-item>
              </v-list>
            </v-menu>
          </div>
        </div>
      </div>

      <div v-else class="text-center py-4 text-medium-emphasis text-caption">所有渠道都处于活跃状态</div>
    </div>

    <v-divider class="my-2" />

    <!-- 弃用渠道池 (deprecated only) -->
    <div class="pt-2 pb-3">
      <div class="inactive-pool-header">
        <div class="text-subtitle-2 text-medium-emphasis d-flex align-center">
          <v-icon size="small" class="mr-1" color="grey">mdi-archive-clock-outline</v-icon>
          弃用渠道池
          <v-chip size="x-small" class="ml-2">{{ deprecatedChannels.length }}</v-chip>
        </div>
        <span class="section-inline-label">待清理</span>
      </div>

      <div v-if="deprecatedChannels.length > 0" class="inactive-pool deprecated-pool">
        <div v-for="channel in deprecatedChannels" :key="channel.index" class="inactive-channel-row">
          <div class="channel-info">
            <div class="channel-info-main">
              <span
                class="font-weight-medium channel-name-link"
                tabindex="0"
                role="button"
                @click="$emit('edit', channel)"
                @keydown.enter="$emit('edit', channel)"
                @keydown.space.prevent="$emit('edit', channel)"
              >{{ channel.name }}</span>
              <span class="text-caption text-disabled ml-2">{{ channelServiceLabel(channelType, channel.serviceType) }}</span>
            </div>
            <div class="channel-info-meta">
              <ChannelMappingBlock
                :channel="channel"
                :channel-type="channelType"
                :preview-limit="26"
                @edit="$emit('edit', channel)"
              />
            </div>
            <div class="channel-info-desc text-caption text-disabled">
              弃用时间：{{ formatDateTime(channel.deprecatedAt) }}
            </div>
          </div>

          <div class="channel-keys">
            <v-chip size="x-small" variant="outlined" color="grey" class="keys-chip" @click="$emit('edit', channel)">
              <v-icon start size="x-small">mdi-key</v-icon>
              {{ channel.apiKeys?.length || 0 }}
            </v-chip>
          </div>

          <div class="channel-actions">
            <v-btn size="small" color="grey" variant="tonal" @click="emit('status', channel.index, 'disabled')">
              <v-icon start size="small">mdi-archive-outline</v-icon>
              转备用
            </v-btn>
            <v-btn size="small" color="error" variant="text" @click="$emit('delete', channel.index)">
              <v-icon start size="small">mdi-delete</v-icon>
              删除
            </v-btn>
          </div>
        </div>
      </div>

      <div v-else class="text-center py-4 text-medium-emphasis text-caption">暂无弃用渠道</div>
    </div>
  </div>
</template>

<script setup lang="ts">
// 备用与弃用资源池只负责展示和发出操作事件，配置写入由父级编排。
import type { Channel, ChannelStatus } from '@/types'
import type { ApiTab } from '@/services/channel-api'
import { channelServiceLabel } from '@/utils/imagesChannelProtocol'
import { formatDateTime } from '@/utils/channelMetricsFormat'
import ChannelMappingBlock from '../ChannelMappingBlock.vue'

defineProps<{
  inactiveChannels: Channel[]
  deprecatedChannels: Channel[]
  channelType: ApiTab
  supportsVisionCapability: boolean
}>()

const emit = defineEmits<{
  edit: [channel: Channel]
  delete: [index: number]
  enable: [index: number]
  duplicate: [index: number]
  vision: [channel: Channel]
  status: [index: number, status: ChannelStatus]
}>()

</script>

<style scoped src="../../styles/channel-orchestration.css"></style>
