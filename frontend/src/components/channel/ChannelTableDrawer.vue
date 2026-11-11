<template>
  <v-dialog
    v-model="open"
    class="channel-table-drawer"
    transition="slide-x-reverse-transition"
    :aria-labelledby="titleId"
  >
    <template #activator="slotProps"><slot name="activator" v-bind="slotProps"></slot></template>
    <v-card class="channel-list-sheet">
      <header class="drawer-header">
        <div>
          <h2 :id="titleId" class="text-h6">{{ protocolLabel }} 渠道列表</h2>
          <p class="text-body-2 text-medium-emphasis">查看当前协议下所有分组的渠道</p>
        </div>
        <v-btn icon="mdi-close" variant="text" aria-label="关闭渠道列表" @click="open = false" />
      </header>

      <div class="drawer-filters">
        <v-text-field v-model="keyword" label="搜索渠道、模型或 ID" prepend-inner-icon="mdi-magnify" clearable hide-details density="compact" class="drawer-search" />
        <v-select v-model="poolFilter" :items="poolOptions" label="分组" hide-details density="compact" />
        <v-select v-model="stateFilter" :items="stateOptions" label="状态 / 用途" hide-details density="compact" />
        <v-btn variant="tonal" prepend-icon="mdi-refresh" :loading="loading" @click="refresh(true)">刷新列表</v-btn>
      </div>
      <p class="drawer-caption">
        组内顺序为配置顺序。近 1 小时为渠道汇总，具体模型的可用性请展开「模型观测」。
      </p>
      <v-alert v-if="error" type="error" variant="tonal" class="mx-4 mb-3" role="alert">{{ error }}；已加载的数据予以保留。</v-alert>
      <v-alert v-else-if="store.lastRefreshSuccess === false" type="warning" variant="tonal" class="mx-4 mb-3" role="status">
        渠道自动刷新失败，当前展示的是上次加载的数据。
      </v-alert>
      <v-progress-linear :active="loading" indeterminate color="primary" aria-label="加载渠道列表" />

      <v-table fixed-header class="channel-list-table" density="comfortable">
        <thead>
          <tr>
            <th scope="col">渠道 / 上游协议</th>
            <th scope="col">分组 / 组内顺序</th>
            <th scope="col">模型映射</th>
            <th scope="col">状态</th>
            <th scope="col">近 1 小时</th>
            <th scope="col">密钥数</th>
            <th scope="col">操作</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="row in visibleRows" :key="row.key">
            <tr>
              <td class="channel-cell">
                <strong>{{ row.channel.name || '未命名渠道' }}</strong>
                <div class="cell-secondary">{{ channelServiceLabel(channelType, row.channel.serviceType) }}</div>
                <div v-if="row.channel.description" class="cell-secondary">{{ row.channel.description }}</div>
              </td>
              <td class="pool-cell">
                <span :class="{ 'text-warning': !row.pool }">{{ row.pool?.name || '未找到分组' }}</span>
                <div class="cell-secondary">{{ row.order ? `组内第 ${row.order} 位` : row.pool ? '不参与常规编排' : '顺序未知' }}</div>
                <div v-if="!row.pool" class="cell-secondary">{{ row.poolId }}</div>
              </td>
              <td class="mapping-cell">
                <ChannelMappingBlock :channel="row.channel" :channel-type="channelType" :preview-limit="32" @edit="edit(row.channel)" />
                <div v-if="row.channel.responsesModel" class="cell-secondary">主模型：{{ row.channel.responsesModel }}</div>
              </td>
              <td>
                <ChannelStatusBadge :status="row.channel.status || 'active'" :metrics="row.metrics" size="small" />
                <div v-if="row.channel.excludeFromConversation" class="cell-secondary">仅视觉辅助</div>
              </td>
              <td class="stats-cell">
                <template v-if="row.stats">
                  <strong>{{ row.stats.requestCount }} 次请求</strong>
                  <div class="cell-secondary">成功率 {{ successRate(row.stats) }}</div>
                </template>
                <span v-else class="cell-secondary">暂无统计</span>
              </td>
              <td class="stats-cell">{{ row.channel.builtinProvider ? '免 Key' : row.channel.apiKeys.length }}</td>
              <td>
                <div class="row-actions">
                  <v-btn variant="text" size="small" :aria-label="`管理渠道 ${row.channel.name}`" @click="edit(row.channel)">{{ row.channel.builtinProvider ? '免费配置' : '编辑' }}</v-btn>
                  <v-btn
                    variant="text" size="small" color="primary"
                    :disabled="!row.channel.id"
                    :aria-label="`查看 ${row.channel.name} 的模型观测`"
                    :aria-expanded="expanded === row.key"
                    @click="expanded = expanded === row.key ? '' : row.key"
                  >模型观测</v-btn>
                </div>
              </td>
            </tr>
            <tr v-if="open && expanded === row.key && row.channel.id">
              <td colspan="7" class="model-detail">
                <h3 class="text-subtitle-2 mb-3">{{ row.channel.name }} · 模型可用性</h3>
                <ModelAvailabilityPanel :key="`${channelType}:${row.channel.id}`" :channel-type="channelType" :channels="channels" :fixed-channel-id="row.channel.id" />
              </td>
            </tr>
          </template>
          <tr v-if="!visibleRows.length">
            <td colspan="7" class="empty-cell">
              {{ loading ? '正在加载渠道列表…' : error ? '渠道列表加载失败，请重试。' : rows.length ? '没有符合筛选条件的渠道。' : '当前协议尚未添加渠道。' }}
            </td>
          </tr>
        </tbody>
      </v-table>

      <footer class="drawer-footer">
        <span class="text-caption text-medium-emphasis">共 {{ rows.length }} 个渠道 · 筛选后 {{ filteredRows.length }} 个</span>
        <v-pagination v-if="pageCount > 1" v-model="page" :length="pageCount" :total-visible="3" density="comfortable" />
      </footer>
    </v-card>
  </v-dialog>
  <v-dialog v-model="freeDialog" max-width="680">
    <v-card>
      <div class="d-flex justify-end"><v-btn icon="mdi-close" variant="text" aria-label="关闭免费配置" @click="freeDialog = false" /></div>
      <FreeChannelPool :channel-type="channelType" :channels="channels" />
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, useId, watch } from 'vue'
import type { ApiTab } from '@/services/channel-api'
import type { Channel, ChannelMetrics, TimeWindowStats } from '@/types/channel'
import { useChannelStore } from '@/stores/channel'
import { findNavigation } from '@/router/navigation'
import { channelServiceLabel } from '@/utils/imagesChannelProtocol'
import ChannelMappingBlock from '@/components/ChannelMappingBlock.vue'
import ChannelStatusBadge from '@/components/ChannelStatusBadge.vue'
import ModelAvailabilityPanel from './ModelAvailabilityPanel.vue'
import FreeChannelPool from './FreeChannelPool.vue'

