<template>
  <!-- 路由视图根节点必须是单个真实 DOM 元素。 -->
  <div class="channels-view">
    <PageHeader :title="`${navigation?.label} 渠道`" :description="navigation?.description">
      <ChannelTableDrawer :channel-type="channelType" :channels="channels">
        <template #activator="{ props: activatorProps }">
          <UiButton v-bind="activatorProps" variant="tonal" prepend-icon="mdi-format-list-bulleted">模型列表</UiButton>
        </template>
      </ChannelTableDrawer>
      <UiTooltip text="刷新" location="bottom">
        <template #activator="{ props: tooltipProps }">
          <UiButton v-bind="tooltipProps" icon="mdi-refresh" variant="text" aria-label="刷新" @click="refreshChannels" />
        </template>
      </UiTooltip>
      <UiButton color="info" variant="tonal" prepend-icon="mdi-speedometer" :loading="channelStore.isPingingAll" @click="pingAllChannels">
        测试延迟
      </UiButton>
      <UiButton color="primary" prepend-icon="mdi-plus" @click="openAddChannel()">添加渠道</UiButton>
    </PageHeader>

    <!-- 概览与路由开关合并为一行，把首屏高度留给渠道列表。 -->
    <section class="channel-overview panel-surface" aria-label="渠道概览">
      <dl class="overview-stats">
        <div class="overview-stat">
          <dt>渠道</dt>
          <dd>{{ totalChannelsDisplay }}</dd>
        </div>
        <div class="overview-stat">
          <dt>活跃</dt>
          <dd>{{ activeChannelsDisplay }}<small>/{{ channelStore.failoverChannelCount }}</small></dd>
        </div>
        <div class="overview-stat">
          <dt>服务</dt>
          <dd class="overview-status" :class="`is-${serviceStatus.tone}`">
            <span class="overview-status__dot" aria-hidden="true"></span>{{ serviceStatus.text }}
          </dd>
        </div>
      </dl>

      <div class="overview-modes">
        <IntentRoutingControl v-if="intentProtocol" :key="intentProtocol" :protocol="intentProtocol" />
        <UiTooltip v-for="mode in modes" :key="mode.key" location="bottom" content-class="status-tooltip">
          <template #activator="{ props: tooltipProps }">
            <UiButton
              v-bind="tooltipProps"
              :variant="mode.enabled ? 'tonal' : 'outlined'"
              :color="mode.enabled ? mode.color : undefined"
              :loading="mode.loading"
              :disabled="mode.unknown"
              :aria-pressed="mode.enabled"
              class="mode-control"
              :class="{ 'is-off': !mode.enabled }"
              @click="mode.toggle"
            >
              <UiIcon start size="18">{{ mode.unknown ? 'mdi-alert-circle-outline' : mode.icon }}</UiIcon>
              {{ mode.label }}
              <span class="mode-state">{{ mode.unknown ? '未知' : mode.enabled ? '开' : '关' }}</span>
            </UiButton>
          </template>
          <span>{{ mode.tooltip }}</span>
        </UiTooltip>
      </div>
    </section>

    <DisclosureSection
      v-model="preferencesStore.showGlobalStats"
      :title="`${navigation?.label} 流量统计`"
      icon="mdi-chart-areaspline"
      lazy
      class="mb-4"
    >
      <GlobalStatsChart :api-type="channelType" />
    </DisclosureSection>

    <ChannelOrchestration
      v-if="channels.length"
      ref="orchestration"
      :channels="channels"
      :channel-type="channelType"
      :dashboard-metrics="channelStore.currentDashboardMetrics"
      :dashboard-recent-activity="channelStore.currentDashboardRecentActivity"
      :vision-channels="sharedVisionChannels"
      @add-channel="openAddChannel"
      @edit="editChannel"
      @duplicate="copyChannel"
      @delete="handleDeleteChannel"
      @ping="pingChannel"
      @refresh="refreshChannels"
      @error="showToast($event, 'error')"
      @success="showToast($event, 'info')"
    />

    <UiCard v-else>
      <EmptyState
        icon="mdi-server-network"
        title="暂无渠道"
        :description="`添加第一个上游渠道后，即可通过本代理转发 ${navigation?.label} 请求。`"
      >
        <UiButton color="primary" prepend-icon="mdi-plus" @click="openAddChannel()">添加渠道</UiButton>
      </EmptyState>
    </UiCard>

    <ConfirmDialog
      :model-value="pendingConfirm !== null"
      :message="pendingConfirm?.message ?? ''"
      :title="pendingConfirm?.title"
      :confirm-text="pendingConfirm?.confirmText"
      @update:model-value="cancelConfirm"
      @confirm="acceptConfirm"
    />
  </div>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiTooltip from '@/shared/ui/overlay/Tooltip.vue'
