<template>
  <v-card elevation="0" rounded="lg" class="channel-orchestration" variant="flat">
    <!-- 调度器统计信息 -->
    <v-card-title class="orchestration-header d-flex align-center justify-space-between py-3 px-0">
      <div class="d-flex align-center">
        <v-icon class="mr-2" color="primary">mdi-swap-vertical-bold</v-icon>
        <span class="text-h6">渠道编排</span>
        <v-chip v-if="isMultiChannelMode" size="small" color="success" variant="tonal" class="ml-3">
          多渠道模式
        </v-chip>
        <v-chip v-else size="small" color="warning" variant="tonal" class="ml-3"> 单渠道模式 </v-chip>
      </div>
      <div class="d-flex align-center ga-2">
        <v-progress-circular v-if="isLoadingMetrics" indeterminate size="16" width="2" color="primary" />
      </div>
    </v-card-title>

    <v-divider />

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
              <div class="channel-row-content">
                <!-- 拖拽手柄 -->
                <div class="drag-handle" @click.stop>
                  <v-icon size="small" color="grey">mdi-drag-vertical</v-icon>
                </div>

            <!-- 优先级序号 -->
            <div class="priority-number" @click.stop>
              <span class="text-caption font-weight-bold">{{ index + 1 }}</span>
            </div>

            <!-- 状态指示器 -->
            <div
              class="channel-status-toggle"
              role="button"
              tabindex="0"
              :title="shouldResumeChannel(element) ? '点击恢复为活跃并重置熔断' : '点击切换为熔断'"
              @click.stop="toggleChannelPrimaryStatus(element)"
              @keydown.enter.stop="toggleChannelPrimaryStatus(element)"
              @keydown.space.prevent.stop="toggleChannelPrimaryStatus(element)"
            >
              <ChannelStatusBadge :status="element.status || 'active'" :metrics="getChannelMetrics(element.index)" />
            </div>

            <!-- 渠道名称和描述 -->
            <div class="channel-name">
              <span
                class="font-weight-medium channel-name-link"
                tabindex="0"
                role="button"
                @click.stop="$emit('edit', element)"
                @keydown.enter.stop="$emit('edit', element)"
                @keydown.space.stop="$emit('edit', element)"
              >{{ element.name }}</span>
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
                v-if="shouldShowVisionCapability(element)"
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
                    @click.stop
                  >
                    <v-icon start size="12">mdi-image-search-outline</v-icon>
                    图片
                  </v-chip>
                </template>
              </v-tooltip>
              <!-- 官网链接按钮 -->
              <v-btn
                :href="getWebsiteUrl(element)"
                target="_blank"
                rel="noopener"
                icon
                size="x-small"
                variant="text"
                color="primary"
                class="ml-1"
                title="打开官网"
                @click.stop
              >
                <v-icon size="14">mdi-open-in-new</v-icon>
              </v-btn>
              <span class="text-caption text-medium-emphasis ml-2">{{ channelServiceLabel(channelType, element.serviceType) }}</span>
              <!-- 展开图标 -->
              <v-icon
                size="x-small"
                class="ml-auto expand-icon"
                :color="expandedChannelIndex === element.index ? 'primary' : 'grey-lighten-1'"
                @click.stop="toggleChannelChart(element.index)"
              >{{ expandedChannelIndex === element.index ? 'mdi-chevron-up' : 'mdi-chevron-down' }}</v-icon>
            </div>

            <!-- 指标显示 — 视觉条 -->
            <div class="channel-metrics" @click.stop>
              <template v-if="getChannelMetrics(element.index)">
                <v-tooltip location="top" :open-delay="200" :open-on-focus="false">
                  <template #activator="{ props: tooltipProps }">
                    <div v-bind="tooltipProps" class="metrics-visual">
                      <!-- 15分钟有请求时显示指标条，否则显示 -- -->
                      <template v-if="get15mStats(element.index)?.requestCount">
                        <!-- 成功率条 -->
                        <div class="mini-metric-bar">
                          <span class="metric-bar-label">成功</span>
                          <div class="mmb-track">
                            <div
                              class="mmb-fill"
                              :class="getRateLevel(get15mStats(element.index)?.successRate)"
                              :style="{ width: `${get15mStats(element.index)?.successRate ?? 0}%` }"
                            ></div>
                            <!-- 指标刷新时重放一次扫光 -->
                            <span
                              :key="`mf-${get15mStats(element.index)?.successRate?.toFixed(0)}`"
                              class="mmb-flash"
                            ></span>
                          </div>
                          <span
                            :key="`mmb-${get15mStats(element.index)?.successRate?.toFixed(0)}`"
                            class="mmb-value"
                            :class="getRateLevel(get15mStats(element.index)?.successRate)"
                          >
                            {{ get15mStats(element.index)?.successRate?.toFixed(0) }}%
                          </span>
                        </div>
                        <!-- 缓存命中率条：与成功率同规格，低命中只是省得少，不按故障报警 -->
                        <div
                          class="mini-metric-bar cache-mini-metric-bar"
                          :class="{ 'is-unavailable': !shouldShowCacheHitRate(get15mStats(element.index)) }"
                        >
                          <span class="metric-bar-label">缓存</span>
                          <div class="mmb-track">
                            <div
                              v-if="shouldShowCacheHitRate(get15mStats(element.index))"
                              class="mmb-fill cache-mmb-fill"
                              :class="getCacheRateLevel(getCacheHitRate(get15mStats(element.index)))"
                              :style="{ width: `${getCacheHitRate(get15mStats(element.index)) ?? 0}%` }"
                            ></div>
                            <span
                              v-if="shouldShowCacheHitRate(get15mStats(element.index))"
                              :key="`cf-${getCacheHitRate(get15mStats(element.index))?.toFixed(0)}`"
                              class="mmb-flash"
                            ></span>
                          </div>
                          <span
                            :key="`cmb-${getCacheHitRate(get15mStats(element.index))?.toFixed(0) ?? 'unknown'}`"
                            class="mmb-value cache-mmb-value"
                            :class="getCacheRateLevel(getCacheHitRate(get15mStats(element.index)))"
                          >
                            {{ shouldShowCacheHitRate(get15mStats(element.index)) ? `${getCacheHitRate(get15mStats(element.index))?.toFixed(0)}%` : '—' }}
                          </span>
                        </div>
                        <!-- 请求数 -->
                        <div class="mini-metric-secondary">
                          <span class="mm-requests">{{ get15mStats(element.index)?.requestCount }} 请求</span>
                        </div>
                      </template>
                      <span v-else class="text-caption text-medium-emphasis">--</span>
                    </div>
                  </template>
                  <div class="metrics-tooltip">
                    <div class="text-caption font-weight-bold mb-1">请求统计</div>
                    <div class="metrics-tooltip-row">
                      <span>15分钟:</span>
                      <span>{{ formatStats(get15mStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>1小时:</span>
                      <span>{{ formatStats(get1hStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>6小时:</span>
                      <span>{{ formatStats(get6hStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>24小时:</span>
                      <span>{{ formatStats(get24hStats(element.index)) }}</span>
                    </div>

                    <div class="text-caption font-weight-bold mt-2 mb-1">缓存统计 (Token)</div>
                    <div class="metrics-tooltip-row">
                      <span>15分钟:</span>
                      <span>{{ formatCacheStats(get15mStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>1小时:</span>
                      <span>{{ formatCacheStats(get1hStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>6小时:</span>
                      <span>{{ formatCacheStats(get6hStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>24小时:</span>
                      <span>{{ formatCacheStats(get24hStats(element.index)) }}</span>
                    </div>
                  </div>
                </v-tooltip>
              </template>
              <span v-else class="text-caption text-medium-emphasis">--</span>
            </div>

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
                :supports-vision-capability="supportsVisionCapability"
                :can-delete="canDeleteChannel(element)"
                :supports-ccs="supportsCcs"
                @edit="$emit('edit', element)"
                @duplicate="duplicateChannel(element.index)"
                @toggle-vision="toggleVisionCapability(element)"
                @copy-config="copyChannelConfig(element)"
                @import-ccs="openCcsImport(element)"
                @quick-test="handleQuickTest(element)"
                @ping="$emit('ping', element.index)"
                @logs="openLogsDialog(element)"
                @promotion="openPromotionDialog(element)"
                @move-top="move_to_top()"
                @move-bottom="move_to_bottom()"
                @resume="resumeChannel(element.index)"
                @suspend="setChannelStatus(element.index, 'suspended')"
                @disable="setChannelStatus(element.index, 'disabled')"
                @deprecate="setChannelStatus(element.index, 'deprecated')"
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
          :supports-vision-capability="supportsVisionCapability"
          :allow-reorder="false"
          :supports-ccs="supportsCcs"
          @edit="$emit('edit', channel)"
          @duplicate="duplicateChannel(channel.index)"
          @toggle-vision="toggleVisionCapability(channel)"
          @copy-config="copyChannelConfig(channel)"
          @import-ccs="openCcsImport(channel)"
          @quick-test="handleQuickTest(channel)"
          @ping="$emit('ping', channel.index)"
          @logs="openLogsDialog(channel)"
          @promotion="openPromotionDialog(channel)"
          @resume="resumeChannel(channel.index)"
          @suspend="setChannelStatus(channel.index, 'suspended')"
          @disable="setChannelStatus(channel.index, 'disabled')"
          @deprecate="setChannelStatus(channel.index, 'deprecated')"
          @delete="$emit('delete', channel.index)"
        />
      </template>
    </ChannelPoolGrid>

    <v-divider class="my-2" />

    <ChannelReservePools
      :inactive-channels="inactiveChannels"
      :deprecated-channels="deprecatedChannels"
      :channel-type="channelType"
      :supports-vision-capability="supportsVisionCapability"
      @edit="emit('edit', $event)"
      @delete="emit('delete', $event)"
      @enable="enableChannel"
      @duplicate="duplicateChannel"
      @vision="toggleVisionCapability"
      @status="setChannelStatus"
    />
  </v-card>

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
import { ref, shallowRef, computed, watch, onMounted, onUnmounted } from 'vue'
import { api, ApiError, channelApiByType, type ApiTab, type Channel, type ChannelMetrics, type ChannelStatus, type TimeWindowStats, type ChannelRecentActivity } from '../services/api'
import { useChannelActivity } from '../composables/useChannelActivity'
import { defineLazyChart } from '@/utils/lazyChart'
import { channelServiceLabel } from '@/utils/imagesChannelProtocol'
import {
  getCacheRateLevel,
  getRateLevel,
  getCacheHitRate,
  shouldShowCacheHitRate,
  getLatencyColor,
  isLatencyValid,
  isInPromotion,
  formatPromotionRemaining,
  formatDateTime,
  formatStats,
  formatTokens,
  formatCacheStats,
  getWebsiteUrl
} from '../utils/channelMetricsFormat'
import CcsImportModal from './CcsImportModal.vue'
import ChannelStatusBadge from './ChannelStatusBadge.vue'
import ChannelActivityChart from './ChannelActivityChart.vue'
import ChannelPoolGrid from './ChannelPoolGrid.vue'
import ChannelQuickMenu from './ChannelQuickMenu.vue'
import ChannelMappingBlock from './ChannelMappingBlock.vue'
import QuickTestModal from './QuickTestModal.vue'
import ChannelPromotionDialog from './channel/ChannelPromotionDialog.vue'
import ChannelLogsDialog from './channel/ChannelLogsDialog.vue'
import ChannelReservePools from './channel/ChannelReservePools.vue'
import { useChannelLogs } from '@/composables/useChannelLogs'
import { useChannelPromotion } from '@/composables/useChannelPromotion'

const KeyTrendChart = defineLazyChart(() => import('./KeyTrendChart.vue'))

const props = defineProps<{
  channels: Channel[]
  currentChannelIndex: number
  channelType: ApiTab
  // 可选：从父组件传入的 metrics 和 stats（使用 dashboard 接口时）
  dashboardMetrics?: ChannelMetrics[]
  dashboardStats?: {
    multiChannelMode: boolean
    activeChannelCount: number
    traceAffinityCount: number
    traceAffinityTTL: string
    failureThreshold: number
    windowSize: number
    circuitRecoveryTime?: string
  }
  // 可选：从父组件传入的实时活跃度数据
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

const supportsVisionCapability = computed(() => true)

// 快捷测试弹窗状态
const showQuickTestModal = ref(false)
const quickTestChannel = ref<Channel | null>(null)

// 状态
// 指标只接收整份快照，不在组件内修改统计段。
const metrics = shallowRef<ChannelMetrics[]>([])
const recentActivity = shallowRef<ChannelRecentActivity[]>([])
const schedulerStats = shallowRef<{
  multiChannelMode: boolean
  activeChannelCount: number
  traceAffinityCount: number
  traceAffinityTTL: string
  failureThreshold: number
  windowSize: number
} | null>(null)
const isLoadingMetrics = ref(false)

const logsViewer = useChannelLogs(props)
const { openLogsDialog } = logsViewer

const promotionEditor = useChannelPromotion(props, emit)
const {
  openPromotionDialog, confirmPromotion, promotionDuration, promotionCount,
  promotionError, promotionChannel, showPromotionDialog
} = promotionEditor

// 用于触发响应式更新的时间戳
const currentTime = ref(Date.now())
let latencyCheckTimer: ReturnType<typeof setInterval> | null = null

// 活跃度可视化收敛到 useChannelActivity composable（R5，方案 8.1）：
// 数据本体（recentActivity）本组件持有，柱状/曲线派生计算经 composable。
const { getActivityBars, formatRPM, formatTPM, hasActivityData } =
  useChannelActivity(recentActivity)

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
    setTimeout(() => {
      copiedChannelIndex.value = null
    }, 2000)
  } catch (error) {
    console.error('复制配置失败:', error)
  }
}

// ============== CC Switch 导入 ==============

// CC Switch 路径（settings.integration），决定弹窗按钮是"导入"还是"复制链接"
const ccsConfigured = ref(false)
const showCcsModal = ref(false)
const ccsModalChannel = ref<Channel | null>(null)

onMounted(async () => {
  try {
    const settings = await api.getSettings()
    ccsConfigured.value = Boolean(settings.integration?.ccSwitchPath?.trim())
  } catch {
    // 取不到设置就按未配置处理：复制链接兜底
    ccsConfigured.value = false
  }
})

// 打开导入弹窗（目标 app 在弹窗中选择，按渠道池预选）
const openCcsImport = (channel: Channel) => {
  ccsModalChannel.value = channel
  showCcsModal.value = true
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

// 监听 dashboard props 变化（从父组件传入的合并数据）
watch(() => props.dashboardMetrics, (newMetrics) => {
  if (newMetrics) {
    metrics.value = newMetrics
  }
}, { immediate: true })

watch(() => props.dashboardStats, (newStats) => {
  if (newStats) {
    schedulerStats.value = newStats
  }
}, { immediate: true })

// 监听 recentActivity props 变化
watch(() => props.dashboardRecentActivity, (newActivity) => {
  recentActivity.value = newActivity ?? []
}, { immediate: true })

// 监听 channelType 变化 - 切换时刷新指标并收起图表
watch(() => props.channelType, () => {
  expandedChannelIndex.value = null // 收起展开的图表
  // 如果没有使用 dashboard props，则自己刷新
  if (!props.dashboardMetrics) {
    refreshMetrics()
  }
})

// 获取渠道指标
const metricsByChannel = computed(() => new Map(metrics.value.map(metric => [metric.channelIndex, metric])))
const getChannelMetrics = (channelIndex: number): ChannelMetrics | undefined => metricsByChannel.value.get(channelIndex)

// 获取分时段统计的辅助方法
const get15mStats = (channelIndex: number) => {
  return getChannelMetrics(channelIndex)?.timeWindows?.['15m']
}

const get1hStats = (channelIndex: number) => {
  return getChannelMetrics(channelIndex)?.timeWindows?.['1h']
}

const get6hStats = (channelIndex: number) => {
  return getChannelMetrics(channelIndex)?.timeWindows?.['6h']
}

const get24hStats = (channelIndex: number) => {
  return getChannelMetrics(channelIndex)?.timeWindows?.['24h']
}

// 指标条档位：驱动 CSS 里的配色与动效强度（成功率与缓存各一套阈值）
const shouldShowVisionCapability = (channel: Channel): boolean => {
  return supportsVisionCapability.value && !!channel.visionCapable
}

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

// 刷新指标
const refreshMetrics = async () => {
  isLoadingMetrics.value = true
  try {
    const [metricsData, statsData] = await Promise.all([
      channelApiByType(props.channelType).getChannelMetrics(),
      api.getSchedulerStats(props.channelType)
    ])
    metrics.value = metricsData
    schedulerStats.value = statsData
  } catch (error) {
    console.error('Failed to load metrics:', error)
  } finally {
    isLoadingMetrics.value = false
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

// 启用渠道（从备用池移到活跃序列）
const enableChannel = async (channelId: number) => {
  await setChannelStatus(channelId, 'active')
}

// 激活接口统一重置两层熔断，避免分两次请求造成恢复到一半的状态。
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

// 组件挂载时加载指标并启动延迟过期检查定时器
onMounted(() => {
  if (!props.dashboardMetrics) void refreshMetrics()
  // 每 30 秒更新一次 currentTime，触发延迟显示的响应式更新
  latencyCheckTimer = setInterval(() => {
    if (!document.hidden) currentTime.value = Date.now()
  }, 30000)
})

// 组件卸载时清理定时器
onUnmounted(() => {
  if (latencyCheckTimer) {
    clearInterval(latencyCheckTimer)
    latencyCheckTimer = null
  }
})

// 暴露方法给父组件
defineExpose({
  refreshMetrics
})
</script>

<style scoped src="../styles/channel-orchestration.css"></style>
