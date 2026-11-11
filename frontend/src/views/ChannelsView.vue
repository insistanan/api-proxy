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
      @add-channel="openAddChannel"
      @edit="editChannel"
      @delete="deleteChannel($event, handleAuthError)"
      @ping="pingChannel"
      @refresh="refreshChannels"
      @error="showToast($event, 'error')"
      @success="showToast($event, 'info')"
    />

    <v-card v-else class="channels-empty">
      <v-icon size="40" color="primary">mdi-server-network</v-icon>
      <div class="text-h6 font-weight-bold mt-4">暂无渠道</div>
      <p class="text-body-2 text-medium-emphasis mt-2 mb-6">添加第一个上游渠道后，即可通过本代理转发 {{ navigation?.label }} 请求。</p>
      <v-btn color="primary" prepend-icon="mdi-plus" @click="openAddChannel()">添加渠道</v-btn>
    </v-card>

    <AddChannelModal
      v-model:show="dialogStore.showAddChannelModal"
      :channel="dialogStore.editingChannel"
      :channel-type="dialogStore.channelType"
      :initial-pool-id="dialogStore.initialPoolId"
      :saving="channelStore.isSavingChannel || channelStore.isDeletingChannel"
      @save="(channel, options) => saveChannel(channel, options, handleAuthError)"
    />

    <!-- AI 配置助手只在渠道页提供。 -->
    <AssistantWidget />
  </div>
</template>

<script setup lang="ts">
// 渠道页：概览、路由开关、流量统计与渠道编排。
// 渠道数据由 channelStore 持有；本页负责进入时加载、停留期间自动刷新、离开时停止。
import { computed, onMounted, onUnmounted, watch } from 'vue'
import PageHeader from '@/components/common/PageHeader.vue'
import DisclosureSection from '@/components/common/DisclosureSection.vue'
import ChannelOrchestration from '@/components/ChannelOrchestration.vue'
import ChannelTableDrawer from '@/components/channel/ChannelTableDrawer.vue'
import AddChannelModal from '@/components/AddChannelModal.vue'
import AssistantWidget from '@/components/AssistantWidget.vue'
import { useAuthFlow } from '@/composables/useAuthFlow'
import { useChannelActions } from '@/composables/useChannelActions'
import { useCountUp } from '@/composables/useCountUp'
import { useRoutingModes } from '@/composables/useRoutingModes'
import { useToast } from '@/composables/useToast'
import { findNavigation } from '@/router/navigation'
import type { Channel } from '@/services/api'
import { useChannelStore } from '@/stores/channel'
import { useDialogStore } from '@/stores/dialog'
import { usePreferencesStore } from '@/stores/preferences'
import { defineLazyChart } from '@/utils/lazyChart'

const GlobalStatsChart = defineLazyChart(() => import('@/components/GlobalStatsChart.vue'))

const channelStore = useChannelStore()
const dialogStore = useDialogStore()
const preferencesStore = usePreferencesStore()
const { showToast } = useToast()
const { handleAuthError } = useAuthFlow(showToast)

// 协议类型以 store 中经过校验的 activeTab 为准，非法路由参数回落到 Messages。
const channelType = computed(() => channelStore.activeTab)
const navigation = computed(() => findNavigation(`/channels/${channelType.value}`))
const channels = computed(() => channelStore.currentChannelsData.channels)

// 数据刷新时数字平滑过渡，而非硬跳变。
const { displayValue: totalChannelsDisplay } = useCountUp(computed(() => channels.value.length))
const { displayValue: activeChannelsDisplay } = useCountUp(computed(() => channelStore.activeChannelCount))

const serviceStatus = computed(() => {
  if (channelStore.lastRefreshSuccess === null) return { text: '连接中', tone: 'pending' }
  return channelStore.lastRefreshSuccess ? { text: '运行中', tone: 'ok' } : { text: '连接失败', tone: 'error' }
})

// 渠道写操作由 useChannelActions 编排（R5，方案 8.1）；已提交但收尾失败时由它提示刷新（R3）。
const { refreshChannels: refreshAction, saveChannel, deleteChannel, pingChannel, pingAllChannels } = useChannelActions(showToast)
const refreshChannels = () => refreshAction(handleAuthError)

const openAddChannel = (poolId = 'default') => dialogStore.openAddChannelModal(channelType.value, poolId)
const editChannel = (channel: Channel) => dialogStore.openEditChannelModal(channel, channelType.value)

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
watch(channelType, () => void refreshChannels())

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
