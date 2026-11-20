<template>
  <section class="request-stats" :class="{ compact }">
    <div v-if="showTitle || !compact" class="stats-heading">
      <div v-if="showTitle">
        <h2>调用统计</h2>
        <p>从持久化调用日志聚合，按上游尝试计数，可逐层反查原始记录。</p>
      </div>
      <div class="stats-actions">
        <v-select v-model="autoRefreshSeconds" :items="refreshItems" label="自动刷新" density="compact" hide-details class="refresh-select" />
        <v-btn variant="tonal" size="small" prepend-icon="mdi-download" :disabled="loading || !stats?.dataPoints.length" @click="exportStats">导出统计</v-btn>
        <v-btn variant="tonal" size="small" prepend-icon="mdi-refresh" :loading="loading" @click="refreshData()">刷新</v-btn>
        <v-btn size="small" color="primary" variant="outlined" prepend-icon="mdi-text-box-search-outline" :disabled="loading || !stats" @click="openLogs()">查看日志</v-btn>
      </div>
    </div>

    <div class="stats-filters">
      <v-btn-toggle v-model="selectedDuration" mandatory density="compact" variant="outlined" divided color="primary" class="duration-toggle">
        <v-btn v-for="item in durationItems" :key="item.value" :value="item.value" size="small">{{ item.title }}</v-btn>
      </v-btn-toggle>
      <template v-if="selectedDuration === 'custom'">
        <v-text-field v-model="fromTime" type="datetime-local" label="开始时间" density="compact" hide-details />
        <v-text-field v-model="toTime" type="datetime-local" label="结束时间" density="compact" hide-details />
        <v-btn color="primary" size="small" variant="flat" @click="applyCustomRange">应用范围</v-btn>
      </template>
      <v-btn size="small" variant="text" :aria-expanded="showFilters" @click="showFilters = !showFilters">
        {{ showFilters ? '收起筛选' : '筛选' }}{{ activeFilterCount ? ` · ${activeFilterCount}` : '' }}
      </v-btn>
      <v-switch v-model="comparePrevious" label="对比上期" color="primary" density="compact" hide-details class="compare-switch" />
    </div>
    <div v-if="showFilters" class="stats-filters extra-filters">
      <v-select v-if="showProtocol" v-model="selectedType" :items="protocolItems" label="协议" density="compact" hide-details clearable class="filter-field" />
      <v-autocomplete v-model="selectedChannel" :items="stats?.facets.channels || []" label="渠道" density="compact" hide-details clearable class="filter-field" />
      <v-autocomplete v-model="selectedModel" :items="stats?.facets.models || []" label="模型" density="compact" hide-details clearable class="filter-field" />
      <v-select v-model="selectedStatus" :items="statusItems" label="状态" density="compact" hide-details class="filter-field" />
      <v-select v-model="selectedErrorType" :items="stats?.errors.map(item => item.name) || []" label="错误类型" density="compact" hide-details clearable class="filter-field" />
      <v-combobox v-model="selectedUpstream" :items="stats?.upstreams?.map(item => item.name) || []" label="上游地址" density="compact" hide-details clearable class="filter-field" />
      <v-combobox v-model="selectedKeyMask" :items="stats?.keys?.map(item => item.name) || []" label="密钥掩码" density="compact" hide-details clearable class="filter-field" />
      <v-btn size="small" variant="text" :disabled="!activeFilterCount" @click="clearFilters">清除筛选</v-btn>
    </div>

    <v-alert v-if="errorMessage" type="error" variant="tonal" density="compact" class="my-3" closable @click:close="errorMessage = ''">
      {{ errorMessage }}<span v-if="stats">；下方保留上一次成功加载的数据。</span>
    </v-alert>
    <v-progress-linear :active="loading" :indeterminate="loading" color="primary" height="2" class="mt-3" />

    <template v-if="stats">
      <StatsMetricCards :summary="stats.summary" :previous="comparePrevious ? stats.previousSummary : undefined" :compact="compact" class="mt-3" />
      <StatsTrendChart
        v-model:mode="trendMode"
        :points="stats.dataPoints"
        :loading="loading"
        :from="stats.from"
        :to="stats.to"
        :compact="compact"
        class="mt-3"
        @point-select="drillPoint"
      />
      <StatsBreakdown v-if="!compact" v-model:sort="dimensionSort" :stats="stats" :loading="loading" class="mt-3" @select="selectDimension" />
      <div class="stats-footnote">
        <span>聚合粒度 {{ stats.interval }} · 调用日志保留 {{ stats.retentionDays }} 天；超出保留期的数据不会被估算。</span>
        <span>{{ formatDateTime(stats.from) }} — {{ formatDateTime(stats.to) }}</span>
      </div>
    </template>
    <div v-else-if="loading" class="stats-initial-loading"><v-progress-circular indeterminate color="primary" /><span>正在汇总调用记录…</span></div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { api, type LogAPIType, type RequestStatsDrill, type RequestStatsParams, type RequestStatsResponse, type StatsDuration } from '@/api'
