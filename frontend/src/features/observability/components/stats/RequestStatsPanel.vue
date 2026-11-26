<template>
  <section class="request-stats" :class="{ compact }">
    <div v-if="showTitle || !compact" class="stats-heading">
      <div v-if="showTitle">
        <h2>调用统计</h2>
        <p>从持久化调用日志聚合，按上游尝试计数，可逐层反查原始记录。</p>
      </div>
      <div class="stats-actions">
        <UiSelect v-model="autoRefreshSeconds" :items="refreshItems" label="自动刷新" density="compact" hide-details class="refresh-select" />
        <UiButton variant="tonal" size="small" prepend-icon="mdi-download" :disabled="loading || !stats?.dataPoints.length" @click="exportStats">导出统计</UiButton>
        <UiButton variant="tonal" size="small" prepend-icon="mdi-refresh" :loading="loading" @click="refreshData()">刷新</UiButton>
        <UiButton size="small" color="primary" variant="outlined" prepend-icon="mdi-text-box-search-outline" :disabled="loading || !stats" @click="openLogs()">查看日志</UiButton>
      </div>
    </div>

    <div class="stats-filters">
      <UiToggleGroup v-model="selectedDuration" mandatory density="compact" variant="outlined" divided color="primary" class="duration-toggle">
        <UiButton v-for="item in durationItems" :key="item.value" :value="item.value" size="small">{{ item.title }}</UiButton>
      </UiToggleGroup>
      <template v-if="selectedDuration === 'custom'">
        <UiInput v-model="fromTime" type="datetime-local" label="开始时间" density="compact" hide-details />
        <UiInput v-model="toTime" type="datetime-local" label="结束时间" density="compact" hide-details />
        <UiButton color="primary" size="small" variant="flat" @click="applyCustomRange">应用范围</UiButton>
      </template>
      <UiButton size="small" variant="text" :aria-expanded="showFilters" @click="showFilters = !showFilters">
        {{ showFilters ? '收起筛选' : '筛选' }}{{ activeFilterCount ? ` · ${activeFilterCount}` : '' }}
      </UiButton>
      <UiSwitch v-model="comparePrevious" label="对比上期" color="primary" density="compact" hide-details class="compare-switch" />
    </div>
    <div v-if="showFilters" class="stats-filters extra-filters">
      <UiSelect v-if="showProtocol" v-model="selectedType" :items="protocolItems" label="协议" density="compact" hide-details clearable class="filter-field" />
      <UiSelect v-model="selectedChannel" searchable :items="stats?.facets.channels || []" label="渠道" density="compact" hide-details clearable class="filter-field" />
      <UiSelect v-model="selectedModel" searchable :items="stats?.facets.models || []" label="模型" density="compact" hide-details clearable class="filter-field" />
      <UiSelect v-model="selectedStatus" :items="statusItems" label="状态" density="compact" hide-details class="filter-field" />
      <UiSelect v-model="selectedErrorType" :items="stats?.errors.map(item => item.name) || []" label="错误类型" density="compact" hide-details clearable class="filter-field" />
      <UiSelect v-model="selectedUpstream" searchable creatable :items="stats?.upstreams?.map(item => item.name) || []" label="上游地址" density="compact" hide-details clearable class="filter-field" />
      <UiSelect v-model="selectedKeyMask" searchable creatable :items="stats?.keys?.map(item => item.name) || []" label="密钥掩码" density="compact" hide-details clearable class="filter-field" />
      <UiButton size="small" variant="text" :disabled="!activeFilterCount" @click="clearFilters">清除筛选</UiButton>
    </div>

    <UiAlert v-if="errorMessage" type="error" variant="tonal" density="compact" class="my-3" closable @click:close="errorMessage = ''">
      {{ errorMessage }}<span v-if="stats">；下方保留上一次成功加载的数据。</span>
    </UiAlert>
    <UiProgress :active="loading" :indeterminate="loading" color="primary" height="2" class="mt-3" />

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
    <div v-else-if="loading" class="stats-initial-loading"><UiSpinner indeterminate color="primary" /><span>正在汇总调用记录…</span></div>
  </section>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiToggleGroup from '@/shared/ui/button/ToggleGroup.vue'
import UiSwitch from '@/shared/ui/check/Switch.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiProgress from '@/shared/ui/feedback/Progress.vue'
import UiSpinner from '@/shared/ui/feedback/Spinner.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
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

let inFlight = 0
let statsController: AbortController | undefined
async function loadStats(silent = false) {
  if (silent && inFlight > 0) return
  statsController?.abort()
  const controller = new AbortController()
  statsController = controller
  inFlight++
  const current = ++revision
  if (!silent) loading.value = true
  errorMessage.value = ''
  try {
    const params = queryParams()
    const result = await api.getRequestStats(params, controller.signal)
    if (current === revision) {
      const { type, channel, model, status, errorType, upstream, keyMask } = params
      loadedFilters = { type, channel, model, status, errorType, upstream, keyMask }
      stats.value = result
    }
  } catch (error) {
    if (current === revision) errorMessage.value = error instanceof Error ? error.message : '读取调用统计失败'
  } finally {
    inFlight--
    if (current === revision) loading.value = false
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
onBeforeUnmount(() => { revision++; statsController?.abort() })

async function refreshData(silent = false) { await loadStats(silent) }
function startAutoRefresh() { if (!autoRefreshSeconds.value) autoRefreshSeconds.value = 15 }
function stopAutoRefresh() { autoRefreshSeconds.value = 0 }
defineExpose({ refreshData, startAutoRefresh, stopAutoRefresh })
</script>

<style scoped>
@reference "@/styles/index.css";

.request-stats { @apply min-w-0; }
.stats-heading { @apply flex items-center justify-between gap-4 mb-3.5; }
.stats-heading h2 { @apply m-0 [font-size:17px]; }
.stats-heading p { @apply [margin:5px_0_0] text-muted-foreground [font-size:12px]; }
.stats-actions { @apply flex items-center justify-end gap-2 flex-wrap ml-auto; }
.refresh-select { @apply w-28 [flex:0_0_112px]; }
.stats-filters { @apply flex items-center gap-2.5 flex-wrap p-3 [border:1px_solid_color-mix(in_srgb,_var(--outline)_18%,_transparent)] rounded-card [background:color-mix(in_srgb,_var(--surface-bright)_35%,_transparent)]; }
.extra-filters { @apply mt-1.5; }
.duration-toggle { @apply shrink-0; }
.filter-field { @apply [flex:1_1_145px] [min-width:135px] max-w-55; }
.compare-switch { @apply [flex:0_0_auto] ml-1; }
.stats-footnote { @apply flex justify-between gap-3 flex-wrap mt-2.5 text-muted-foreground [font-size:11px]; }
.stats-initial-loading { @apply flex items-center justify-center gap-3 min-h-55 text-muted-foreground; }
@media (max-width: 900px) { .stats-heading { @apply items-start flex-col; } .stats-actions { @apply justify-start ml-0; } .duration-toggle { @apply max-w-full overflow-x-auto; } }
@media (max-width: 600px) { .stats-filters { @apply items-stretch; } .stats-filters > :deep(.ui-field-root), .stats-filters > :deep(.ui-button) { @apply [flex:1_1_140px] [max-width:none]; } .refresh-select { @apply [flex-basis:100px]; } }
</style>
