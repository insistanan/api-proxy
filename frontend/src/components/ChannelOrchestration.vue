<template>
  <div class="channel-orchestration">
    <ChannelPoolGrid
      :channels="channels"
      :channel-type="channelType"
      @edit="$emit('edit', $event)"
      @delete="$emit('delete', $event)"
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
      <template #channel="{ channel: element, index, pool, move_to_top, move_to_bottom }">
          <div class="channel-item-wrapper">
            <div
              class="channel-row"
              :class="{ 'is-suspended': element.status === 'suspended' }"
              @click="toggleChannelChart(element.index)"
            >
              <ChannelActivityChart
                :bars="getActivityBars(element.index)"
                :channel-id="element.index"
                :channel-type="channelType"
              />

              <!-- Grid 内容容器 -->
              <div class="channel-row-content" :class="{ 'is-idle': !hasRecentTraffic(element.index) }">
                <!-- 拖拽手柄 -->
                <div class="drag-handle" @click.stop>
                  <v-icon size="small" color="grey">mdi-drag-vertical</v-icon>
                </div>

            <!-- 优先级序号 -->
            <div class="priority-number" @click.stop>
              <span class="text-caption font-weight-bold">{{ index + 1 }}</span>
            </div>

            <!-- 状态指示器 -->
            <button
              type="button"
              class="channel-status-toggle"
              :title="shouldResumeChannel(element) ? '点击恢复为活跃并重置熔断' : '点击切换为熔断'"
              :aria-label="`${shouldResumeChannel(element) ? '恢复' : '暂停'} ${element.name}`"
              @click.stop="toggleChannelPrimaryStatus(element)"
            >
              <ChannelStatusBadge :status="element.status || 'active'" :metrics="getChannelMetrics(element.index)" />
            </button>

            <!-- 渠道名称和描述 -->
            <div class="channel-name">
              <button
                type="button"
                class="font-weight-medium channel-name-link"
                :title="element.name"
                :aria-label="`编辑渠道 ${element.name}`"
                @click.stop="$emit('edit', element)"
              >{{ element.name }}</button>
              <!-- 促销期标识 -->
              <v-chip
                v-if="isInPromotion(element)"
                size="x-small"
                color="info"
                variant="flat"
                class="ml-2"
              >
                <v-icon start size="12">mdi-rocket-launch</v-icon>
                {{ formatPromotionRemaining(element.promotionUntil, element.promotionCount) }}
              </v-chip>
              <v-chip
                v-if="element.temporary"
                size="x-small"
                color="warning"
                variant="tonal"
                class="ml-2"
              >
                <v-icon start size="12">mdi-timer-sand</v-icon>
                临时 {{ formatDateTime(element.temporaryUntil) }}
              </v-chip>
              <v-tooltip
                v-if="element.visionCapable"
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
                    variant="tonal"
                    class="ml-2 vision-default-chip"
                    @click.stop
                  >
                    <v-icon start size="12">mdi-image-search-outline</v-icon>
                    图片
                  </v-chip>
                </template>
              </v-tooltip>
              <!-- 展开图标 -->
              <v-btn
                icon
                size="x-small"
                variant="text"
                class="ml-auto expand-icon"
                :color="expandedChannelIndex === element.index ? 'primary' : 'grey-lighten-1'"
                :aria-label="`${expandedChannelIndex === element.index ? '收起' : '展开'} ${element.name} 的密钥趋势`"
                :aria-expanded="expandedChannelIndex === element.index"
                @click.stop="toggleChannelChart(element.index)"
              ><v-icon size="18">{{ expandedChannelIndex === element.index ? 'mdi-chevron-up' : 'mdi-chevron-down' }}</v-icon></v-btn>
            </div>

            <ChannelMetricsCell :metrics="getChannelMetrics(element.index)" />

            <!-- RPM/TPM 显示 -->
            <div class="channel-rpm-tpm" @click.stop>
              <div class="rpm-tpm-values">
                <span class="rpm-value" :class="{ 'has-data': hasActivityData(element.index) }">{{ formatRPM(element.index) }}</span>
                <span class="rpm-tpm-separator">/</span>
                <span class="tpm-value" :class="{ 'has-data': hasActivityData(element.index) }">{{ formatTPM(element.index) }}</span>
              </div>
              <div class="rpm-tpm-labels">
                <span>RPM</span>
                <span>/</span>
                <span>TPM</span>
              </div>
            </div>

            <!-- 延迟显示 -->
            <div class="channel-latency" @click.stop>
              <v-chip
                v-if="isLatencyValid(element, currentTime)"
                size="x-small"
                :color="getLatencyColor(element.latency!)"
                variant="tonal"
              >
                {{ element.latency }}ms
              </v-chip>
            </div>

            <!-- API密钥数量 -->
            <div class="channel-keys" @click.stop>
              <v-chip size="x-small" variant="outlined" class="keys-chip" @click="$emit('edit', element)">
                <v-icon start size="x-small">mdi-key</v-icon>
                {{ element.apiKeys?.length || 0 }}
              </v-chip>
            </div>

            <!-- 操作按钮 -->
            <div class="channel-actions" @click.stop>
              <!-- suspended 状态显示恢复按钮 -->
              <v-btn
                v-if="element.status === 'suspended'"
                icon
                size="x-small"
                variant="text"
                color="warning"
                title="恢复"
                @click="resumeChannel(element.index)"
              >
                <v-icon size="small">mdi-refresh</v-icon>
              </v-btn>

              <ChannelQuickMenu
                :channel="element"
                :position="index"
                :total="pool.channels.length"
                :copied="copiedChannelIndex === element.index"
                :can-delete="canDeleteChannel(element)"
                :supports-ccs="supportsCcs"
                @action="runMenuAction(element, $event)"
                @move-top="move_to_top()"
                @move-bottom="move_to_bottom()"
                @delete="handleDeleteChannel(element)"
              />
            </div>
              </div><!-- .channel-row-content -->

              <!-- 副行：映射 / 描述。主行名称列塞不下它们，挤在一起会被压成看不出内容的小方块。
                   映射未配置也照样占位显示——漏配映射是常见误配，必须一眼看得到 -->
              <div class="channel-row-meta" @click.stop>
                <ChannelMappingBlock
                  :channel="element"
                  :channel-type="channelType"
                  @edit="$emit('edit', element)"
                />

                <span class="channel-service">{{ channelServiceLabel(channelType, element.serviceType) }}</span>
                <v-btn
                  :href="getWebsiteUrl(element)"
                  target="_blank"
                  rel="noopener"
                  icon="mdi-open-in-new"
                  size="x-small"
                  variant="text"
                  title="打开官网"
                  :aria-label="`打开 ${element.name} 的官网`"
                />

                <span v-if="element.description" class="meta-desc" :title="element.description">
                  {{ element.description }}
                </span>
              </div>
          </div><!-- .channel-row -->

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
          :supports-ccs="supportsCcs"
          @action="runMenuAction(channel, $event)"
          @delete="$emit('delete', channel.index)"
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
    <ChannelLogsDialog :viewer="logsViewer" />

    <!-- 快捷测试弹窗 -->
    <QuickTestModal
      v-model="showQuickTestModal"
      :channel="quickTestChannel"
      :api-type="channelType"
    />

    <!-- CC Switch 导入弹窗 -->
    <CcsImportModal
      v-model="showCcsModal"
      :channel="ccsModalChannel"
      :channel-type="channelType"
      :ccs-configured="ccsConfigured"
      @imported="name => emit('success', `已调用 CC Switch 导入 ${name}，请在其确认框中确认`)"
      @copied="emit('success', 'CCS 链接已复制，粘贴到浏览器地址栏回车即可唤起 CC Switch；也可在 设置 → 集成 配置路径实现一键导入')"
      @error="message => emit('error', message)"
    />

