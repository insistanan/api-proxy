<template>
  <!-- 路由视图根节点必须是单个真实 DOM 元素。 -->
  <div class="channels-view">
    <PageHeader :title="`${navigation?.label} 渠道`" :description="navigation?.description">
      <ChannelTableDrawer :channel-type="channelType" :channels="channels">
        <template #activator="{ props: activatorProps }">
          <v-btn v-bind="activatorProps" variant="tonal" prepend-icon="mdi-format-list-bulleted">模型列表</v-btn>
        </template>
      </ChannelTableDrawer>
      <v-tooltip text="刷新" location="bottom">
        <template #activator="{ props: tooltipProps }">
          <v-btn v-bind="tooltipProps" icon="mdi-refresh" variant="text" aria-label="刷新" @click="refreshChannels" />
        </template>
      </v-tooltip>
      <v-btn color="info" variant="tonal" prepend-icon="mdi-speedometer" :loading="channelStore.isPingingAll" @click="pingAllChannels">
        测试延迟
      </v-btn>
      <v-btn color="primary" prepend-icon="mdi-plus" @click="openAddChannel()">添加渠道</v-btn>
    </PageHeader>

    <!-- 概览与路由开关合并为一行，把首屏高度留给渠道列表。 -->
    <section class="channel-overview" aria-label="渠道概览">
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
        <v-tooltip v-for="mode in modes" :key="mode.key" location="bottom" content-class="status-tooltip">
          <template #activator="{ props: tooltipProps }">
            <v-btn
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
              <v-icon start size="18">{{ mode.unknown ? 'mdi-alert-circle-outline' : mode.icon }}</v-icon>
              {{ mode.label }}
              <span class="mode-state">{{ mode.unknown ? '未知' : mode.enabled ? '开' : '关' }}</span>
            </v-btn>
          </template>
          <span>{{ mode.tooltip }}</span>
        </v-tooltip>
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

    <v-card v-else>
      <EmptyState
        icon="mdi-server-network"
        title="暂无渠道"
        :description="`添加第一个上游渠道后，即可通过本代理转发 ${navigation?.label} 请求。`"
      >
        <v-btn color="primary" prepend-icon="mdi-plus" @click="openAddChannel()">添加渠道</v-btn>
      </EmptyState>
    </v-card>

    <AddChannelModal
      v-model:show="dialogStore.showAddChannelModal"
      v-model:channel-type="dialogStore.channelType"
      :channel="dialogStore.editingChannel"
      :copy-source="dialogStore.copyingChannel"
      :copy-source-type="dialogStore.copySourceType"
      :initial-pool-id="dialogStore.initialPoolId"
      :saving="channelStore.isSavingChannel || channelStore.isDeletingChannel"
      @save="handleSaveChannel"
    />

    <!-- AI 配置助手只在渠道页提供。 -->
    <AssistantWidget />
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
// 渠道页：概览、路由开关、流量统计与渠道编排。
// 渠道数据由 channelStore 持有；本页负责进入时加载、停留期间自动刷新、离开时停止。
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import ConfirmDialog from '@/shared/components/common/ConfirmDialog.vue'
import { useConfirm } from '@/shared/composables/useConfirm'
import { deletionImpactWarning } from '@/features/channels/utils/deletionImpact'
import PageHeader from '@/shared/components/common/PageHeader.vue'
import EmptyState from '@/shared/components/common/EmptyState.vue'
import DisclosureSection from '@/shared/components/common/DisclosureSection.vue'
import ChannelOrchestration from '@/features/channels/components/dashboard/ChannelOrchestration.vue'
import ChannelTableDrawer from '@/features/channels/components/dashboard/ChannelTableDrawer.vue'
import AddChannelModal from '@/features/channels/components/form/AddChannelModal.vue'
import { AssistantWidget } from '@/features/assistant'
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
import { useAuthStore } from '@/shared/stores/auth'
import { defineLazyChart } from '@/shared/utils/lazyChart'

const GlobalStatsChart = defineLazyChart(() => import('@/features/channels/components/dashboard/GlobalStatsChart.vue'))