// 渠道页：概览、路由开关、流量统计与渠道编排。
// 渠道数据由 channelStore 持有；本页负责进入时加载、停留期间自动刷新、离开时停止。
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import ConfirmDialog from '@/shared/components/overlay/ConfirmDialog.vue'
import { useConfirm } from '@/shared/composables/useConfirm'
import { deletionImpactWarning } from '@/features/channels/utils/deletionImpact'
import { channelAnchorId } from '@/features/channels/utils/channelSearch'
import PageHeader from '@/shared/components/layout/PageHeader.vue'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'
import DisclosureSection from '@/shared/components/layout/DisclosureSection.vue'
import ChannelOrchestration from '@/features/channels/components/dashboard/ChannelOrchestration.vue'
import ChannelTableDrawer from '@/features/channels/components/dashboard/ChannelTableDrawer.vue'
import IntentRoutingControl from '@/features/channels/components/routing/IntentRoutingControl.vue'
import { isIntentProtocol } from '@/features/channels/services/intent'
import { isConversationProtocol } from '@/features/channels/services/channel-api'
import { useChannelActions } from '@/features/channels/composables/useChannelActions'
import { useCountUp } from '@/shared/composables/useCountUp'
import { useRoutingModes } from '@/features/channels/composables/useRoutingModes'
import { useToast } from '@/shared/composables/useToast'
import { findNavigation } from '@/router/navigation'
import { api, type ApiTab, type Channel } from '@/api'
import { useChannelStore } from '@/features/channels/stores/channel'
import { useDialogStore } from '@/features/channels/stores/dialog'
import { usePreferencesStore } from '@/shared/stores/preferences'
import { defineLazyChart } from '@/shared/utils/lazyChart'

const GlobalStatsChart = defineLazyChart(() => import('@/features/channels/components/dashboard/GlobalStatsChart.vue'))

const channelStore = useChannelStore()
const dialogStore = useDialogStore()
const preferencesStore = usePreferencesStore()
const { showToast } = useToast()
const { pending: pendingConfirm, askConfirm, acceptConfirm, cancelConfirm } = useConfirm()
const preparingDelete = ref(false)
const orchestration = ref<InstanceType<typeof ChannelOrchestration>>()

// 协议类型以 store 中经过校验的 activeTab 为准，非法路由参数回落到 Messages。
const channelType = computed(() => channelStore.activeTab)
const intentProtocol = computed(() => isIntentProtocol(channelType.value) ? channelType.value : null)
const navigation = computed(() => findNavigation(`/channels/${channelType.value}`))
const channels = computed(() => channelStore.currentChannelsData.channels)
// 公用图片理解池只服务于四个会话协议，透传协议页不展示。
const sharedVisionChannels = computed(() => isConversationProtocol(channelType.value) ? channelStore.visionChannels : [])

// 数据刷新时数字平滑过渡，而非硬跳变。
const { displayValue: totalChannelsDisplay } = useCountUp(computed(() => channels.value.length))
const { displayValue: activeChannelsDisplay } = useCountUp(computed(() => channelStore.activeChannelCount))

const serviceStatus = computed(() => {
  if (channelStore.lastRefreshSuccess === null) return { text: '连接中', tone: 'pending' }
  return channelStore.lastRefreshSuccess ? { text: '运行中', tone: 'ok' } : { text: '连接失败', tone: 'error' }
})

