<template>
  <div class="channel-orchestration">
    <ChannelPoolGrid
      :channels="channels"
      :channel-type="channelType"
      :shared-vision-channels="visionChannels"
      @add-channel="$emit('addChannel', $event)"
      @edit="$emit('edit', $event)"
      @delete="handlePoolDelete"
      @error="$emit('error', $event)"
      @success="$emit('success', $event)"
      @refresh="$emit('refresh')"
      @toggle-vision-status="toggleChannelPrimaryStatus"
    >
      <template #heading>
        <v-chip size="x-small" :color="isMultiChannelMode ? 'success' : 'warning'" variant="tonal">
          {{ isMultiChannelMode ? '多渠道模式' : '单渠道模式' }}
        </v-chip>
      </template>
      <template #free-summary="{ channel }">
        <span class="channel-concurrency text-caption" title="当前在免费渠道处理中的独立对话数">
          并发对话 <strong>{{ getChannelMetrics(channel.index)?.activeConversations ?? '—' }}</strong>
        </span>
      </template>
      <template #free-activity="{ channel }">
        <ChannelActivityChart
          :bars="getActivityBars(channel.index)"
          :channel-id="channel.index"
          :channel-type="channelType"
        />
      </template>
      <template #channel="{ channel: element, index, pool, move_to_top, move_to_bottom }">
        <div class="channel-item-wrapper">
          <div
            class="channel-row"
            :class="{ 'is-suspended': element.status === 'suspended' }"
            @click="toggleChannelChart(element.index)"
          >
            <div class="channel-row-main">
              <div class="drag-handle" title="拖动调整顺序" @click.stop>
                <v-icon size="small">mdi-drag-vertical</v-icon>
              </div>
              <span class="priority-number" title="故障转移优先级">{{ index + 1 }}</span>
              <div class="channel-name">
                <button
                  type="button"
                  class="channel-name-link"
                  :title="element.name"
                  :aria-label="`编辑渠道 ${element.name}`"
                  @click.stop="$emit('edit', element)"
                >{{ element.name }}</button>
                <button
                  type="button"
                  class="channel-status-toggle"
                  :title="shouldResumeChannel(element) ? '恢复活跃并重置熔断' : '切换为熔断'"
                  :aria-label="`${shouldResumeChannel(element) ? '恢复' : '暂停'} ${element.name}`"
                  @click.stop="toggleChannelPrimaryStatus(element)"
                >
                  <ChannelStatusBadge size="small" :status="element.status || 'active'" :metrics="getChannelMetrics(element.index)" />
                </button>
                <v-chip v-if="isInPromotion(element)" size="x-small" color="info" variant="tonal" title="促销期">
                  <v-icon start size="12">mdi-rocket-launch</v-icon>
                  {{ formatPromotionRemaining(element.promotionUntil, element.promotionCount) }}
                </v-chip>
                <v-chip v-if="element.temporary" size="x-small" color="warning" variant="tonal" :title="`临时渠道 · ${formatDateTime(element.temporaryUntil)} 到期`">
                  临时
                </v-chip>
                <v-icon v-if="element.visionCapable" size="14" class="channel-vision-icon" title="支持图片理解" aria-label="支持图片理解">
                  mdi-image-search-outline
                </v-icon>
              </div>
              <div class="channel-actions" @click.stop>
                <v-btn
                  size="x-small"
                  variant="text"
                  class="channel-keys"
                  :title="`${element.apiKeys?.length || 0} 个密钥`"
                  :aria-label="`编辑 ${element.name} 的 ${element.apiKeys?.length || 0} 个密钥`"
                  @click="$emit('edit', element)"
                ><v-icon start size="12">mdi-key</v-icon>{{ element.apiKeys?.length || 0 }}</v-btn>
                <v-btn
                  :icon="expandedChannelIndex === element.index ? 'mdi-chevron-up' : 'mdi-chevron-down'"
                  size="x-small"
                  variant="text"
                  :color="expandedChannelIndex === element.index ? 'primary' : undefined"
                  :aria-label="`${expandedChannelIndex === element.index ? '收起' : '展开'} ${element.name} 的密钥趋势`"
                  :aria-expanded="expandedChannelIndex === element.index"
                  @click="toggleChannelChart(element.index)"
                />
                <ChannelQuickMenu
                  :channel="element"
                  :position="index"
                  :total="pool.channels.length"
                  :copied="copiedChannelIndex === element.index"
                  @action="runMenuAction(element, $event)"
                  @move-top="move_to_top()"
                  @move-bottom="move_to_bottom()"
                  @delete="handleDeleteChannel(element)"
                />
              </div>
            </div>

            <div class="channel-row-meta" @click.stop>
              <div class="channel-route">
                <ChannelMappingBlock :channel="element" :channel-type="channelType" :preview-limit="32" @edit="$emit('edit', element)" />
                <a
                  class="channel-service"
                  :href="getWebsiteUrl(element)"
                  target="_blank"
                  rel="noopener"
                  :title="`打开 ${element.name} 的官网${element.description ? ' · ' + element.description : ''}`"
                >{{ channelServiceLabel(channelType, element.serviceType) }}</a>
              </div>
              <div class="channel-live-status">
                <span
                  v-if="showsConversations"
                  class="channel-concurrency"
                  :class="{ 'has-conversations': (getChannelMetrics(element.index)?.activeConversations ?? 0) > 0 }"
                  title="当前在此渠道处理中的独立对话数"
                >
                  并发对话 <strong>{{ getChannelMetrics(element.index)?.activeConversations ?? '—' }}</strong>
                </span>
                <span v-if="isLatencyValid(element, currentTime)" :class="`text-${getLatencyColor(element.latency!)}`" title="最近探测延迟">{{ element.latency }} ms</span>
              </div>
            </div>

            <div v-if="getChannelMetrics(element.index)?.timeWindows?.['15m']?.requestCount || hasActivityData(element.index)" class="channel-telemetry" @click.stop>
              <ChannelMetricsCell :metrics="getChannelMetrics(element.index)" />
              <div class="channel-throughput" title="最近 15 分钟平均每分钟请求数 / Token 数">
                <span>{{ formatRPM(element.index) }} <small>RPM</small> · {{ formatTPM(element.index) }} <small>TPM</small></span>
                <span class="channel-traffic-count">15 分钟 · {{ getChannelMetrics(element.index)?.timeWindows?.['15m']?.requestCount ?? '—' }} 次请求</span>
              </div>
              <div class="channel-activity" role="img" aria-label="最近 15 分钟请求活跃度，绿色表示成功，暖色表示存在失败">
                <ChannelActivityChart
                  class="channel-sparkline"
                  :bars="getActivityBars(element.index)"
                  :channel-id="element.index"
                  :channel-type="channelType"
                />
              </div>
            </div>
          </div>

          <!-- 展开的图表区域 -->
          <v-expand-transition>
            <div v-if="expandedChannelIndex === element.index" class="channel-chart-wrapper">
              <KeyTrendChart
                :key="`chart-${channelType}-${element.index}`"
                :channel-id="element.index"
                :channel-type="channelType"
                @close="expandedChannelIndex = null"
              />
            </div>
          </v-expand-transition>
        </div>
      </template>

      <template #vision-actions="{ channel }">
        <ChannelQuickMenu
          :channel="channel"
          :copied="copiedChannelIndex === channel.index"
          :allow-reorder="false"
          @action="runMenuAction(channel, $event)"
          @delete="$emit('delete', channel.index, channel.kind)"
        />
      </template>
    </ChannelPoolGrid>

    <v-divider class="my-2" />

    <ChannelReservePools
      :inactive-channels="inactiveChannels"
      :deprecated-channels="deprecatedChannels"
      :channel-type="channelType"
      @edit="emit('edit', $event)"
      @delete="emit('delete', $event)"
      @enable="resumeChannel"
      @duplicate="duplicateChannel"
      @vision="toggleVisionCapability"
      @status="setChannelStatus"
    />
  </div>

    <ChannelPromotionDialog :editor="promotionEditor" />
    <ChannelLogsDialog :viewer="logsViewer" :channel-type="channelType" />

    <!-- 快捷测试弹窗 -->
    <QuickTestModal
      v-model="showQuickTestModal"
      :channel="quickTestChannel"
      :api-type="quickTestType"
    />

