<template>
  <section class="trend-panel" aria-label="调用趋势">
    <div class="trend-panel__header">
      <div>
        <h3>{{ currentMode.label }}</h3>
        <p>{{ currentMode.description }}</p>
      </div>
      <UiToggleGroup :model-value="mode" mandatory density="compact" variant="outlined" divided color="primary" @update:model-value="emit('update:mode', $event)">
        <UiButton v-for="item in modes" :key="item.value" :value="item.value" size="small">{{ item.shortLabel }}</UiButton>
      </UiToggleGroup>
    </div>
    <UiProgress :active="loading" :indeterminate="loading" color="primary" height="2" />
    <div v-if="!hasData && !loading" class="trend-empty">
      <EmptyState variant="inline" icon="mdi-chart-timeline-variant" title="当前范围没有调用数据" description="可以扩大时间范围、清除筛选，或检查调用日志保留策略。" />
    </div>
    <template v-else>
      <apexchart :type="chartType" :height="height" :options="chartOptions" :series="series" @data-point-selection="handlePointSelect" />
      <details class="trend-data">
        <summary>按时间段查日志</summary>
        <div class="trend-data__scroll">
          <UiTable density="compact">
            <thead><tr><th>时间</th><th>尝试</th><th>成功</th><th>失败/中止</th><th>输入</th><th>输出</th><th>平均耗时</th></tr></thead>
            <tbody><tr v-for="(point, index) in points" :key="point.timestamp"><td><UiButton size="x-small" variant="text" color="primary" :disabled="loading || !point.requestCount" @click="emit('pointSelect', index)">{{ formatShortDateTime(point.timestamp) }}</UiButton></td><td>{{ point.requestCount }}</td><td>{{ point.successCount }}</td><td>{{ point.failureCount }}</td><td>{{ point.inputTokens }}</td><td>{{ point.outputTokens }}</td><td>{{ formatStatMs(point.avgDurationMs) }}</td></tr></tbody>
          </UiTable>
        </div>
      </details>
    </template>
  </section>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiToggleGroup from '@/shared/ui/button/ToggleGroup.vue'
import UiTable from '@/shared/ui/data/Table.vue'
import UiProgress from '@/shared/ui/feedback/Progress.vue'
import { computed } from 'vue'
import type { ApexOptions } from 'apexcharts'
import type { RequestStatsPoint } from '@/types/metrics'
import { defineLazyChart } from '@/shared/utils/lazyChart'
import { useChartColors } from '@/shared/theme'
import { formatCompactNumber, formatStatMs } from '@/features/observability/utils/statsFormat'
import { formatShortDateTime } from '@/shared/utils/datetime'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'

export type StatsTrendMode = 'traffic' | 'tokens' | 'cache' | 'latency' | 'errors'

const apexchart = defineLazyChart(() => import('vue3-apexcharts'))
const props = withDefaults(defineProps<{
  points: RequestStatsPoint[]
  mode: StatsTrendMode
  loading?: boolean
  from?: string
  to?: string
  compact?: boolean
}>(), { loading: false, compact: false })
const emit = defineEmits<{ 'update:mode': [value: StatsTrendMode]; pointSelect: [index: number] }>()

const modes: Array<{ value: StatsTrendMode; label: string; shortLabel: string; description: string }> = [
  { value: 'traffic', label: '请求与成功趋势', shortLabel: '请求', description: '按上游尝试计数；同一客户端请求的重试会分别计入。' },
  { value: 'tokens', label: 'Token 输入与输出', shortLabel: 'Token', description: '每个时间段的输入与输出用量。' },
  { value: 'cache', label: '缓存读取与创建', shortLabel: '缓存', description: '缓存命中率的汇总口径：读取 ÷（读取 + 非缓存输入）。' },
  { value: 'latency', label: '耗时与首字趋势', shortLabel: '耗时', description: '每个时间段内有效样本的平均耗时。' },
  { value: 'errors', label: '失败与重试趋势', shortLabel: '异常', description: '失败包含中止；重试是带有重试标记的上游尝试。' }
]
const currentMode = computed(() => modes.find(item => item.value === props.mode) || modes[0]!)
const chartType = computed(() => props.mode === 'errors' ? 'bar' : props.mode === 'latency' ? 'line' : 'area')
const height = computed(() => props.compact ? 220 : 320)
const hasData = computed(() => props.points.some(point => point.requestCount > 0 || point.inputTokens > 0 || point.outputTokens > 0))
const chartColors = useChartColors()