</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { api, ApiError, channelApiByType, type ApiTab, type Channel, type ChannelMetrics, type ChannelStatus, type ChannelRecentActivity } from '../services/api'
import { useChannelActivity } from '../composables/useChannelActivity'
import { defineLazyChart } from '@/utils/lazyChart'
import { channelServiceLabel } from '@/utils/imagesChannelProtocol'
import {
  getLatencyColor,
  isLatencyValid,
  isInPromotion,
  formatPromotionRemaining,
  formatDateTime,
  getWebsiteUrl
} from '../utils/channelMetricsFormat'
import CcsImportModal from './CcsImportModal.vue'
import ChannelStatusBadge from './ChannelStatusBadge.vue'
import ChannelActivityChart from './ChannelActivityChart.vue'
import ChannelPoolGrid from './ChannelPoolGrid.vue'
import ChannelQuickMenu, { type ChannelMenuAction } from './ChannelQuickMenu.vue'
import ChannelMappingBlock from './ChannelMappingBlock.vue'
import QuickTestModal from './QuickTestModal.vue'
import ChannelPromotionDialog from './channel/ChannelPromotionDialog.vue'
import ChannelLogsDialog from './channel/ChannelLogsDialog.vue'
import ChannelReservePools from './channel/ChannelReservePools.vue'
import ChannelMetricsCell from './channel/ChannelMetricsCell.vue'
import { useChannelLogs } from '@/composables/useChannelLogs'
import { useChannelPromotion } from '@/composables/useChannelPromotion'