</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { api, ApiError, channelApiByType, type ApiTab, type Channel, type ChannelMetrics, type ChannelStatus, type ChannelRecentActivity, type VisionChannel } from '@/api'
import { useChannelActivity } from '@/features/channels/composables/useChannelActivity'
import { useClipboard } from '@/shared/composables/useClipboard'
import { defineLazyChart } from '@/shared/utils/lazyChart'
import { channelServiceLabel } from '@/features/channels/utils/imagesChannelProtocol'
import {
  getLatencyColor,
  isLatencyValid,
  isInPromotion,
  formatPromotionRemaining,
  formatDateTime,
  getWebsiteUrl
} from '@/features/channels/utils/channelMetricsFormat'
import ChannelStatusBadge from '@/features/channels/components/dashboard/ChannelStatusBadge.vue'
import ChannelActivityChart from '@/features/channels/components/dashboard/ChannelActivityChart.vue'
import ChannelPoolGrid from '@/features/channels/components/dashboard/ChannelPoolGrid.vue'
import ChannelQuickMenu, { type ChannelMenuAction } from '@/features/channels/components/dashboard/ChannelQuickMenu.vue'
import ChannelMappingBlock from '@/features/channels/components/dashboard/ChannelMappingBlock.vue'
import QuickTestModal from '@/features/channels/components/dashboard/QuickTestModal.vue'
import ChannelPromotionDialog from '@/features/channels/components/dashboard/ChannelPromotionDialog.vue'
import ChannelLogsDialog from '@/features/channels/components/dashboard/ChannelLogsDialog.vue'
import ChannelReservePools from '@/features/channels/components/dashboard/ChannelReservePools.vue'
import ChannelMetricsCell from '@/features/channels/components/dashboard/ChannelMetricsCell.vue'
import { useChannelLogs } from '@/features/channels/composables/useChannelLogs'
import { useChannelPromotion } from '@/features/channels/composables/useChannelPromotion'

