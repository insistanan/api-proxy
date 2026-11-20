<template>
  <section class="trend-panel" aria-label="调用趋势">
    <div class="trend-panel__header">
      <div>
        <h3>{{ currentMode.label }}</h3>
        <p>{{ currentMode.description }}</p>
      </div>
      <v-btn-toggle :model-value="mode" mandatory density="compact" variant="outlined" divided color="primary" @update:model-value="emit('update:mode', $event)">
        <v-btn v-for="item in modes" :key="item.value" :value="item.value" size="small">{{ item.shortLabel }}</v-btn>
      </v-btn-toggle>
    </div>
    <v-progress-linear :active="loading" :indeterminate="loading" color="primary" height="2" />
    <div v-if="!hasData && !loading" class="trend-empty">
      <EmptyState variant="inline" icon="mdi-chart-timeline-variant" title="当前范围没有调用数据" description="可以扩大时间范围、清除筛选，或检查调用日志保留策略。" />
    </div>
    <template v-else>
      <apexchart :type="chartType" :height="height" :options="chartOptions" :series="series" @data-point-selection="handlePointSelect" />
      <details class="trend-data">
        <summary>按时间段查日志</summary>
        <div class="trend-data__scroll">
          <v-table density="compact">
            <thead><tr><th>时间</th><th>尝试</th><th>成功</th><th>失败/中止</th><th>输入</th><th>输出</th><th>平均耗时</th></tr></thead>
            <tbody><tr v-for="(point, index) in points" :key="point.timestamp"><td><v-btn size="x-small" variant="text" color="primary" :disabled="loading || !point.requestCount" @click="emit('pointSelect', index)">{{ formatShortDateTime(point.timestamp) }}</v-btn></td><td>{{ point.requestCount }}</td><td>{{ point.successCount }}</td><td>{{ point.failureCount }}</td><td>{{ point.inputTokens }}</td><td>{{ point.outputTokens }}</td><td>{{ formatStatMs(point.avgDurationMs) }}</td></tr></tbody>
          </v-table>
        </div>
      </details>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useChartDarkTheme } from '@/shared/composables/useChartDarkTheme'
import type { ApexOptions } from 'apexcharts'
import type { RequestStatsPoint } from '@/types/metrics'
import { defineLazyChart } from '@/shared/utils/lazyChart'
import { chartThemeColors, GLOBAL_STATS_COLORS, CHART_PALETTE } from '@/shared/utils/chartPalette'
import { formatCompactNumber, formatStatMs } from '@/features/observability/utils/statsFormat'
import { formatShortDateTime } from '@/shared/utils/datetime'
import EmptyState from '@/shared/components/common/EmptyState.vue'

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
const isDark = useChartDarkTheme()

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
    case 'traffic': return [GLOBAL_STATS_COLORS.traffic.primary, GLOBAL_STATS_COLORS.traffic.success, CHART_PALETTE.danger]
    case 'tokens': return [GLOBAL_STATS_COLORS.tokens.input, GLOBAL_STATS_COLORS.tokens.output]
    case 'cache': return [CHART_PALETTE.teal, CHART_PALETTE.copper]
    case 'latency': return [CHART_PALETTE.rose, CHART_PALETTE.blue]
    case 'errors': return [CHART_PALETTE.danger, CHART_PALETTE.ochre]
    default: return []
  }
})
const longRange = computed(() => props.from && props.to ? new Date(props.to).getTime() - new Date(props.from).getTime() > 36 * 3600000 : false)
const chartOptions = computed<ApexOptions>(() => {
  const colorsForTheme = chartThemeColors(isDark.value)
  const latency = props.mode === 'latency'
  return {
    chart: {
      background: 'transparent', foreColor: colorsForTheme.text, fontFamily: 'inherit', animations: { enabled: false },
      toolbar: { show: true, tools: { download: false, selection: true, zoom: true, zoomin: true, zoomout: true, pan: false, reset: true } },
      zoom: { enabled: true, type: 'x', autoScaleYaxis: true }
    },
    theme: { mode: isDark.value ? 'dark' : 'light' },
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
      theme: isDark.value ? 'dark' : 'light', shared: true, intersect: false,
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
.trend-panel { position: relative; min-width: 0; padding: 18px 20px 12px; border: 1px solid rgba(var(--v-theme-outline), .18); border-radius: 12px; background: rgb(var(--v-theme-surface)); }
.trend-panel__header { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 12px; }
h3 { margin: 0; font-size: 15px; font-weight: 650; }
p { margin: 5px 0 0; color: rgb(var(--v-theme-on-surface-variant)); font-size: 12px; }
.trend-empty { min-height: 260px; display: grid; place-items: center; }
.trend-data { margin-top: 4px; color: rgb(var(--v-theme-on-surface-variant)); font-size: 12px; }
.trend-data summary { padding: 8px 0; cursor: pointer; }
.trend-data__scroll { max-height: 300px; overflow: auto; }
@media (max-width: 900px) { .trend-panel__header { flex-direction: column; } .trend-panel__header .v-btn-toggle { max-width: 100%; overflow-x: auto; } }
@media (max-width: 600px) { .trend-panel { padding: 14px 10px 10px; } }
</style>
