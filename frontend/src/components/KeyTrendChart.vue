<template>
  <div class="key-trend-chart-container">
    <v-snackbar v-model="showError" color="error" :timeout="3000" location="top">
      {{ errorMessage }}
      <template #actions>
        <v-btn variant="text" @click="showError = false">关闭</v-btn>
      </template>
    </v-snackbar>

    <!-- Header -->
    <div class="chart-header d-flex align-center justify-space-between mb-3 flex-wrap ga-2">
      <div class="d-flex align-center ga-2">
        <v-btn-toggle v-model="selectedDuration" mandatory density="compact" variant="outlined" divided :disabled="isLoading" color="primary">
          <v-btn value="1h" size="small">1h</v-btn>
          <v-btn value="6h" size="small">6h</v-btn>
          <v-btn value="24h" size="small">24h</v-btn>
          <v-btn value="today" size="small">今日</v-btn>
        </v-btn-toggle>
        <v-btn icon size="small" variant="text" :loading="isLoading" :disabled="isLoading" @click="refreshData">
          <v-icon>mdi-refresh</v-icon>
        </v-btn>
      </div>
      <v-btn-toggle v-model="selectedView" mandatory density="compact" variant="outlined" divided :disabled="isLoading" color="primary">
        <v-btn value="traffic" size="small">
          <v-icon start size="14">mdi-chart-line</v-icon>流量
        </v-btn>
        <v-btn value="tokens" size="small">
          <v-icon start size="14">mdi-chart-areaspline</v-icon>Token I/O
        </v-btn>
        <v-btn value="cache" size="small">
          <v-icon start size="14">mdi-database</v-icon>缓存
        </v-btn>
      </v-btn-toggle>
    </div>

    <!-- Loading -->
    <div v-if="isLoading" class="d-flex justify-center align-center" style="height: 240px">
      <v-progress-circular indeterminate size="28" color="primary" width="3" />
    </div>

    <!-- Empty -->
    <div v-else-if="!hasData" class="d-flex flex-column justify-center align-center text-medium-emphasis" style="height: 240px">
      <v-icon size="44" class="mb-2" :color="isDark ? 'grey-darken-1' : 'grey-lighten-1'">mdi-chart-timeline-variant</v-icon>
      <div class="text-caption">选定时间范围内没有 Key 使用记录</div>
    </div>

    <!-- Chart -->
    <div v-else class="chart-area">
      <apexchart ref="chartRef" type="area" height="280" :options="chartOptions" :series="chartSeries" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useTheme } from 'vuetify'
import VueApexCharts from 'vue3-apexcharts'
import type { ApexOptions } from 'apexcharts'
import { api, channelApiByType, type ChannelKeyMetricsHistoryResponse } from '../services/api'
import { useAutoRefresh } from '../composables/useAutoRefresh'

const apexchart = VueApexCharts

const props = defineProps<{
  channelId: number
  channelType: 'messages' | 'responses' | 'gemini' | 'chat' | 'images'
}>()

type ViewMode = 'traffic' | 'tokens' | 'cache'
type Duration = '1h' | '6h' | '24h' | 'today'

const getStorageKey = (channelType: string, key: string) => `keyTrendChart:${channelType}:${key}`

const isLocalStorageAvailable = (): boolean => {
  try { return typeof window !== 'undefined' && window.localStorage !== undefined }
  catch { return false }
}

const loadSavedPreferences = (channelType: string): { view: ViewMode; duration: Duration } => {
  if (!isLocalStorageAvailable()) return { view: 'tokens', duration: '1h' }
  try {
    const savedView = window.localStorage.getItem(getStorageKey(channelType, 'viewMode')) as ViewMode | null
    const savedDuration = window.localStorage.getItem(getStorageKey(channelType, 'duration')) as Duration | null
    return {
      view: savedView && ['traffic', 'tokens', 'cache'].includes(savedView) ? savedView : 'tokens',
      duration: savedDuration && ['1h', '6h', '24h', 'today'].includes(savedDuration) ? savedDuration : '1h'
    }
  } catch { return { view: 'tokens', duration: '1h' } }
}

const savePreference = (channelType: string, key: string, value: string) => {
  if (!isLocalStorageAvailable()) return
  try { window.localStorage.setItem(getStorageKey(channelType, key), value) } catch { /* ignore */ }
}