const props = defineProps<{ channelType: ApiTab; channels: Channel[] }>()
const emit = defineEmits<{ edit: [channel: Channel] }>()
const store = useChannelStore()
const titleId = useId()
const open = ref(false)
const freeDialog = ref(false)
const keyword = ref<string | null>('')
const poolFilter = ref('')
const stateFilter = ref('all')
const expanded = ref('')
const page = ref(1)
const loading = ref(false)
const error = ref('')
let refreshRevision = 0

const protocolLabel = computed(() => findNavigation(`/channels/${props.channelType}`)?.label)
const pools = computed(() => store.channelPoolsMap[props.channelType])
const metricsByIndex = computed(() => new Map(store.currentDashboardMetrics.map(metrics => [metrics.channelIndex, metrics])))
const stateOptions = [
  { title: '全部状态', value: 'all' },
  { title: '活跃', value: 'active' },
  { title: '熔断', value: 'open' },
  { title: '探测中', value: 'half_open' },
  { title: '禁用 / 备用', value: 'disabled' },
  { title: '弃用', value: 'deprecated' },
  { title: '仅视觉辅助', value: 'vision' },
  { title: '错误', value: 'error' },
  { title: '未知', value: 'unknown' }
]

function displayState(channel: Channel, metrics?: ChannelMetrics) {
  const status = channel.status || 'active'
  if (status === 'suspended') return 'open'
  if (status !== 'active' && status !== 'healthy') return status
  return metrics?.circuitState && metrics.circuitState !== 'closed' ? metrics.circuitState : 'active'
}
function isScheduled(channel: Channel) {
  return !channel.excludeFromConversation && ['active', 'suspended'].includes(channel.status || 'active')
}
function priority(channel: Channel) {
  return channel.priority || channel.index + 1
}