import type { RequestStatsDimensionSort } from '@/types/metrics'
import StatsMetricCards from '@/features/observability/components/stats/StatsMetricCards.vue'
import StatsTrendChart, { type StatsTrendMode } from '@/features/observability/components/stats/StatsTrendChart.vue'
import StatsBreakdown, { type StatsBreakdownKind } from '@/features/observability/components/stats/StatsBreakdown.vue'
import { downloadCsv } from '@/features/observability/utils/exportCsv'
import { formatDateTime, toDateTimeLocal } from '@/shared/utils/datetime'

const props = withDefaults(defineProps<{
  apiType?: LogAPIType | ''
  compact?: boolean
  showTitle?: boolean
  showProtocol?: boolean
  defaultDuration?: Exclude<StatsDuration, 'custom'>
}>(), { apiType: '', compact: false, showTitle: true, showProtocol: false, defaultDuration: '24h' })
const emit = defineEmits<{ drill: [filters: RequestStatsDrill] }>()

const durationItems: Array<{ title: string; value: StatsDuration }> = [
  { title: '1h', value: '1h' }, { title: '6h', value: '6h' }, { title: '24h', value: '24h' },
  { title: '今日', value: 'today' }, { title: '7天', value: '7d' }, { title: '30天', value: '30d' }, { title: '自定义', value: 'custom' }
]
const protocolItems = [
  { title: '全部协议', value: '' }, { title: 'Messages', value: 'messages' }, { title: 'Responses', value: 'responses' },
  { title: 'Gemini', value: 'gemini' }, { title: 'Chat', value: 'chat' }, { title: 'Images', value: 'images' },
  { title: 'Embeddings', value: 'embeddings' }, { title: 'Decisions', value: 'decisions' },
  { title: '命令 AI 复核', value: 'cmd_gate' }, { title: '内部 AI 调用', value: 'llmgate' }
]
const statusItems = [
  { title: '全部状态', value: '' }, { title: '已完成', value: 'completed' }, { title: '失败', value: 'failed' },
  { title: '已取消', value: 'cancelled' }, { title: '未正常收尾', value: 'interrupted' }, { title: '已拦截', value: 'blocked' }, { title: '状态未知', value: 'unknown' }
]
const refreshItems = [
  { title: '关闭', value: 0 }, { title: '5 秒', value: 5 }, { title: '15 秒', value: 15 },
  { title: '30 秒', value: 30 }, { title: '60 秒', value: 60 }
]

const stats = shallowRef<RequestStatsResponse | null>(null)
const loading = ref(false)
const errorMessage = ref('')
const selectedDuration = ref<StatsDuration>(props.defaultDuration)
const selectedType = ref<LogAPIType | ''>(props.apiType)
const selectedChannel = ref<string | null>(null)
const selectedModel = ref<string | null>(null)
const selectedStatus = ref('')
const selectedErrorType = ref<string | null>(null)
const selectedUpstream = ref<string | null>(null)
const selectedKeyMask = ref<string | null>(null)
const showFilters = ref(false)
const dimensionSort = ref<RequestStatsDimensionSort>('failures')
const activeFilterCount = computed(() => [
  selectedType.value && selectedType.value !== props.apiType,
  selectedChannel.value, selectedModel.value, selectedStatus.value,
  selectedErrorType.value, selectedUpstream.value, selectedKeyMask.value
].filter(Boolean).length)