const theme = useTheme()
const isDark = computed(() => theme.global.current.value.dark)
const savedPrefs = loadSavedPreferences(props.channelType)

const selectedView = ref<ViewMode>(savedPrefs.view)
const selectedDuration = ref<Duration>(savedPrefs.duration)
const isLoading = ref(false)
const isRefreshing = ref(false)
const historyData = ref<ChannelKeyMetricsHistoryResponse | null>(null)
const showError = ref(false)
const errorMessage = ref('')
const chartRef = ref<InstanceType<typeof VueApexCharts> | null>(null)
let refreshRequestId = 0

const { start: startAutoRefresh, stop: stopAutoRefresh } = useAutoRefresh(
  () => refreshData(true), isRefreshing, 2000,
)

// 调色板 — 低饱和的十色环，与 Instrument Deck 主题同调；
// 明暗主题共用一套中间明度值，切主题时不需要重算系列色
const keyColors = [
  '#5C6BC8', '#C1804A', '#2FA478', '#8A72C0', '#C46A92',
  '#B99436', '#3E93A8', '#CA6072', '#7E9C46', '#6E77CE',
]

const FAILURE_RATE_THRESHOLD = 0.1

const AGGREGATION_INTERVALS: Record<Duration, number> = {
  '1h': 60000, '6h': 300000, '24h': 900000, 'today': 300000
}

const getAggregationInterval = (duration: Duration): number => {
  const interval = AGGREGATION_INTERVALS[duration]
  if (interval === undefined) return 60000
  return interval
}

const alignToBucket = (timestamp: number, interval: number): number =>
  Math.floor(timestamp / interval) * interval

const hasData = computed(() => {
  if (!historyData.value) return false
  return historyData.value.keys &&
    historyData.value.keys.length > 0 &&
    historyData.value.keys.some(k => k.dataPoints && k.dataPoints.length > 0)
})

// 失败率色带
const timePointFailureRates = computed(() => {
  if (!historyData.value?.keys?.length) return []
  const interval = getAggregationInterval(selectedDuration.value)
  const timeMap = new Map<number, { totalRequests: number; totalFailures: number }>()
  historyData.value.keys.forEach(keyData => {
    keyData.dataPoints?.forEach(dp => {
      const rawTs = new Date(dp.timestamp).getTime()
      const alignedTs = alignToBucket(rawTs, interval)
      const existing = timeMap.get(alignedTs) || { totalRequests: 0, totalFailures: 0 }
      existing.totalRequests += dp.requestCount
      existing.totalFailures += dp.failureCount
      timeMap.set(alignedTs, existing)
    })
  })
  return Array.from(timeMap.entries())
    .map(([timestamp, data]) => ({
      timestamp,
      failureRate: data.totalRequests > 0 ? data.totalFailures / data.totalRequests : 0
    }))
    .sort((a, b) => a.timestamp - b.timestamp)
})

const getFailureOpacity = (failureRate: number): number => {
  const minOpacity = 0.08; const maxOpacity = 0.65
  const normalizedRate = Math.min((failureRate - FAILURE_RATE_THRESHOLD) / (1 - FAILURE_RATE_THRESHOLD), 1)
  return minOpacity + normalizedRate * (maxOpacity - minOpacity)
}

const failureAnnotations = computed(() => {
  if (selectedView.value !== 'traffic') return []
  const rates = timePointFailureRates.value
  if (rates.length === 0) return []
  const DEFAULT_INTERVAL = getAggregationInterval(selectedDuration.value)
  const MAX_INTERVAL = DEFAULT_INTERVAL * 2
  const annotations: any[] = []
  rates.forEach((point, index) => {
    if (point.failureRate >= FAILURE_RATE_THRESHOLD) {
      let interval = DEFAULT_INTERVAL
      if (rates.length > 1) {
        if (index > 0) interval = point.timestamp - rates[index - 1].timestamp
        else if (index < rates.length - 1) interval = rates[index + 1].timestamp - point.timestamp
      }
      interval = Math.min(interval, MAX_INTERVAL)
      annotations.push({
        x: point.timestamp - interval / 2,
        x2: point.timestamp + interval / 2,
        fillColor: '#D05B5B',
        opacity: getFailureOpacity(point.failureRate),
        label: { text: '' }
      })
    }
  })
  return annotations
})

