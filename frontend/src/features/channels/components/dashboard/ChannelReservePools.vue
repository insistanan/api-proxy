<template>
  <div>
    <!-- 备用资源池 (disabled only) -->
    <DisclosureSection v-model="inactiveExpanded" title="备用渠道" description="暂不参与调度，启用后回到所属分组。" icon="mdi-archive-outline" :count="inactiveChannels.length" lazy>
      <div v-if="inactiveChannels.length > 0" class="inactive-pool">
        <div v-for="channel in inactiveChannels" :id="channel.id ? channelAnchorId(channelType, channel.id) : undefined" :key="channel.index" class="inactive-channel-row" tabindex="-1">
          <!-- 渠道信息 -->
          <div class="channel-info">
            <div class="channel-info-main">
              <span
                class="font-medium channel-name-link"
                tabindex="0"
                role="button"
                @click="$emit('edit', channel)"
                @keydown.enter="$emit('edit', channel)"
                @keydown.space.prevent="$emit('edit', channel)"
              >{{ channel.name }}</span>
              <ChannelWebsiteLink :channel="channel" />
              <span class="text-xs text-muted-foreground/65 ml-2">{{ channelServiceLabel(channelType, channel.serviceType) }}</span>
              <UiBadge v-if="channel.temporary" size="x-small" color="warning" variant="tonal" class="ml-2">
                临时 {{ formatDateTime(channel.temporaryUntil) }}
              </UiBadge>
              <UiTooltip
                v-if="channel.visionCapable"
                text="支持图片理解"
                location="top"
                :open-delay="150"
                :open-on-focus="false"
              >
                <template #activator="{ props: tooltipProps }">
                  <UiBadge
                    v-bind="tooltipProps"
                    size="x-small"
                    color="primary"
                    variant="flat"
                    class="ml-2 vision-default-chip"
                  >
                    <UiIcon start size="12">mdi-image-search-outline</UiIcon>
                    图片
                  </UiBadge>
                </template>
              </UiTooltip>
            </div>
            <div class="channel-info-meta">
              <ChannelMappingBlock
                :channel="channel"
                :channel-type="channelType"
                :preview-limit="26"
                @edit="$emit('edit', channel)"
              />
            </div>
            <div v-if="channel.description" class="channel-info-desc text-xs text-muted-foreground/65">
              {{ channel.description }}
            </div>
          </div>

          <!-- API密钥数量 -->
          <div class="channel-keys">
            <UiBadge size="x-small" variant="outlined" color="grey" class="keys-chip" @click="$emit('edit', channel)">
              <UiIcon start size="x-small">mdi-key</UiIcon>
              {{ channel.apiKeys?.length || 0 }}
            </UiBadge>
          </div>

          <!-- 操作按钮 -->
          <div class="channel-actions">
            <UiButton size="small" color="success" variant="tonal" @click="emit('enable', channel)">
              <UiIcon start size="small">mdi-play-circle</UiIcon>
              启用
            </UiButton>

            <UiMenu>
              <template #activator="{ props: menuProps }">
                <UiButton icon size="x-small" variant="text" v-bind="menuProps" :aria-label="`打开 ${channel.name} 的备用渠道菜单`">
                  <UiIcon size="small">mdi-dots-vertical</UiIcon>
                </UiButton>
              </template>
              <UiList density="compact">
                <UiListItem @click="$emit('edit', channel)">
                  <template #prepend>
                    <UiIcon size="small">mdi-pencil</UiIcon>
                  </template>
                  <span class="block">编辑</span>
                </UiListItem>
                <UiListItem @click="emit('duplicate', channel)">
                  <template #prepend>
                    <UiIcon size="small">mdi-content-copy</UiIcon>
                  </template>
                  <span class="block">复制渠道</span>
                </UiListItem>
                <UiListItem @click="emit('vision', channel)">
                  <template #prepend>
                    <UiIcon size="small" :color="channel.visionCapable ? 'success' : 'primary'">
                      {{ channel.visionCapable ? 'mdi-check-circle' : 'mdi-image-search-outline' }}
                    </UiIcon>
                  </template>
                  <span class="block">
                    {{ channel.visionCapable ? '取消支持图片理解' : '设为支持图片理解' }}
                  </span>
                </UiListItem>
                <hr class="border-0 border-t border-border" />
                <UiListItem @click="emit('enable', channel)">
                  <template #prepend>
                    <UiIcon size="small" color="success">mdi-play-circle</UiIcon>
                  </template>
                  <span class="block">启用</span>
                </UiListItem>
                <UiListItem @click="emit('status', channel, 'deprecated')">
                  <template #prepend>
                    <UiIcon size="small" color="grey">mdi-archive-clock-outline</UiIcon>
                  </template>
                  <span class="block">移至弃用池</span>
                </UiListItem>
                <UiListItem @click="$emit('delete', channel.index)">
                  <template #prepend>
                    <UiIcon size="small" color="error">mdi-delete</UiIcon>
                  </template>
                  <span class="block">删除</span>
                </UiListItem>
              </UiList>
            </UiMenu>
          </div>
        </div>
      </div>

      <div v-else class="text-center py-4 text-muted-foreground text-xs">所有渠道都处于活跃状态</div>
    </DisclosureSection>

    <!-- 弃用渠道池 (deprecated only) -->
    <DisclosureSection v-model="deprecatedExpanded" title="弃用渠道" description="可转为备用或手动删除；弃用超过 3 天后由后台清理。" icon="mdi-archive-clock-outline" :count="deprecatedChannels.length" lazy>
      <div v-if="deprecatedChannels.length > 0" class="inactive-pool deprecated-pool">
        <div v-for="channel in deprecatedChannels" :id="channel.id ? channelAnchorId(channelType, channel.id) : undefined" :key="channel.index" class="inactive-channel-row" tabindex="-1">
          <div class="channel-info">
            <div class="channel-info-main">
              <span
                class="font-medium channel-name-link"
                tabindex="0"
                role="button"
                @click="$emit('edit', channel)"
                @keydown.enter="$emit('edit', channel)"
                @keydown.space.prevent="$emit('edit', channel)"
              >{{ channel.name }}</span>
              <ChannelWebsiteLink :channel="channel" />
              <span class="text-xs text-muted-foreground/65 ml-2">{{ channelServiceLabel(channelType, channel.serviceType) }}</span>
            </div>
            <div class="channel-info-meta">
              <ChannelMappingBlock
                :channel="channel"
                :channel-type="channelType"
                :preview-limit="26"
                @edit="$emit('edit', channel)"
              />
            </div>
            <div class="channel-info-desc text-xs text-muted-foreground/65">
              弃用时间：{{ formatDateTime(channel.deprecatedAt) }}
            </div>
          </div>

          <div class="channel-keys">
            <UiBadge size="x-small" variant="outlined" color="grey" class="keys-chip" @click="$emit('edit', channel)">
              <UiIcon start size="x-small">mdi-key</UiIcon>
              {{ channel.apiKeys?.length || 0 }}
            </UiBadge>
          </div>

          <div class="channel-actions">
            <UiButton size="small" color="grey" variant="tonal" @click="emit('status', channel, 'disabled')">
              <UiIcon start size="small">mdi-archive-outline</UiIcon>
              转备用
            </UiButton>
            <UiButton size="small" color="error" variant="text" @click="$emit('delete', channel.index)">
              <UiIcon start size="small">mdi-delete</UiIcon>
              删除
            </UiButton>
          </div>
        </div>
      </div>

      <EmptyState v-else variant="inline" icon="mdi-archive-outline" title="暂无弃用渠道" description="停用或删除渠道时会先移入这里，可随时恢复。" />
    </DisclosureSection>
  </div>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiList from '@/shared/ui/navigation/List.vue'