const rows = computed(() => {
  const poolMap = new Map(pools.value.map((pool, index) => [pool.id, { pool, index }]))
  const ordered = props.channels.filter(channel => channel.status !== 'deleted').slice().sort((left, right) => {
    const leftPool = poolMap.get(left.poolId || 'default')?.index ?? pools.value.length
    const rightPool = poolMap.get(right.poolId || 'default')?.index ?? pools.value.length
    return leftPool - rightPool || Number(isScheduled(right)) - Number(isScheduled(left)) || priority(left) - priority(right) || left.index - right.index
  })
  const positions = new Map<string, number>()
  return ordered.map(channel => {
    const poolId = channel.poolId || 'default'
    const pool = poolMap.get(poolId)?.pool
    const order = pool && isScheduled(channel) ? (positions.get(poolId) ?? 0) + 1 : undefined
    if (order) positions.set(poolId, order)
    const metrics = metricsByIndex.value.get(channel.index)
    return {
      key: channel.id || `index:${channel.index}`, channel, poolId, pool, order, metrics,
      stats: metrics?.timeWindows?.['1h'], state: displayState(channel, metrics)
    }
  })
})
const poolOptions = computed(() => [
  { title: '全部分组', value: '' },
  ...pools.value.map(pool => ({ title: pool.name, value: pool.id })),
  ...[...new Set(rows.value.filter(row => !row.pool).map(row => row.poolId))]
    .map(id => ({ title: `未找到分组（${id}）`, value: id }))
])
const filteredRows = computed(() => {
  const query = (keyword.value ?? '').trim().toLowerCase()
  return rows.value.filter(row => {
    if (poolFilter.value && row.poolId !== poolFilter.value) return false
    if (stateFilter.value === 'vision' ? !row.channel.excludeFromConversation : stateFilter.value !== 'all' && row.state !== stateFilter.value) return false
    if (!query) return true
    const channel = row.channel
    const models = Object.entries(channel.modelMapping ?? {}).flatMap(([source, targets]) => [source, ...targets])
    return [channel.id, channel.name, channel.description, channel.defaultModel, channel.responsesModel, ...models]
      .join(' ').toLowerCase().includes(query)
  })
})
const pageCount = computed(() => Math.ceil(filteredRows.value.length / 20))
const visibleRows = computed(() => filteredRows.value.slice((page.value - 1) * 20, page.value * 20))

function successRate(stats: TimeWindowStats) {
  return stats.requestCount > 0 && Number.isFinite(stats.successRate) ? `${stats.successRate.toFixed(1)}%` : '—'
}
function edit(channel: Channel) {
  if (channel.builtinProvider) {
    freeDialog.value = true
    return
  }
  open.value = false
  emit('edit', channel)
}
async function refresh(includeChannels = false) {
  const revision = ++refreshRevision
  const type = props.channelType
  loading.value = true
  error.value = ''
  const requests = [{ label: '分组', promise: store.refreshChannelPools(type) }]
  if (includeChannels) requests.push({ label: '渠道数据', promise: store.refreshChannels(type) })
  const results = await Promise.allSettled(requests.map(request => request.promise))
  // 关闭或切换协议后的结果只更新 store，不再覆盖下一次打开时的查询状态。
  if (revision !== refreshRevision) return
  error.value = results.flatMap((result, index) => result.status === 'rejected'
    ? [`${requests[index].label}加载失败：${result.reason instanceof Error ? result.reason.message : String(result.reason)}`] : []).join('；')
  loading.value = false
}