const KeyTrendChart = defineLazyChart(() => import('./KeyTrendChart.vue'))

const props = defineProps<{
  channels: Channel[]
  channelType: ApiTab
  // 指标与活跃度由父级随 Dashboard 快照整体替换，本组件只读。
  dashboardMetrics: ChannelMetrics[]
  dashboardRecentActivity?: ChannelRecentActivity[]
}>()

const emit = defineEmits<{
  (_e: 'edit', _channel: Channel): void
  (_e: 'delete', _channelId: number): void
  (_e: 'ping', _channelId: number): void
  (_e: 'refresh'): void
  (_e: 'error', _message: string): void
  (_e: 'success', _message: string): void
}>()

// 快捷测试弹窗状态
const showQuickTestModal = ref(false)
const quickTestChannel = ref<Channel | null>(null)

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
  showQuickTestModal.value = true
}

// 复制渠道配置到剪贴板
const copiedChannelIndex = ref<number | null>(null)
let copyStatusTimer: ReturnType<typeof setTimeout> | undefined
const copyChannelConfig = async (channel: Channel) => {
  try {
    // 获取第一个密钥
    const firstKey = channel.apiKeys.length > 0 ? channel.apiKeys[0] : ''
    // 构建配置文本
    const configText = `名称: ${channel.name}
URL: ${channel.baseUrl}
密钥: ${firstKey}`
    // 复制到剪贴板
    await navigator.clipboard.writeText(configText)
    // 显示已复制状态
    copiedChannelIndex.value = channel.index
    if (copyStatusTimer) clearTimeout(copyStatusTimer)
    copyStatusTimer = setTimeout(() => {
      copiedChannelIndex.value = null
    }, 2000)
  } catch (error) {
    emit('error', `复制配置失败：${error instanceof Error ? error.message : '无法访问剪贴板'}`)
  }
}

// ============== CC Switch 导入 ==============

// CC Switch 路径（settings.integration），决定弹窗按钮是"导入"还是"复制链接"
const ccsConfigured = ref(false)
const showCcsModal = ref(false)
const ccsModalChannel = ref<Channel | null>(null)

const openCcsImport = async (channel: Channel) => {
  try {
    const settings = await api.getSettings()
    ccsConfigured.value = Boolean(settings.integration?.ccSwitchPath?.trim())
    ccsModalChannel.value = channel
    showCcsModal.value = true
  } catch (error) {
    emit('error', `读取 CC Switch 集成设置失败：${error instanceof Error ? error.message : '未知错误'}；请重试`)
  }
}
// 计算属性：非活跃渠道 - 仅 disabled 状态
const inactiveChannels = computed(() => {
	return props.channels.filter(ch => !ch.excludeFromConversation && ch.status === 'disabled')
})