import UiListItem from '@/shared/ui/navigation/ListItem.vue'
import UiMenu from '@/shared/ui/overlay/Menu.vue'
import UiTooltip from '@/shared/ui/overlay/Tooltip.vue'
// 备用与弃用资源池只负责展示和发出操作事件，配置写入由父级编排。
import type { Channel, ChannelStatus } from '@/types'
import { nextTick, ref, watch } from 'vue'
import { channelAnchorId } from '@/features/channels/utils/channelSearch'
import type { ApiTab } from '@/features/channels/services/channel-api'
import { channelServiceLabel } from '@/features/channels/utils/imagesChannelProtocol'
import { formatDateTime } from '@/features/channels/utils/channelMetricsFormat'
import ChannelMappingBlock from '@/features/channels/components/dashboard/ChannelMappingBlock.vue'
import ChannelWebsiteLink from '@/features/channels/components/dashboard/ChannelWebsiteLink.vue'
import DisclosureSection from '@/shared/components/layout/DisclosureSection.vue'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'

const props = defineProps<{
  inactiveChannels: Channel[]
  deprecatedChannels: Channel[]
  channelType: ApiTab
}>()

const inactiveExpanded = ref(false)
const deprecatedExpanded = ref(false)
watch(() => props.channelType, () => { inactiveExpanded.value = false; deprecatedExpanded.value = false })

async function revealChannel(channel: Channel) {
  if (channel.excludeFromConversation) return
  if (channel.status === 'disabled') inactiveExpanded.value = true
  if (channel.status === 'deprecated') deprecatedExpanded.value = true
  await nextTick()
}

defineExpose({ revealChannel })

const emit = defineEmits<{
  edit: [channel: Channel]
  delete: [index: number]
  enable: [channel: Channel]
  duplicate: [channel: Channel]
  vision: [channel: Channel]
  status: [channel: Channel, status: ChannelStatus]
}>()

</script>

<style scoped src="@/features/channels/styles/channel-orchestration.css"></style>