const KeyTrendChart = defineLazyChart(() => import('@/features/channels/components/dashboard/KeyTrendChart.vue'))

const props = defineProps<{
  channels: Channel[]
  channelType: ApiTab
  // 指标与活跃度由父级随 Dashboard 快照整体替换，本组件只读。
  dashboardMetrics: ChannelMetrics[]
  dashboardRecentActivity?: ChannelRecentActivity[]
  // 四个会话协议共享的公用图片理解池，渠道自带所属协议。
  visionChannels?: VisionChannel[]
}>()

const emit = defineEmits<{
  (_e: 'addChannel', _poolId: string): void
  (_e: 'edit', _channel: Channel): void
  (_e: 'duplicate', _channel: Channel): void
  (_e: 'delete', _channelId: number, _kind?: ApiTab): void
  (_e: 'ping', _channelId: number): void
  (_e: 'refresh'): void
  (_e: 'error', _message: string): void
  (_e: 'success', _message: string): void
}>()

// 共享图片理解渠道可能来自其他协议；所有写操作必须回到渠道自身协议。
const kindOf = (channel: Channel): ApiTab => channel.kind ?? props.channelType
const handlePoolDelete = (channelId: number, kind?: ApiTab) => emit('delete', channelId, kind)

// 快捷测试弹窗状态
const showQuickTestModal = ref(false)
const quickTestChannel = ref<Channel | null>(null)
const quickTestType = ref<ApiTab>(props.channelType)

const { copyText } = useClipboard()

const logsViewer = useChannelLogs(props)
const { openLogsDialog } = logsViewer

const promotionEditor = useChannelPromotion(props, emit)
// 促销草稿在此解构到组件作用域，特性测试通过 setup 状态观察同一份编辑状态。
const {
  openPromotionDialog, confirmPromotion, promotionDuration, promotionCount,
  promotionError, promotionChannel, showPromotionDialog
} = promotionEditor

// 用于触发响应式更新的时间戳
const currentTime = ref(Date.now())
let latencyCheckTimer: ReturnType<typeof setInterval> | null = null

// 活跃度的柱状派生计算收敛在 useChannelActivity（R5，方案 8.1）。
const { getActivityBars, formatRPM, formatTPM, hasActivityData } =
  useChannelActivity(computed(() => props.dashboardRecentActivity ?? []))

// 图表展开状态
const expandedChannelIndex = ref<number | null>(null)

// 切换渠道图表展开/收起
const toggleChannelChart = (channelIndex: number) => {
  expandedChannelIndex.value = expandedChannelIndex.value === channelIndex ? null : channelIndex
}

const handleQuickTest = (channel: Channel) => {
  quickTestChannel.value = channel
  quickTestType.value = kindOf(channel)
  showQuickTestModal.value = true
}

// 复制渠道配置到剪贴板
const copiedChannelIndex = ref<number | null>(null)
let copyStatusTimer: ReturnType<typeof setTimeout> | undefined
const copyChannelConfig = async (channel: Channel) => {
  // 获取第一个密钥
  const firstKey = channel.apiKeys.length > 0 ? channel.apiKeys[0] : ''
  // 构建配置文本
  const configText = `名称: ${channel.name}
URL: ${channel.baseUrl}
密钥: ${firstKey}`
  // 复制到剪贴板
  if (!await copyText(configText)) {
    emit('error', '复制配置失败：无法访问剪贴板')
    return
  }
  // 显示已复制状态
  copiedChannelIndex.value = channel.index
  if (copyStatusTimer) clearTimeout(copyStatusTimer)
  copyStatusTimer = setTimeout(() => {
    copiedChannelIndex.value = null
  }, 2000)
}

// 计算属性：非活跃渠道 - 仅 disabled 状态
const inactiveChannels = computed(() => {
	return props.channels.filter(ch => !ch.builtinProvider && !ch.excludeFromConversation && ch.status === 'disabled')
})

const deprecatedChannels = computed(() => {
	return props.channels.filter(ch => !ch.builtinProvider && !ch.excludeFromConversation && ch.status === 'deprecated')
})