const deprecatedChannels = computed(() => {
	return props.channels.filter(ch => !ch.excludeFromConversation && ch.status === 'deprecated')
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

// 近 15 分钟既无请求也无实时速率的渠道，在窄布局下收起指标行。
const hasRecentTraffic = (channelIndex: number) =>
  !!getChannelMetrics(channelIndex)?.timeWindows?.['15m']?.requestCount || hasActivityData(channelIndex)

const metricsByChannel = computed(() => new Map(props.dashboardMetrics.map(metric => [metric.channelIndex, metric])))
const getChannelMetrics = (channelIndex: number): ChannelMetrics | undefined => metricsByChannel.value.get(channelIndex)

const supportsCcs = computed(() => props.channelType !== 'images' && props.channelType !== 'embeddings' && props.channelType !== 'decisions')

const toggleVisionCapability = async (channel: Channel) => {
  const nextValue = !channel.visionCapable

  try {
    await channelApiByType(props.channelType).updateChannel(channel.index, { visionCapable: nextValue })

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

const duplicateChannel = async (channelIndex: number) => {
  try {
    await api.duplicateChannel(props.channelType, channelIndex)
    emit('refresh')
  } catch (error) {
    console.error('Failed to duplicate channel:', error)
    const errorMessage = error instanceof Error ? error.message : '未知错误'
    emit('error', `复制渠道失败: ${errorMessage}`)
  }
}

// 设置渠道状态
const setChannelStatus = async (channelId: number, status: ChannelStatus) => {
  try {
    await channelApiByType(props.channelType).setStatus(channelId, status)
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
const resumeChannel = async (channelId: number) => {
  await setChannelStatus(channelId, 'active')
}

const shouldResumeChannel = (channel: Channel): boolean => {
  const status = channel.status || 'active'
  const circuitState = getChannelMetrics(channel.index)?.circuitState
  return status === 'suspended' ||
    (status === 'active' && (circuitState === 'open' || circuitState === 'half_open'))
}

const toggleChannelPrimaryStatus = async (channel: Channel) => {
  const currentStatus = channel.status || 'active'

  if (shouldResumeChannel(channel)) {
    await resumeChannel(channel.index)
    return
  }

  if (currentStatus === 'active') {
    await setChannelStatus(channel.index, 'suspended')
  }
}

const runMenuAction = (channel: Channel, action: ChannelMenuAction) => {
  switch (action) {
    case 'edit': return emit('edit', channel)
    case 'duplicate': return duplicateChannel(channel.index)
    case 'toggleVision': return toggleVisionCapability(channel)
    case 'copyConfig': return copyChannelConfig(channel)
    case 'importCcs': return openCcsImport(channel)
    case 'quickTest': return handleQuickTest(channel)
    case 'ping': return emit('ping', channel.index)
    case 'logs': return openLogsDialog(channel)
    case 'promotion': return openPromotionDialog(channel)
    case 'resume': return resumeChannel(channel.index)
    case 'suspend': return setChannelStatus(channel.index, 'suspended')
    case 'disable': return setChannelStatus(channel.index, 'disabled')
    case 'deprecate': return setChannelStatus(channel.index, 'deprecated')
  }
}

// 判断渠道是否可以删除
// 规则：当前渠道分组中至少要保留一个 active 状态的渠道
const activeChannelsByPool = computed(() => {
  const counts = new Map<string, number>()
  for (const channel of props.channels) {
    if (channel.excludeFromConversation || !['active', undefined, ''].includes(channel.status)) continue
    const poolID = channel.poolId || 'default'
    counts.set(poolID, (counts.get(poolID) ?? 0) + 1)
  }
  return counts
})
const canDeleteChannel = (channel: Channel): boolean => {
  // 统计当前 active 状态的渠道数量
  const poolID = channel.poolId || 'default'
  const activeCount = activeChannelsByPool.value.get(poolID) ?? 0

  // 如果要删除的是 active 渠道，且只剩一个 active，则不允许删除
  const isActive = channel.status === 'active' || channel.status === undefined || channel.status === ''
  if (isActive && activeCount <= 1) {
    return false
  }

  return true
}

// 处理删除渠道
const handleDeleteChannel = (channel: Channel) => {
  if (!canDeleteChannel(channel)) {
    emit('error', '无法删除：当前渠道分组中至少需要保留一个活跃渠道')
    return
  }
  emit('delete', channel.index)
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

<style scoped src="../styles/channel-orchestration.css"></style>