const getThemeColors = computed(() => ({
  grid: isDark.value ? 'rgba(255,255,255,0.06)' : 'rgba(0,0,0,0.06)',
  text: isDark.value ? '#A2AAB8' : '#555E6E',
}))

const getChartColors = (): string[] => {
  const keyCount = historyData.value?.keys?.length || 0
  if (keyCount === 0) return keyColors
  if (selectedView.value === 'traffic') {
    return historyData.value!.keys.map((_, i) => keyColors[i % keyColors.length])
  }
  const colors: string[] = []
  for (let i = 0; i < keyCount; i++) {
    const color = keyColors[i % keyColors.length]
    colors.push(color); colors.push(color)
  }
  return colors
}

const getDashArray = (): number | number[] => {
  if (selectedView.value === 'traffic') return 0
  const keyCount = historyData.value?.keys?.length || 0
  const dashArray: number[] = []
  for (let i = 0; i < keyCount; i++) { dashArray.push(0); dashArray.push(5) }
  return dashArray.length > 0 ? dashArray : 0
}

const formatNumber = (num: number): string => {
  if (num >= 1000000) return (num / 1000000).toFixed(1) + 'M'
  if (num >= 1000) return (num / 1000).toFixed(1) + 'K'
  return num.toFixed(0)
}

const formatAxisValue = (val: number, mode: ViewMode): string => {
  switch (mode) {
    case 'traffic': return Math.round(val).toString()
    case 'tokens': case 'cache': return formatNumber(Math.abs(val))
    default: return val.toString()
  }
}

const formatTooltipValue = (val: number, mode: ViewMode): string => {
  switch (mode) {
    case 'traffic': return `${Math.round(val)} 请求`
    case 'tokens': case 'cache': return formatNumber(Math.abs(val))
    default: return val.toString()
  }
}

// 自定义 tooltip
const escapeHtml = (str: string): string =>
  str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;')

const buildTrafficTooltip = ({ seriesIndex, dataPointIndex, w }: any): string => {
  if (!historyData.value?.keys) return ''
  const timestamp = w.globals.seriesX[seriesIndex][dataPointIndex]
  const date = new Date(timestamp)
  const timeStr = date.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
  const interval = getAggregationInterval(selectedDuration.value)
  const alignedTimestamp = alignToBucket(timestamp, interval)

  const keyStats: { keyMask: string; success: number; failure: number; total: number; color: string; model: string }[] = []
  let grandTotal = 0; let grandFailure = 0

  historyData.value.keys.forEach((keyData, keyIndex) => {
    const matchingPoints = keyData.dataPoints?.filter(p => {
      const dpTimestamp = new Date(p.timestamp).getTime()
      return alignToBucket(dpTimestamp, interval) === alignedTimestamp
    }) || []
    if (matchingPoints.length > 0) {
      const aggregated = matchingPoints.reduce((acc, dp) => {
        acc.success += dp.successCount; acc.failure += dp.failureCount
        acc.total += dp.requestCount
        if (dp.model) acc.models.push(dp.model)
        return acc
      }, { success: 0, failure: 0, total: 0, models: [] as string[] })
      const uniqueModels = Array.from(new Set(aggregated.models.flatMap(m => m.split(', '))).values()).filter(m => !!m)
      const modelStr = escapeHtml(uniqueModels.join(', '))
      if (aggregated.total > 0) {
        keyStats.push({ keyMask: escapeHtml(keyData.keyMask), success: aggregated.success, failure: aggregated.failure, total: aggregated.total, color: keyColors[keyIndex % keyColors.length], model: modelStr })
        grandTotal += aggregated.total; grandFailure += aggregated.failure
      }
    }
  })
  if (keyStats.length === 0) return ''

  const grandFailureRate = grandTotal > 0 ? (grandFailure / grandTotal * 100).toFixed(1) : '0'
  const hasFailure = grandFailure > 0

  let html = `<div style="padding: 10px 14px; font-size: 12px; min-width: 220px;">`
  html += `<div style="font-weight: 600; margin-bottom: 8px; color: ${hasFailure ? '#D05B5B' : 'inherit'}; border-bottom: 1px solid rgba(128,128,128,0.15); padding-bottom: 6px;">${timeStr}</div>`
  keyStats.forEach(stat => {
    const failureRate = stat.total > 0 ? (stat.failure / stat.total * 100).toFixed(0) : '0'
    const hasKeyFailure = stat.failure > 0
    html += `<div style="display: flex; align-items: center; margin: 5px 0; gap: 6px;">`
    html += `<span style="width: 8px; height: 8px; border-radius: 50%; background: ${stat.color}; flex-shrink: 0;"></span>`
    html += `<span style="flex: 1; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">${stat.keyMask}${stat.model ? ` <span style="font-size: 10px; color: #888;">(${stat.model})</span>` : ''}</span>`
    html += `<span style="font-weight: 600;">${stat.total}</span>`
    if (hasKeyFailure) {
      html += `<span style="color: #D05B5B; font-size: 11px; margin-left: 4px;">${stat.failure}失(${failureRate}%)</span>`
    }
    html += `</div>`
  })
  if (keyStats.length > 1) {
    html += `<div style="border-top: 1px solid rgba(128,128,128,0.2); margin-top: 8px; padding-top: 6px; font-weight: 600; display: flex; justify-content: space-between;">`
    html += `<span>合计: ${grandTotal} 请求</span>`
    if (hasFailure) html += `<span style="color: #D05B5B;">${grandFailure} 失败 (${grandFailureRate}%)</span>`
    html += `</div>`
  }
  html += `</div>`
  return html
}