function clearFilters() {
  selectedType.value = props.apiType
  selectedChannel.value = null
  selectedModel.value = null
  selectedStatus.value = ''
  selectedErrorType.value = null
  selectedUpstream.value = null
  selectedKeyMask.value = null
}

const comparePrevious = ref(true)
const trendMode = ref<StatsTrendMode>('traffic')
const autoRefreshSeconds = ref(0)
const fromTime = ref(toDateTimeLocal(Date.now() - 3600000))
const toTime = ref(toDateTimeLocal(Date.now()))
let revision = 0
let loadedFilters: RequestStatsDrill = {}

function readPreferences() {
  if (typeof window === 'undefined') return
  try {
    const key = `requestStats:${props.apiType || 'all'}`
    const savedDuration = window.localStorage.getItem(`${key}:duration`) as StatsDuration | null
    if (savedDuration && durationItems.some(item => item.value === savedDuration)) selectedDuration.value = savedDuration
    const savedMode = window.localStorage.getItem(`${key}:mode`) as StatsTrendMode | null
    if (savedMode && ['traffic', 'tokens', 'cache', 'latency', 'errors'].includes(savedMode)) trendMode.value = savedMode
  } catch { /* 隐私模式下使用默认偏好 */ }
}
function savePreference(name: string, value: string) {
  if (typeof window === 'undefined') return
  try { window.localStorage.setItem(`requestStats:${props.apiType || 'all'}:${name}`, value) } catch { /* 存储不可用时不影响查询 */ }
}

function filters(): RequestStatsDrill {
  return {
    type: selectedType.value || '', channel: selectedChannel.value || '', model: selectedModel.value || '',
    status: selectedStatus.value, errorType: selectedErrorType.value || '',
    upstream: selectedUpstream.value || '', keyMask: selectedKeyMask.value || ''
  }
}
function queryParams(): RequestStatsParams {
  const params: RequestStatsParams = { ...filters(), compare: comparePrevious.value, dimensionSort: dimensionSort.value }
  if (selectedDuration.value === 'custom') {
    const from = new Date(fromTime.value)
    const to = new Date(toTime.value)
    if (!fromTime.value || !toTime.value || Number.isNaN(from.getTime()) || Number.isNaN(to.getTime())) throw new Error('请选择有效的自定义开始和结束时间')
    if (from >= to) throw new Error('开始时间必须早于结束时间')
    params.from = from.toISOString()
    params.to = to.toISOString()
  } else {
    params.duration = selectedDuration.value
  }
  return params
}

async function loadStats(silent = false) {
  const current = ++revision
  if (!silent) loading.value = true
  errorMessage.value = ''
  try {
    const params = queryParams()
    const result = await api.getRequestStats(params)
    if (current === revision) {
      const { type, channel, model, status, errorType, upstream, keyMask } = params
      loadedFilters = { type, channel, model, status, errorType, upstream, keyMask }
      stats.value = result
    }
  } catch (error) {
    if (current === revision) errorMessage.value = error instanceof Error ? error.message : '读取调用统计失败'
  } finally {
    if (current === revision && !silent) loading.value = false
  }
}
function applyCustomRange() { void loadStats() }
function openLogs(extra: RequestStatsDrill = {}) {
  if (!stats.value || loading.value) return
  emit('drill', { ...loadedFilters, from: stats.value.from, to: stats.value.to, ...extra })
}
function drillPoint(index: number) {
  const points = stats.value?.dataPoints
  const point = points?.[index]
  if (!point || !stats.value) return
  const next = points?.[index + 1]
  openLogs({ from: point.timestamp, to: next ? new Date(new Date(next.timestamp).getTime() - 1).toISOString() : stats.value.to })
}
function selectDimension(payload: { kind: StatsBreakdownKind; name: string; drill: boolean }) {
  const name = payload.name
  const extra: Record<StatsBreakdownKind, RequestStatsDrill> = {
    channels: { channel: name }, models: { model: name }, errors: { errorType: name },
    protocols: { type: name as LogAPIType }, statuses: { status: name },
    upstreams: { upstream: name }, keys: { keyMask: name }
  }
  if (payload.drill) {
    openLogs(extra[payload.kind])
    return
  }
  showFilters.value = true
  switch (payload.kind) {
    case 'channels': selectedChannel.value = name; break
    case 'models': selectedModel.value = name; break
    case 'errors': selectedErrorType.value = name; break
    case 'protocols': selectedType.value = name as LogAPIType; break
    case 'statuses': selectedStatus.value = name; break
    case 'upstreams': selectedUpstream.value = name; break
    case 'keys': selectedKeyMask.value = name; break
  }
}
function exportStats() {
  if (!stats.value) return
  downloadCsv(`调用统计-${toDateTimeLocal(stats.value.from).replace(/:/g, '-')}.csv`,
    ['时间', '上游尝试', '成功', '失败/中止', '输入 Token', '输出 Token', '缓存创建', '缓存读取', '重试', '平均耗时(ms)', '平均首字(ms)'],
    stats.value.dataPoints.map(point => [point.timestamp, point.requestCount, point.successCount, point.failureCount, point.inputTokens, point.outputTokens, point.cacheCreationTokens, point.cacheReadTokens, point.retriedCount, Math.round(point.avgDurationMs), Math.round(point.avgFirstTokenMs)]))
}