const channelStore = useChannelStore()
const router = useRouter()
const dialogStore = useDialogStore()
const preferencesStore = usePreferencesStore()
const authStore = useAuthStore()
const { showToast } = useToast()
const { pending: pendingConfirm, askConfirm, acceptConfirm, cancelConfirm } = useConfirm()
const preparingDelete = ref(false)

const handleAuthError = (error: unknown): void => {
  if (error instanceof Error && error.message.includes('认证失败')) {
    authStore.setAuthError('访问密钥无效或已过期，请重新输入')
  } else {
    showToast(`操作失败: ${error instanceof Error ? error.message : '未知错误'}`, 'error')
  }
}

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
const { refreshChannels: refreshAction, saveChannel, deleteChannel, pingChannel, pingAllChannels } = useChannelActions(showToast)

// 公用图片理解池跨协议共享；任何渠道写入都可能改变它，刷新时机跟随渠道列表。
const refreshVisionChannelPool = async () => {
  try {
    await channelStore.refreshVisionChannels()
  } catch (error) {
    handleAuthError(error)
  }
}
const refreshChannels = async () => {
  await refreshAction(handleAuthError)
  await refreshVisionChannelPool()
}

const openAddChannel = (poolId = 'default') => dialogStore.openAddChannelModal(channelType.value, poolId)
const editChannel = (channel: Channel) => dialogStore.openEditChannelModal(channel, channel.kind ?? channelType.value)
const copyChannel = (channel: Channel) => dialogStore.openCopyChannelModal(channel, channel.kind ?? channelType.value)
const handleSaveChannel = async (
  channel: Omit<Channel, 'id' | 'index' | 'latency'>,
  options?: { isQuickAdd?: boolean }
) => {
  const copying = !!dialogStore.copyingChannel
  const targetType = dialogStore.channelType
  await saveChannel(channel, options, handleAuthError)
  await refreshVisionChannelPool()
  if (copying && !dialogStore.showAddChannelModal && targetType !== channelType.value) {
    try {
      await router.push(`/channels/${targetType}`)
    } catch (error) {
      showToast(`渠道已创建，但切换协议失败：${error instanceof Error ? error.message : '未知错误'}`, 'warning')
    }
  }
}
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
  void refreshChannels()
  void routing.loadModes()
  channelStore.startAutoRefresh()
})
onUnmounted(() => channelStore.stopAutoRefresh())
</script>

<style scoped>
.channel-overview { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px 24px; margin-bottom: 16px; padding: 12px 20px; background: rgb(var(--v-theme-surface)); border: 1px solid rgba(var(--v-theme-outline), 0.24); border-radius: var(--cut-md); }
.overview-stats { display: flex; align-items: center; flex-wrap: wrap; gap: 8px 32px; margin: 0; }
.overview-stat { display: flex; align-items: baseline; gap: 8px; }
.overview-stat dt { color: rgb(var(--v-theme-on-surface-variant)); font-size: 0.8125rem; }
.overview-stat dd { margin: 0; font-size: 1.25rem; font-weight: 650; line-height: 1.4; font-variant-numeric: tabular-nums; letter-spacing: -0.02em; }
.overview-stat small { margin-left: 2px; font-size: 0.8125rem; font-weight: 400; color: rgb(var(--v-theme-on-surface-variant)); }
.overview-status { display: flex; align-items: center; gap: 8px; font-size: 0.9375rem !important; --status: var(--v-theme-on-surface-variant); }
.overview-status.is-ok { --status: var(--v-theme-success); }
.overview-status.is-error { --status: var(--v-theme-error); }
.overview-status__dot { width: 8px; height: 8px; border-radius: 50%; background: rgb(var(--status)); box-shadow: 0 0 0 3px rgba(var(--status), 0.18); }
.overview-modes { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; }
.mode-control.is-off { color: rgb(var(--v-theme-on-surface-variant)); }
.mode-state { margin-left: 8px; padding: 1px 6px; font-size: 0.75rem; border-radius: 4px; background: rgba(var(--v-theme-on-surface), 0.08); }
.channels-empty { display: flex; flex-direction: column; align-items: center; padding: 56px 24px; text-align: center; }
@media (max-width: 600px) {
  .channel-overview { padding: 12px; }
  .overview-stats { gap: 8px 20px; }
  .overview-modes, .overview-modes .mode-control { width: 100%; }
}
</style>