const buildDualTooltip = ({ seriesIndex, dataPointIndex, w }: any): string => {
  if (!historyData.value?.keys) return ''
  const timestamp = w.globals.seriesX[seriesIndex][dataPointIndex]
  const date = new Date(timestamp)
  const timeStr = date.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
  const mode = selectedView.value
  const interval = getAggregationInterval(selectedDuration.value)
  const alignedTimestamp = alignToBucket(timestamp, interval)

  const keyStats: { keyMask: string; color: string; model: string; val1: number; val2: number; cacheHitRate?: number }[] = []

  historyData.value.keys.forEach((keyData, keyIndex) => {
    const matchingPoints = keyData.dataPoints?.filter(p => {
      const dpTimestamp = new Date(p.timestamp).getTime()
      return alignToBucket(dpTimestamp, interval) === alignedTimestamp
    }) || []
    if (matchingPoints.length > 0) {
      const aggregated = matchingPoints.reduce((acc, dp) => {
        if (mode === 'tokens') { acc.val1 += dp.inputTokens; acc.val2 += dp.outputTokens }
        else { acc.val1 += dp.cacheReadTokens; acc.val2 += dp.cacheCreationTokens }
        if (dp.model) acc.models.push(dp.model)
        return acc
      }, { val1: 0, val2: 0, models: [] as string[] })
      const uniqueModels = Array.from(new Set(aggregated.models.flatMap(m => m.split(', '))).values()).filter(m => !!m)
      const modelStr = escapeHtml(uniqueModels.join(', '))
      let cacheHitRate: number | undefined
      if (mode === 'cache') {
        const totalRead = aggregated.val1
        const totalInput = matchingPoints.reduce((sum, p) => sum + p.inputTokens, 0)
        if (totalRead + totalInput > 0) cacheHitRate = (totalRead / (totalRead + totalInput)) * 100
      }
      if (aggregated.val1 > 0 || aggregated.val2 > 0) {
        keyStats.push({ keyMask: escapeHtml(keyData.keyMask), color: keyColors[keyIndex % keyColors.length], model: modelStr, val1: aggregated.val1, val2: aggregated.val2, cacheHitRate })
      }
    }
  })
  if (keyStats.length === 0) return ''

  const label1 = mode === 'tokens' ? 'Input' : 'Cache Read'
  const label2 = mode === 'tokens' ? 'Output' : 'Cache Write'

  let html = `<div style="padding: 10px 14px; font-size: 12px; min-width: 240px;">`
  html += `<div style="font-weight: 600; margin-bottom: 8px; border-bottom: 1px solid rgba(128,128,128,0.15); padding-bottom: 6px;">${timeStr}</div>`
  keyStats.forEach(stat => {
    html += `<div style="margin-bottom: 8px; padding-bottom: 6px; border-bottom: 1px dashed rgba(128,128,128,0.1);">`
    html += `<div style="display: flex; align-items: center; gap: 6px; font-weight: 500; margin-bottom: 3px;">`
    html += `<span style="width: 8px; height: 8px; border-radius: 50%; background: ${stat.color}; flex-shrink: 0;"></span>`
    html += `<span style="flex: 1;">${stat.keyMask}${stat.model ? ` <span style="font-size: 10px; color: #888; font-weight: normal;">(${stat.model})</span>` : ''}</span>`
    html += `</div>`
    html += `<div style="padding-left: 14px; display: flex; gap: 16px; font-size: 11px; color: #888;">`
    html += `<span>${label1}: <b style="color: rgb(var(--v-theme-on-surface));">${formatNumber(stat.val1)}</b></span>`
    html += `<span>${label2}: <b style="color: rgb(var(--v-theme-on-surface));">${formatNumber(stat.val2)}</b></span>`
    html += `</div>`
    if (stat.cacheHitRate !== undefined) {
      const hitColor = stat.cacheHitRate >= 50 ? '#2FA478' : stat.cacheHitRate >= 20 ? '#5C6BC8' : '#C1804A'
      html += `<div style="padding-left: 14px; font-size: 11px; color: ${hitColor}; margin-top: 2px;">缓存命中率: <b>${stat.cacheHitRate.toFixed(1)}%</b></div>`
    }
    html += `</div>`
  })
  html += `</div>`
  return html
}