// 渠道写操作由 useChannelActions 编排（R5，方案 8.1）；已提交但收尾失败时由它提示刷新（R3）。
const {
  refreshChannels: refreshAction, refreshVisionChannelPool, handleAuthError,
  deleteChannel, pingChannel, pingAllChannels
} = useChannelActions(showToast)

const refreshChannels = async () => {
  await Promise.all([refreshAction(handleAuthError), refreshVisionChannelPool()])
}

async function locateChannel(request: NonNullable<typeof channelStore.locateRequest>, isCurrent: () => boolean) {
  try {
    await channelStore.refreshChannels(request.protocol)
    if (!isCurrent()) return
    const channel = channels.value.find(item => item.id === request.id && item.status !== 'deleted')
    if (!channel) throw new Error('目标渠道已移除或不再可用')
    if (channel.excludeFromConversation && !sharedVisionChannels.value.some(item => item.id === channel.id && item.kind === request.protocol)) {
      await channelStore.refreshVisionChannels()
    }
    await nextTick()
    if (!isCurrent()) return
    if (!orchestration.value) throw new Error('渠道编排尚未就绪，请重试')
    await orchestration.value.revealChannel(channel)
    await new Promise<void>(resolve => window.requestAnimationFrame(() => resolve()))
    if (!isCurrent()) return
    const target = document.getElementById(channelAnchorId(request.protocol, request.id))
    if (!target) throw new Error(`“${channel.name}”未能显示在所属分组中，请刷新后重试`)
    // 等折叠面板完成布局过渡，再计算滚动落点，避免定位后渠道又被挤出视口。
    const transitions: Array<Promise<Awaited<ReturnType<Element['getAnimations']>>[number]>> = []
    for (let parent = target.parentElement; parent && parent.id !== 'main-content'; parent = parent.parentElement) {
      for (const animation of parent.getAnimations?.() ?? []) {
        if (animation.playState === 'running' && animation.effect?.getComputedTiming().iterations !== Infinity) {
          transitions.push(animation.finished)
        }
      }
    }
    await Promise.allSettled(transitions)
    if (!isCurrent() || !target.isConnected) return
    target.focus({ preventScroll: true })
    const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    target.scrollIntoView({ block: 'center', behavior: reduceMotion ? 'instant' : 'smooth' })
  } catch (error) {
    if (isCurrent()) showToast(error instanceof Error ? error.message : '定位渠道失败', 'warning')
  } finally {
    if (isCurrent()) channelStore.locateRequest = null
  }
}

const openAddChannel = (poolId = 'default') => dialogStore.openAddChannelModal(channelType.value, poolId)
const editChannel = (channel: Channel) => dialogStore.openEditChannelModal(channel, channel.kind ?? channelType.value)
const copyChannel = (channel: Channel) => dialogStore.openCopyChannelModal(channel, channel.kind ?? channelType.value)
const handleDeleteChannel = async (channelId: number, kind?: ApiTab) => {
  if (preparingDelete.value || pendingConfirm.value || channelStore.isDeletingChannel || channelStore.isSavingChannel) return
  const targetKind = kind ?? channelType.value
  const sourceKind = channelType.value
  preparingDelete.value = true
  try {
    const impact = await api.getChannelDeletionImpact(targetKind, channelId)
    if (sourceKind !== channelType.value) return
    askConfirm({
      title: '删除渠道',
      confirmText: '确认删除',
      message: `确定删除渠道“${impact.name}”吗？此操作不可撤销。${deletionImpactWarning(impact)}`,
      action: async () => {
        await deleteChannel(channelId, handleAuthError, targetKind)
        await refreshChannels()
      }
    })
  } catch (error) {
    handleAuthError(error)
  } finally {
    preparingDelete.value = false
  }
}