watch([selectedType, selectedChannel, selectedModel, selectedStatus, selectedErrorType, selectedUpstream, selectedKeyMask, comparePrevious, dimensionSort], () => { void loadStats() })
watch(selectedDuration, value => {
  savePreference('duration', value)
  if (value !== 'custom') void loadStats()
})
watch(trendMode, value => savePreference('mode', value))
watch(autoRefreshSeconds, (seconds, _previous, onCleanup) => {
  if (!seconds) return
  const timer = window.setInterval(() => { if (!document.hidden && !loading.value) void loadStats(true) }, seconds * 1000)
  onCleanup(() => window.clearInterval(timer))
})
watch(() => props.apiType, value => {
  selectedType.value = value || ''
  stats.value = null
  void loadStats()
})
onMounted(() => { readPreferences(); void loadStats() })
onBeforeUnmount(() => { revision++ })

async function refreshData(silent = false) { await loadStats(silent) }
function startAutoRefresh() { if (!autoRefreshSeconds.value) autoRefreshSeconds.value = 15 }
function stopAutoRefresh() { autoRefreshSeconds.value = 0 }
defineExpose({ refreshData, startAutoRefresh, stopAutoRefresh })
</script>

<style scoped>
.request-stats { min-width: 0; }
.stats-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 14px; }
.stats-heading h2 { margin: 0; font-size: 17px; }
.stats-heading p { margin: 5px 0 0; color: rgb(var(--v-theme-on-surface-variant)); font-size: 12px; }
.stats-actions { display: flex; align-items: center; justify-content: flex-end; gap: 8px; flex-wrap: wrap; margin-left: auto; }
.refresh-select { width: 112px; flex: 0 0 112px; }
.stats-filters { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; padding: 12px; border: 1px solid rgba(var(--v-theme-outline), .18); border-radius: 12px; background: rgba(var(--v-theme-surface-bright), .35); }
.extra-filters { margin-top: 6px; }
.duration-toggle { flex-shrink: 0; }
.filter-field { flex: 1 1 145px; min-width: 135px; max-width: 220px; }
.compare-switch { flex: 0 0 auto; margin-left: 4px; }
.stats-footnote { display: flex; justify-content: space-between; gap: 12px; flex-wrap: wrap; margin-top: 10px; color: rgb(var(--v-theme-on-surface-variant)); font-size: 11px; }
.stats-initial-loading { display: flex; align-items: center; justify-content: center; gap: 12px; min-height: 220px; color: rgb(var(--v-theme-on-surface-variant)); }
@media (max-width: 900px) { .stats-heading { align-items: flex-start; flex-direction: column; } .stats-actions { justify-content: flex-start; margin-left: 0; } .duration-toggle { max-width: 100%; overflow-x: auto; } }
@media (max-width: 600px) { .stats-filters { align-items: stretch; } .stats-filters > :deep(.v-input), .stats-filters > :deep(.v-btn) { flex: 1 1 140px; max-width: none; } .refresh-select { flex-basis: 100px; } }
</style>