const chartOptions = computed<ApexOptions>(() => {
  const mode = selectedView.value
  const tc = getThemeColors.value

  let yaxisConfig: any
  if (mode === 'tokens' || mode === 'cache') {
    const keyCount = historyData.value?.keys?.length || 1
    yaxisConfig = []
    for (let i = 0; i < keyCount; i++) {
      yaxisConfig.push({
        seriesName: historyData.value?.keys?.[i]?.keyMask ? `${historyData.value.keys[i].keyMask} ${mode === 'tokens' ? 'Input' : 'Cache Read'}` : undefined,
        show: i === 0,
        labels: { formatter: (val: number) => formatAxisValue(val, mode), style: { fontSize: '11px', colors: tc.text } },
        min: 0, axisBorder: { show: false }, axisTicks: { show: false }
      })
      yaxisConfig.push({
        seriesName: historyData.value?.keys?.[i]?.keyMask ? `${historyData.value.keys[i].keyMask} ${mode === 'tokens' ? 'Output' : 'Cache Write'}` : undefined,
        opposite: true, show: i === 0,
        labels: { formatter: (val: number) => formatAxisValue(val, mode), style: { fontSize: '11px', colors: tc.text } },
        min: 0, axisBorder: { show: false }, axisTicks: { show: false }
      })
    }
  } else {
    yaxisConfig = {
      labels: { formatter: (val: number) => formatAxisValue(val, mode), style: { fontSize: '11px', colors: tc.text } },
      min: 0, axisBorder: { show: false }, axisTicks: { show: false }
    }
  }

  return {
    chart: {
      toolbar: { show: false },
      zoom: { enabled: false },
      background: 'transparent',
      fontFamily: 'inherit',
      sparkline: { enabled: false },
      animations: {
        enabled: true, speed: 500,
        animateGradually: { enabled: true, delay: 100 },
        dynamicAnimation: { enabled: true, speed: 350 }
      },
      dropShadow: {
        enabled: true, top: 0, left: 0, blur: 3, opacity: 0.08
      }
    },
    theme: { mode: isDark.value ? 'dark' : 'light' },
    colors: getChartColors(),
    fill: {
      type: 'gradient',
      gradient: {
        shadeIntensity: 1, opacityFrom: 0.35, opacityTo: 0.04, stops: [0, 85, 100]
      }
    },
    dataLabels: { enabled: false },
    stroke: {
      curve: 'smooth', width: 2,
      dashArray: getDashArray(),
      lineCap: 'round'
    },
    grid: {
      borderColor: tc.grid, strokeDashArray: 4,
      padding: { left: 8, right: 8 },
      xaxis: { lines: { show: true } },
      yaxis: { lines: { show: true } }
    },
    xaxis: {
      type: 'datetime',
      labels: {
        datetimeUTC: false, format: 'HH:mm',
        style: { fontSize: '11px', colors: tc.text, fontFamily: 'inherit' }
      },
      axisBorder: { show: false }, axisTicks: { show: false },
      crosshairs: {
        show: true, width: 1, position: 'back',
        stroke: { color: tc.grid, width: 1, dashArray: 3 }
      }
    },
    yaxis: yaxisConfig,
    annotations: { xaxis: failureAnnotations.value },
    tooltip: {
      theme: isDark.value ? 'dark' : 'light',
      x: { format: 'MM-dd HH:mm' },
      y: { formatter: (val: number) => formatTooltipValue(val, mode) },
      custom: mode === 'traffic' ? buildTrafficTooltip : buildDualTooltip,
      style: { fontSize: '12px', fontFamily: 'inherit' }
    },
    legend: { show: false },
    markers: { size: 0, hover: { size: 4 } }
  }
})