watch(open, value => {
  expanded.value = ''
  if (value) void refresh()
  else { refreshRevision++; loading.value = false }
})
watch(() => props.channelType, () => {
  open.value = false
  refreshRevision++
  keyword.value = ''
  poolFilter.value = ''
  stateFilter.value = 'all'
  expanded.value = ''
  page.value = 1
  error.value = ''
  loading.value = false
})
watch([keyword, poolFilter, stateFilter], () => { page.value = 1; expanded.value = '' })
watch(pageCount, count => { page.value = Math.max(1, Math.min(page.value, count)) })
watch(visibleRows, visible => {
  if (!visible.some(row => row.key === expanded.value)) expanded.value = ''
})
onUnmounted(() => { refreshRevision++ })
</script>

<style scoped>
:global(.channel-table-drawer.v-dialog) { align-items: stretch; justify-content: flex-end; }
:global(.channel-table-drawer.v-dialog > .v-overlay__content) { width: min(1180px, 100%); max-width: 100%; height: 100%; max-height: 100%; margin: 0; }
:global(.channel-table-drawer.v-dialog > .v-overlay__content > .channel-list-sheet) { border-radius: 0; overflow: hidden; }
:global(.channel-table-drawer > .slide-x-reverse-transition-enter-from),
:global(.channel-table-drawer > .slide-x-reverse-transition-leave-to) { transform: translateX(100%); }
.drawer-header { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 20px 16px 16px 24px; flex-shrink: 0; }
.drawer-filters { display: grid; grid-template-columns: minmax(200px, 2fr) repeat(2, minmax(140px, 1fr)) auto; align-items: center; gap: 12px; padding: 0 24px 12px; flex-shrink: 0; }
.drawer-caption { padding: 0 24px 12px; font-size: 0.8125rem; color: rgb(var(--v-theme-on-surface-variant)); }
.channel-list-table { flex: 1 1 auto; min-height: 120px; border-radius: 0; }
.channel-list-table > :deep(.v-table__wrapper > table) { min-width: 1020px; }
.channel-list-table th { white-space: nowrap; }
.channel-list-table td { padding-top: 12px !important; padding-bottom: 12px !important; }
.channel-cell { min-width: 160px; max-width: 220px; overflow-wrap: anywhere; }
.pool-cell { min-width: 140px; max-width: 180px; overflow-wrap: anywhere; }
.mapping-cell { min-width: 220px; max-width: 280px; overflow-wrap: anywhere; }
.mapping-cell :deep(.meta-block) { max-width: 100%; }
.stats-cell { white-space: nowrap; font-variant-numeric: tabular-nums; }
.cell-secondary { font-size: 0.75rem; line-height: 1.6; margin-top: 4px; color: rgb(var(--v-theme-on-surface-variant)); }
.row-actions { display: flex; align-items: center; gap: 4px; white-space: nowrap; }
.model-detail { background: rgba(var(--v-theme-on-surface), 0.025); }
.empty-cell { height: 180px !important; text-align: center; color: rgb(var(--v-theme-on-surface-variant)); }
.drawer-footer { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 8px; min-height: 60px; padding: 8px 24px; border-top: 1px solid rgba(var(--v-theme-outline), 0.24); flex-shrink: 0; }
@media (max-width: 700px) {
  .drawer-header { padding: 16px; }
  .drawer-filters { grid-template-columns: repeat(2, minmax(0, 1fr)); padding: 0 16px 12px; gap: 8px; }
  .drawer-search { grid-column: 1 / -1; }
  .drawer-caption { padding: 0 16px 12px; }
  .drawer-footer { padding: 8px 16px; }
}
@media (max-height: 540px) {
  :global(.channel-table-drawer.v-dialog > .v-overlay__content > .channel-list-sheet) { overflow-y: auto; }
  .channel-list-table { flex-shrink: 0; }
}
@media (prefers-reduced-motion: reduce) {
  :global(.channel-table-drawer > .v-overlay__content) { transition: none !important; animation: none !important; }
}
</style>
