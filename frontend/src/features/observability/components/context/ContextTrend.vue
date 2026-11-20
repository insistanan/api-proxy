<template>
  <section class="context-trend" aria-label="全会话上下文趋势">
    <div class="trend-heading">
      <div><strong>上下文趋势</strong><p v-if="overview">全会话 {{ overview.calls }} 次调用 · {{ overview.knownCalls }} 次有可比较快照</p></div>
      <UiBadge v-if="peakPoint" size="small" variant="tonal" color="secondary">峰值 {{ estimatedLabel(peakPoint.peak?.estimatedTokens || 0) }}</UiBadge>
    </div>
    <UiAlert v-if="error" type="error" variant="tonal" density="compact">{{ error }} <UiButton size="small" variant="text" @click="load">重试</UiButton></UiAlert>
    <p v-else-if="!overview">{{ loading ? '正在读取会话摘要…' : '暂无趋势数据' }}</p>
    <p v-else-if="overview.calls === 0">暂无会话日志，记录可能尚未生成或已清理。</p>
    <template v-if="overview && overview.calls > 0">
      <p class="trend-description">{{ overview.bucketSize > 1 ? `每组最多 ${overview.bucketSize} 次调用，显示组内上下文估算峰值` : '按调用顺序展示已保存输入的上下文估算' }}。尾部摘要、缺失数据与服务端历史引用不参与比较。</p>
      <template v-if="overview.knownCalls">
        <apexchart type="bar" height="230" :options="chartOptions" :series="series" @data-point-selection="selectPoint" />
        <div class="trend-selection">
          <UiSelect v-model="selectedPointIndex" :items="pointItems" label="选择趋势点" density="compact" hide-details clearable />
          <UiButton size="small" variant="tonal" color="primary" :disabled="!selectedPoint?.requestId" @click="selectedPoint?.requestId && emit('locate', selectedPoint.requestId)">定位调用</UiButton>
          <span v-if="selectedDelta != null" :class="selectedDelta > 0 ? 'delta-up' : selectedDelta < 0 ? 'delta-down' : ''">
            较前一快照 {{ selectedDelta > 0 ? '+' : '' }}{{ selectedDelta.toLocaleString() }} Token
          </span>
        </div>
        <details class="trend-data">
          <summary>查看上下文估算数据</summary>
          <div class="trend-data__scroll">
            <UiTable density="compact">
              <thead><tr><th>时间</th><th>调用 / 有快照</th><th>峰值估算</th><th>请求</th></tr></thead>
              <tbody><tr v-for="(point, index) in overview.points" :key="index"><td>{{ formatDateTime(point.firstAt) }}</td><td>{{ point.calls }} / {{ point.knownCalls }}</td><td>{{ point.peak ? estimatedLabel(point.peak.estimatedTokens) : '--' }}</td><td><UiButton size="x-small" variant="text" color="primary" :disabled="!point.requestId" @click="point.requestId && emit('locate', point.requestId)">{{ point.requestId?.slice(0, 8) || '--' }}</UiButton></td></tr></tbody>
            </UiTable>
          </div>
        </details>
      </template>
      <p v-else>历史日志尚未记录可比较的上下文摘要；新调用将在结束后出现在这里。</p>
      <div class="trend-scale"><span>会话开始</span><span>柱形为组内峰值，不表示每一轮都达到该值</span><span>最近调用</span></div>
    </template>
  </section>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiTable from '@/shared/ui/data/Table.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import type { ApexOptions } from 'apexcharts'
import { api, ApiError } from '@/api'
import type { ContextOverview, ContextTrendPoint } from '@/types/logs'
import { contextKind, estimatedLabel } from '@/features/observability/utils/contextPresentation'
import { formatDateTime } from '@/shared/utils/datetime'
import { defineLazyChart } from '@/shared/utils/lazyChart'
import { useChartColors } from '@/shared/theme'