const series = computed(() => {
  const points = props.points
  const data = (pick: (point: RequestStatsPoint) => number | null) => points.map(point => ({ x: new Date(point.timestamp).getTime(), y: pick(point) }))
  switch (props.mode) {
    case 'traffic': return [
      { name: '上游尝试', data: data(point => point.requestCount) },
      { name: '成功', data: data(point => point.successCount) },
      { name: '失败/中止', data: data(point => point.failureCount) }
    ]
    case 'tokens': return [
      { name: '输入 Token', data: data(point => point.inputTokens) },
      { name: '输出 Token', data: data(point => point.outputTokens) }
    ]
    case 'cache': return [
      { name: '缓存读取', data: data(point => point.cacheReadTokens) },
      { name: '缓存创建', data: data(point => point.cacheCreationTokens) }
    ]
    case 'latency': return [
      { name: '平均总耗时', data: data(point => point.avgDurationMs > 0 ? Math.round(point.avgDurationMs) : null) },
      { name: '平均首字耗时', data: data(point => point.avgFirstTokenMs > 0 ? Math.round(point.avgFirstTokenMs) : null) }
    ]
    case 'errors': return [
      { name: '失败/中止', data: data(point => point.failureCount) },
      { name: '重试', data: data(point => point.retriedCount) }
    ]
    default: return []
  }
})
const colors = computed(() => {
  switch (props.mode) {
    case 'traffic': return [chartColors.value.indigo, chartColors.value.jade, chartColors.value.danger]
    case 'tokens': return [chartColors.value.violet, chartColors.value.copper]
    case 'cache': return [chartColors.value.teal, chartColors.value.copper]
    case 'latency': return [chartColors.value.rose, chartColors.value.blue]
    case 'errors': return [chartColors.value.danger, chartColors.value.ochre]
    default: return []
  }
})
const longRange = computed(() => props.from && props.to ? new Date(props.to).getTime() - new Date(props.from).getTime() > 36 * 3600000 : false)
const chartOptions = computed<ApexOptions>(() => {
  const colorsForTheme = chartColors.value
  const latency = props.mode === 'latency'
  return {
    chart: {
      background: 'transparent', foreColor: colorsForTheme.text, fontFamily: 'inherit', animations: { enabled: false },
      toolbar: { show: true, tools: { download: false, selection: true, zoom: true, zoomin: true, zoomout: true, pan: false, reset: true } },
      zoom: { enabled: true, type: 'x', autoScaleYaxis: true }
    },
    theme: { mode: chartColors.value.isDark ? 'dark' : 'light' },
    colors: colors.value,
    dataLabels: { enabled: false },
    stroke: { curve: 'straight', width: props.mode === 'errors' ? 0 : 2, dashArray: 0 },
    fill: { type: chartType.value === 'area' ? 'gradient' : 'solid', gradient: { opacityFrom: .28, opacityTo: .03, stops: [0, 95, 100] } },
    plotOptions: { bar: { columnWidth: '55%', borderRadius: 3 } },
    grid: { borderColor: colorsForTheme.grid, strokeDashArray: 3, padding: { left: 12, right: 12 } },
    xaxis: {
      type: 'datetime', labels: { datetimeUTC: false, format: longRange.value ? 'MM/dd HH:mm' : 'HH:mm', style: { colors: colorsForTheme.text, fontSize: '11px' } },
      axisBorder: { show: false }, axisTicks: { show: false }, tooltip: { enabled: false }
    },
    yaxis: {
      min: 0, labels: { formatter: (value: number) => latency ? formatStatMs(value) : formatCompactNumber(value), style: { colors: colorsForTheme.text, fontSize: '11px' } },
      title: { text: latency ? '毫秒' : props.mode === 'traffic' || props.mode === 'errors' ? '次' : 'Token', style: { color: colorsForTheme.text, fontWeight: 400 } }
    },
    legend: { show: true, position: 'top', horizontalAlign: 'right', fontSize: '12px', labels: { colors: colorsForTheme.text }, markers: { size: 6, strokeWidth: 0, shape: 'circle' }, itemMargin: { horizontal: 12 } },
    tooltip: {
      theme: chartColors.value.isDark ? 'dark' : 'light', shared: true, intersect: false,
      x: { format: 'yyyy-MM-dd HH:mm' },
      y: { formatter: (value: number) => latency ? formatStatMs(value) : `${formatStatNumberLocal(value)}${props.mode === 'traffic' || props.mode === 'errors' ? ' 次' : ' Token'}` }
    },
    noData: { text: '暂无数据', style: { color: colorsForTheme.text } }
  }
})
const formatStatNumberLocal = (value: number) => new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(value || 0)
function handlePointSelect(_event: unknown, _seriesIndex: number, config: { dataPointIndex?: number }) {
  if (typeof config.dataPointIndex === 'number') emit('pointSelect', config.dataPointIndex)
}
</script>

<style scoped>
@reference "@/styles/index.css";

.trend-panel { @apply relative min-w-0 [padding:18px_20px_12px] [border:1px_solid_color-mix(in_srgb,_var(--outline)_18%,_transparent)] rounded-card bg-card; }
.trend-panel__header { @apply flex items-start justify-between gap-4 mb-3; }
h3 { @apply m-0 [font-size:15px] [font-weight:650]; }
p { @apply [margin:5px_0_0] text-muted-foreground [font-size:12px]; }
.trend-empty { @apply min-h-65 grid [place-items:center]; }
.trend-data { @apply mt-1 text-muted-foreground [font-size:12px]; }
.trend-data summary { @apply [padding:8px_0] cursor-pointer; }
.trend-data__scroll { @apply max-h-75 overflow-auto; }
@media (max-width: 900px) { .trend-panel__header { @apply flex-col; } .trend-panel__header .ui-toggle-group { @apply max-w-full overflow-x-auto; } }
@media (max-width: 600px) { .trend-panel { @apply [padding:14px_10px_10px]; } }
</style>