// 计算属性：是否为多渠道模式
// 多渠道模式判断逻辑：
// 1. 只有一个启用的渠道 → 单渠道模式
// 2. 有一个 active + 几个 suspended → 单渠道模式
// 3. 有多个 active 渠道 → 多渠道模式
const isMultiChannelMode = computed(() => {
	const activeCount = props.channels.filter(
		ch => !ch.excludeFromConversation && (ch.status === 'active' || ch.status === undefined || ch.status === '')
	).length
  return activeCount > 1
})

// 切换协议时收起展开的图表
watch(() => props.channelType, () => {
  expandedChannelIndex.value = null
})

const showsConversations = computed(() => ['messages', 'responses', 'chat', 'gemini'].includes(props.channelType))

const metricsByChannel = computed(() => new Map(props.dashboardMetrics.map(metric => [metric.channelIndex, metric])))
const getChannelMetrics = (channelIndex: number): ChannelMetrics | undefined => metricsByChannel.value.get(channelIndex)

const toggleVisionCapability = async (channel: Channel) => {
  const nextValue = !channel.visionCapable

  try {
    await channelApiByType(kindOf(channel)).updateChannel(channel.index, { visionCapable: nextValue })

    emit('refresh')
    emit('success', nextValue
      ? `已将 ${channel.name} 设为支持图片理解`
      : `已取消 ${channel.name} 的图片理解支持`)
  } catch (error) {
    console.error('Failed to update vision capability:', error)
    const errorMessage = error instanceof Error ? error.message : '未知错误'
    emit('error', `设置图片理解能力失败: ${errorMessage}`)
  }
}

const duplicateChannel = (channel: Channel) => emit('duplicate', channel)

// 设置渠道状态
const setChannelStatus = async (channelId: number, status: ChannelStatus, kind: ApiTab = props.channelType) => {
  try {
    await channelApiByType(kind).setStatus(channelId, status)
    emit('refresh')
  } catch (error) {
    const errorMessage = error instanceof Error ? error.message : '未知错误'
    if (error instanceof ApiError && error.committed) {
      emit('refresh')
      emit('error', `${errorMessage}（状态已写入，请刷新查看实际状态，请勿重复提交）`)
      return
    }
    emit('error', `设置渠道状态失败: ${errorMessage}`)
  }
}

// 恢复暂停渠道与启用备用渠道是同一操作：激活接口统一重置两层熔断，避免恢复到一半的状态。
const resumeChannel = async (channelId: number, kind: ApiTab = props.channelType) => {
  await setChannelStatus(channelId, 'active', kind)
}

const shouldResumeChannel = (channel: Channel): boolean => {
  const status = channel.status || 'active'
  const circuitState = getChannelMetrics(channel.index)?.circuitState
  return status === 'suspended' ||
    (status === 'active' && (circuitState === 'open' || circuitState === 'half_open'))
}

const toggleChannelPrimaryStatus = async (channel: Channel) => {
  const currentStatus = channel.status || 'active'
  const kind = kindOf(channel)

  if (shouldResumeChannel(channel)) {
    await resumeChannel(channel.index, kind)
    return
  }

  if (currentStatus === 'active') {
    await setChannelStatus(channel.index, 'suspended', kind)
  }
}

const runMenuAction = (channel: Channel, action: ChannelMenuAction) => {
  const kind = kindOf(channel)
  switch (action) {
    case 'edit': return emit('edit', channel)
    case 'duplicate': return duplicateChannel(channel)
    case 'toggleVision': return toggleVisionCapability(channel)
    case 'copyConfig': return copyChannelConfig(channel)
    case 'quickTest': return handleQuickTest(channel)
    case 'ping': return emit('ping', channel.index)
    case 'logs': return openLogsDialog(channel)
    case 'promotion': return openPromotionDialog(channel)
    case 'resume': return resumeChannel(channel.index, kind)
    case 'suspend': return setChannelStatus(channel.index, 'suspended', kind)
    case 'disable': return setChannelStatus(channel.index, 'disabled', kind)
    case 'deprecate': return setChannelStatus(channel.index, 'deprecated', kind)
  }
}

// 处理删除渠道
const handleDeleteChannel = (channel: Channel) => {
  emit('delete', channel.index, channel.kind)
}

onMounted(() => {
  // 每 30 秒更新一次 currentTime，触发延迟显示的响应式更新
  latencyCheckTimer = setInterval(() => {
    if (!document.hidden) currentTime.value = Date.now()
  }, 30000)
})

// 组件卸载时清理定时器
onUnmounted(() => {
  if (copyStatusTimer) clearTimeout(copyStatusTimer)
  if (latencyCheckTimer) {
    clearInterval(latencyCheckTimer)
    latencyCheckTimer = null
  }
})

</script>

<style scoped src="@/features/channels/styles/channel-orchestration.css"></style>