const routing = useRoutingModes(showToast)
const modes = computed(() => [
  ...(routing.disguiseProtocol.value ? [{
    key: 'disguise',
    label: routing.disguiseLabel.value,
    enabled: routing.disguiseEnabled.value,
    unknown: routing.disguiseUnknown.value,
    loading: routing.disguiseLoading.value,
    color: 'success',
    icon: routing.disguiseEnabled.value ? 'mdi-robot' : 'mdi-robot-outline',
    tooltip: routing.disguiseTooltip.value,
    toggle: routing.toggleClientDisguise
  }] : []),
  {
    key: 'fuzzy',
    label: '自动故障转移',
    enabled: routing.fuzzyEnabled.value,
    unknown: routing.fuzzyUnknown.value,
    loading: routing.fuzzyLoading.value,
    color: 'warning',
    icon: routing.fuzzyEnabled.value ? 'mdi-shield-refresh' : 'mdi-shield-off-outline',
    tooltip: routing.fuzzyTooltip.value,
    toggle: routing.toggleFuzzyMode
  }
])

// 切换协议时本页不重新挂载，只刷新对应协议的数据；各协议缓存独立，切换不闪烁。
watch(channelType, () => { cancelConfirm(); void refreshChannels() })

onMounted(() => {
  watch([() => channelStore.locateRequest, channelType], ([request, protocol], _oldValue, onCleanup) => {
    if (!request) return
    if (request.protocol !== protocol) {
      channelStore.locateRequest = null
      return
    }
    let current = true
    onCleanup(() => { current = false })
    void locateChannel(request, () => current && channelStore.locateRequest === request && channelType.value === protocol)
  }, { flush: 'post', immediate: true })
  void refreshChannels()
  void routing.loadModes()
  channelStore.startAutoRefresh()
})
onUnmounted(() => {
  channelStore.stopAutoRefresh()
  channelStore.locateRequest = null
})
</script>

<style scoped>
@reference "@/styles/index.css";

.channel-overview { @apply flex items-center justify-between flex-wrap [gap:12px_24px] mb-4 [padding:12px_20px] ; }
.overview-stats { @apply flex items-center flex-wrap [gap:8px_32px] m-0; }
.overview-stat { @apply flex items-baseline gap-2; }
.overview-stat dt { @apply text-muted-foreground [font-size:0.8125rem]; }
.overview-stat dd { @apply m-0 [font-size:1.25rem] [font-weight:650] [line-height:1.4] tabular-nums [letter-spacing:-0.02em]; }
.overview-stat small { @apply ml-0.5 [font-size:0.8125rem] font-normal text-muted-foreground; }
.overview-status { @apply flex items-center gap-2 [font-size:0.9375rem]!; --status: var(--muted-foreground); }
.overview-status.is-ok { --status: var(--success); }
.overview-status.is-error { --status: var(--destructive); }
.overview-status__dot { @apply w-2 h-2 rounded-full [background:var(--status)] [box-shadow:0_0_0_3px_color-mix(in_srgb,_var(--status)_18%,_transparent)]; }
.overview-modes { @apply flex items-center flex-wrap gap-2; }
.channels-view :deep([id^='channel-'][tabindex='-1']) { scroll-margin-block: calc(var(--app-header-height) + 1.5rem); }
.channels-view :deep([id^='channel-'][tabindex='-1']:focus) { @apply rounded-control outline-2 outline-offset-2 outline-primary/70; animation: channel-locate 1800ms ease-out; }
@keyframes channel-locate { from { background-color: color-mix(in srgb, var(--primary) 18%, transparent); } to { background-color: transparent; } }
.mode-control.is-off { @apply text-muted-foreground; }
.mode-state { @apply ml-2 [padding:1px_6px] [font-size:0.75rem] rounded-badge [background:color-mix(in_srgb,_var(--foreground)_8%,_transparent)]; }
.channels-empty { @apply flex flex-col items-center [padding:56px_24px] text-center; }
@media (max-width: 600px) {
  .channel-overview { @apply p-3; }
  .overview-stats { @apply [gap:8px_20px]; }
  .overview-modes, .overview-modes .mode-control { @apply w-full; }
}
</style>