const buildChartSeries = (data: ChannelKeyMetricsHistoryResponse | null) => {
  if (!data?.keys) return []
  const mode = selectedView.value
  const result: { name: string; data: { x: number; y: number }[] }[] = []
  data.keys.forEach((keyData, keyIndex) => {
    if (mode === 'traffic') {
      result.push({
        name: keyData.keyMask,
        data: keyData.dataPoints.map(dp => ({ x: new Date(dp.timestamp).getTime(), y: dp.requestCount }))
      })
    } else {
      const inLabel = mode === 'tokens' ? 'Input' : 'Cache Read'
      const outLabel = mode === 'tokens' ? 'Output' : 'Cache Write'
      result.push({
        name: `${keyData.keyMask} ${inLabel}`,
        data: keyData.dataPoints.map(dp => ({ x: new Date(dp.timestamp).getTime(), y: mode === 'tokens' ? dp.inputTokens : dp.cacheReadTokens }))
      })
      result.push({
        name: `${keyData.keyMask} ${outLabel}`,
        data: keyData.dataPoints.map(dp => ({ x: new Date(dp.timestamp).getTime(), y: mode === 'tokens' ? dp.outputTokens : dp.cacheCreationTokens }))
      })
    }
  })
  return result
}

const chartSeries = computed(() => buildChartSeries(historyData.value))

const refreshData = async (isAutoRefresh = false) => {
  const requestId = ++refreshRequestId
  isRefreshing.value = true
  if (!isAutoRefresh) isLoading.value = true
  errorMessage.value = ''
  try {
    const newData = await channelApiByType(props.channelType).getChannelKeyMetricsHistory(props.channelId, selectedDuration.value)
    if (requestId !== refreshRequestId) return
    const canUpdateInPlace = isAutoRefresh && chartRef.value &&
      historyData.value?.keys?.length === newData.keys?.length &&
      historyData.value?.keys?.every((k, i) => k.keyMask === newData.keys[i].keyMask)
    if (canUpdateInPlace) {
      historyData.value = newData
      chartRef.value?.updateSeries(buildChartSeries(newData), false)
    } else {
      historyData.value = newData
    }
  } catch (error) {
    if (requestId !== refreshRequestId) return
    console.error('Failed to fetch key metrics history:', error)
    errorMessage.value = error instanceof Error ? error.message : '获取 Key 历史数据失败'
    showError.value = true
    historyData.value = null
  } finally {
    if (requestId === refreshRequestId) {
      isRefreshing.value = false
      if (!isAutoRefresh) isLoading.value = false
    }
  }
}

watch(selectedDuration, () => {
  savePreference(props.channelType, 'duration', selectedDuration.value)
  refreshData()
}, { flush: 'sync' })
watch(selectedView, () => {
  savePreference(props.channelType, 'viewMode', selectedView.value)
}, { flush: 'sync' })
watch(() => props.channelType, (newChannelType) => {
  const prefs = loadSavedPreferences(newChannelType)
  const oldDuration = selectedDuration.value
  selectedView.value = prefs.view
  selectedDuration.value = prefs.duration
  historyData.value = null
  if (oldDuration === prefs.duration) refreshData()
})

onMounted(() => { refreshData(); startAutoRefresh() })
onUnmounted(() => { stopAutoRefresh() })
defineExpose({ refreshData })
</script>

<style scoped>
.key-trend-chart-container {
  padding: 16px 20px;
  background: rgb(var(--v-theme-surface));
  border-top: 1px solid rgba(var(--v-theme-outline), 0.15);
}

.chart-header {
  flex-wrap: wrap;
  gap: 8px;
}

.chart-area {
  margin-top: 4px;
}
</style>