const apexchart = defineLazyChart(() => import('vue3-apexcharts'))
const props = defineProps<{ groupKey: string; version: string }>()
const emit = defineEmits<{ locate: [requestId: string] }>()
const overview = shallowRef<ContextOverview | null>(null)
const loading = ref(false)
const error = ref('')
const selectedPointIndex = ref<number | null>(null)
const chartColors = useChartColors()
let revision = 0
let loadedKey = ''
const peakPoint = computed(() => overview.value?.points.reduce<ContextTrendPoint | undefined>((highest, point) => (point.peak?.estimatedTokens || 0) > (highest?.peak?.estimatedTokens || 0) ? point : highest, undefined))
const selectedPoint = computed(() => typeof selectedPointIndex.value === 'number' ? overview.value?.points[selectedPointIndex.value] : undefined)
const selectedDelta = computed(() => {
  const index = selectedPointIndex.value
  if (typeof index !== 'number' || !selectedPoint.value?.peak) return null
  const previous = overview.value?.points.slice(0, index).reverse().find(point => point.peak)?.peak
  return previous ? selectedPoint.value.peak.estimatedTokens - previous.estimatedTokens : null
})
const pointItems = computed(() => overview.value?.points.map((point, index) => ({ title: `${formatDateTime(point.firstAt)} · ${point.peak ? estimatedLabel(point.peak.estimatedTokens) : '无快照'}`, value: index })) || [])
const kinds = computed(() => Array.from(new Set(overview.value?.points.flatMap(point => point.peak?.parts.map(part => part.kind) || []) || [])))
const series = computed(() => kinds.value.map(kind => ({
  name: contextKind(kind).label,
  data: overview.value?.points.map(point => ({
    x: point.firstAt,
    y: point.peak?.parts.find(part => part.kind === kind)?.estimatedTokens || 0
  })) || []
})))
const chartOptions = computed<ApexOptions>(() => {
  const colors = chartColors.value
  return {
    chart: { type: 'bar', stacked: true, background: 'transparent', animations: { enabled: false }, toolbar: { show: false } },
    theme: { mode: chartColors.value.isDark ? 'dark' : 'light' },
    colors: kinds.value.map(kind => contextKind(kind).color),
    plotOptions: { bar: { columnWidth: '72%', borderRadius: 2 } },
    dataLabels: { enabled: false },
    grid: { borderColor: colors.grid, strokeDashArray: 3, padding: { left: 10, right: 10 } },
    xaxis: { type: 'datetime', labels: { datetimeUTC: false, format: 'MM/dd HH:mm', style: { colors: colors.text, fontSize: '11px' } }, axisBorder: { show: false }, axisTicks: { show: false } },
    yaxis: { min: 0, title: { text: '估算 Token', style: { color: colors.text, fontWeight: 400 } }, labels: { style: { colors: colors.text, fontSize: '11px' }, formatter: (value: number) => new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 }).format(value) } },
    legend: { show: true, position: 'top', horizontalAlign: 'left', fontSize: '11px', labels: { colors: colors.text }, markers: { size: 5, strokeWidth: 0 }, itemMargin: { horizontal: 8 } },
    tooltip: { theme: chartColors.value.isDark ? 'dark' : 'light', shared: true, intersect: false, x: { format: 'yyyy-MM-dd HH:mm:ss' }, y: { formatter: (value: number) => estimatedLabel(Math.round(value)) } },
    noData: { text: '暂无可比较快照', style: { color: colors.text } }
  }
})
function selectPoint(_event: unknown, _seriesIndex: number, config: { dataPointIndex?: number }) {
  if (typeof config.dataPointIndex === 'number') selectedPointIndex.value = config.dataPointIndex
}
async function load() {
  const version = ++revision
  if (loadedKey !== props.groupKey) { overview.value = null; loadedKey = props.groupKey; selectedPointIndex.value = null }
  loading.value = true
  error.value = ''
  try {
    const result = await api.getLogContextOverview(props.groupKey)
    if (version === revision) overview.value = result
  } catch (failure) {
    if (version !== revision) return
    if (failure instanceof ApiError && failure.status === 404) overview.value = { calls: 0, knownCalls: 0, bucketSize: 1, points: [] }
    else error.value = failure instanceof Error ? failure.message : '读取上下文趋势失败'
  }
  finally { if (version === revision) loading.value = false }
}
watch(() => [props.groupKey, props.version], () => { void load() }, { immediate: true })
onBeforeUnmount(() => { revision++ })
</script>

<style scoped>
@reference "@/styles/index.css";

.context-trend { @apply mb-5 p-4 [border:1px_solid_color-mix(in_srgb,_var(--outline)_18%,_transparent)] rounded-card [background:color-mix(in_srgb,_var(--surface-bright)_25%,_transparent)]; }
.trend-heading { @apply flex items-start justify-between gap-3; }
.trend-heading p, .trend-description, .trend-scale { @apply [margin:5px_0_0] text-muted-foreground [font-size:12px]; }
.trend-description { @apply mb-2; }
.trend-selection { @apply flex items-center gap-2.5 flex-wrap; }
.trend-selection .ui-field-root { @apply [flex:1_1_260px]; }
.trend-selection > span { @apply [font-size:12px] tabular-nums; }
.delta-up { @apply text-warning; }
.delta-down { @apply text-success; }
.trend-data { @apply mt-2 text-muted-foreground [font-size:12px]; }
.trend-data summary { @apply [padding:6px_0] cursor-pointer; }
.trend-data__scroll { @apply max-h-65 overflow-auto; }
.trend-scale { @apply flex justify-between gap-3 flex-wrap mt-2; }
</style>
