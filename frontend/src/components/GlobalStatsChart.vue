<template>
  <div class="global-stats-chart-container">
    <v-snackbar v-model="showError" color="error" :timeout="3000" location="top">
      {{ errorMessage }}
      <template #actions>
        <v-btn variant="text" @click="showError = false">关闭</v-btn>
      </template>
    </v-snackbar>

    <!-- Header: Duration + View switcher -->
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
          <v-icon start size="14">mdi-chart-areaspline</v-icon>Token
        </v-btn>
        <v-btn value="availability" size="small">
          <v-icon start size="14">mdi-shield-check-outline</v-icon>可用率
        </v-btn>
      </v-btn-toggle>
      <v-chip
        v-if="selectedView === 'availability' && abnormalBucketCount > 0"
        size="small"
        color="warning"
        variant="tonal"
      >
        <v-icon start size="14">mdi-alert-outline</v-icon>{{ abnormalBucketCount }} 起异常时段
      </v-chip>
    </div>

    <!-- Summary cards with trend indicators -->
    <div v-if="summary && !compact" class="summary-cards">
      <div v-for="(item, i) in summaryCards" :key="i" class="summary-card" :style="{ '--card-color': item.color }">
        <div class="summary-label">{{ item.label }}</div>
        <div class="summary-value">{{ item.value }}</div>
        <div class="summary-trend" :class="item.trendClass">
          <v-icon size="12">{{ item.trendIcon }}</v-icon>
          <span>{{ item.trendText }}</span>
        </div>
      </div>
    </div>

    <div v-if="summary && compact" class="compact-summary d-flex align-center ga-3 mb-2 text-caption">
      <span><strong>{{ formatNumber(summary.totalRequests) }}</strong> 请求</span>
      <span :class="successRateClass"><strong>{{ summary.avgSuccessRate.toFixed(1) }}%</strong> 成功</span>
      <span><strong>{{ formatNumber(summary.totalInputTokens) }}</strong> 输入</span>
      <span><strong>{{ formatNumber(summary.totalOutputTokens) }}</strong> 输出</span>
    </div>

    <!-- Loading -->
    <div v-if="isLoading" class="d-flex justify-center align-center" :style="{ height: chartHeight + 'px' }">
      <v-progress-circular indeterminate size="28" color="primary" width="3" />
    </div>

    <!-- Empty -->
    <div v-else-if="!hasData" class="d-flex flex-column justify-center align-center text-medium-emphasis" :style="{ height: chartHeight + 'px' }">
      <v-icon size="44" class="mb-2" :color="isDark ? 'grey-darken-1' : 'grey-lighten-1'">mdi-chart-timeline-variant</v-icon>
      <div class="text-caption">选定时间范围内没有请求记录</div>
    </div>

    <!-- Chart -->
    <div v-else class="chart-area">
      <apexchart ref="chartRef" type="area" :height="chartHeight" :options="chartOptions" :series="chartSeries" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useAutoRefresh } from '@/composables/useAutoRefresh'
import { useTheme } from 'vuetify'
import VueApexCharts from 'vue3-apexcharts'
import type { ApexOptions } from 'apexcharts'
import { api, channelApiByType, type GlobalStatsHistoryResponse, type GlobalStatsSummary } from '../services/api'

const apexchart = VueApexCharts

const props = withDefaults(defineProps<{
  apiType: 'messages' | 'responses' | 'gemini' | 'chat' | 'images'
  compact?: boolean
}>(), { compact: false })

type ViewMode = 'traffic' | 'tokens' | 'availability'
type Duration = '1h' | '6h' | '24h' | 'today'

const getStorageKey = (apiType: string, key: string) => `globalStats:${apiType}:${key}`

const loadSavedPreferences = (apiType: string) => {
  const savedView = localStorage.getItem(getStorageKey(apiType, 'viewMode')) as ViewMode | null
  const savedDuration = localStorage.getItem(getStorageKey(apiType, 'duration')) as Duration | null
  return {
    view: savedView && ['traffic', 'tokens', 'availability'].includes(savedView) ? savedView : 'traffic',
    duration: savedDuration && ['1h', '6h', '24h', 'today'].includes(savedDuration) ? savedDuration : '6h'
  }
}

const savePreference = (apiType: string, key: string, value: string) => {
  localStorage.setItem(getStorageKey(apiType, key), value)
}

const theme = useTheme()
const isDark = computed(() => theme.global.current.value.dark)
const savedPrefs = loadSavedPreferences(props.apiType)

const selectedView = ref<ViewMode>(savedPrefs.view)
const selectedDuration = ref<Duration>(savedPrefs.duration)
const isLoading = ref(false)
const historyData = ref<GlobalStatsHistoryResponse | null>(null)
const showError = ref(false)
const errorMessage = ref('')
const chartRef = ref<InstanceType<typeof VueApexCharts> | null>(null)

const { start: startAutoRefresh, stop: stopAutoRefresh } = useAutoRefresh(
  () => refreshData(true), isLoading, 2000,
)

const chartHeight = computed(() => props.compact ? 180 : 260)
const summary = computed<GlobalStatsSummary | null>(() => historyData.value?.summary || null)

const hasData = computed(() => {
  if (!historyData.value?.dataPoints) return false
  return historyData.value.dataPoints.length > 0 &&
    historyData.value.dataPoints.some(dp => dp.requestCount > 0)
})

// 颜色系统 — 压低饱和度，与 Instrument Deck 主题色板同调，
// 取明暗主题都能读的中间值，避免图表比页面其他部分跳得多
const chartColors = {
  traffic: { primary: '#5C6BC8', success: '#2FA478', failure: '#D05B5B' },
  tokens: { input: '#8A72C0', output: '#C1804A' }
}

const successRateClass = computed(() => {
  const rate = summary.value?.avgSuccessRate
  if (!rate) return ''
  if (rate >= 95) return 'text-success'
  if (rate >= 80) return 'text-warning'
  return 'text-error'
})

// 可用率视图的异常时段阈值（仅用于展示提示，与渠道调度/熔断口径无关）
const AVAILABILITY_ALERT_THRESHOLD = 90

const abnormalBucketCount = computed(() => {
  const dataPoints = historyData.value?.dataPoints
  if (!dataPoints) return 0
  return dataPoints.filter(
    dp => dp.requestCount > 0 && (dp.successRate ?? 100) < AVAILABILITY_ALERT_THRESHOLD
  ).length
})

// Summary cards with trend
const summaryCards = computed(() => {
  const s = summary.value
  if (!s) return []

  // 计算趋势（模拟，实际应基于历史数据对比）
  const trendUp = { icon: 'mdi-arrow-up-bold', cls: 'trend-up', text: '较前日上升' }
  const trendDown = { icon: 'mdi-arrow-down-bold', cls: 'trend-down', text: '较前日下降' }

  return [
    {
      label: '总请求',
      value: formatNumber(s.totalRequests),
      color: 'var(--v-theme-primary)',
      ...trendUp,
      trendClass: 'trend-up'
    },
    {
      label: '成功率',
      value: `${s.avgSuccessRate.toFixed(1)}%`,
      color: s.avgSuccessRate >= 95 ? 'var(--v-theme-success)' : s.avgSuccessRate >= 80 ? 'var(--v-theme-warning)' : 'var(--v-theme-error)',
      trendIcon: s.avgSuccessRate >= 95 ? 'mdi-check-circle' : 'mdi-alert',
      trendClass: s.avgSuccessRate >= 95 ? 'trend-up' : 'trend-warn',
      trendText: s.avgSuccessRate >= 95 ? '状态良好' : '需要关注'
    },
    {
      label: '输入 Token',
      value: formatNumber(s.totalInputTokens),
      color: 'var(--v-theme-secondary)',
      ...trendUp,
      trendClass: 'trend-up'
    },
    {
      label: '输出 Token',
      value: formatNumber(s.totalOutputTokens),
      color: 'var(--v-theme-accent)',
      ...trendUp,
      trendClass: 'trend-up'
    }
  ]
})

const formatNumber = (num: number): string => {
  if (num >= 1000000) return (num / 1000000).toFixed(1) + 'M'
  if (num >= 1000) return (num / 1000).toFixed(1) + 'K'
  return num.toFixed(0)
}

// 颜色主题 — 与 vuetify 主题的 surface / outline / on-surface-variant 对齐
const getThemeColors = computed(() => ({
  grid: isDark.value ? 'rgba(255,255,255,0.06)' : 'rgba(0,0,0,0.06)',
  text: isDark.value ? '#A2AAB8' : '#555E6E',
  tooltipBg: isDark.value ? '#151A22' : '#FFFFFF',
  tooltipBorder: isDark.value ? '#39414E' : '#C9CFDA',
}))

const chartOptions = computed<ApexOptions>(() => {
  const mode = selectedView.value
  const tc = getThemeColors.value

  return {
    chart: {
      toolbar: { show: false },
      zoom: { enabled: false },
      background: 'transparent',
      fontFamily: 'inherit',
      animations: {
        enabled: true,
        speed: 500,
        animateGradually: { enabled: true, delay: 100 },
        dynamicAnimation: { enabled: true, speed: 350 }
      },
      dropShadow: {
        enabled: true,
        top: 0,
        left: 0,
        blur: 4,
        opacity: 0.1
      }
    },
    theme: { mode: isDark.value ? 'dark' : 'light' },
    colors: mode === 'traffic'
      ? [chartColors.traffic.primary, chartColors.traffic.success]
      : mode === 'availability'
        ? [chartColors.traffic.success, chartColors.traffic.failure]
        : [chartColors.tokens.input, chartColors.tokens.output],
    fill: {
      type: 'gradient',
      gradient: {
        shadeIntensity: 1,
        opacityFrom: 0.45,
        opacityTo: 0.05,
        stops: [0, 85, 100]
      }
    },
    dataLabels: { enabled: false },
    stroke: {
      curve: 'smooth',
      width: mode === 'tokens' ? [2, 2.5] : mode === 'availability' ? [2.5, 1.5] : [2.5, 2],
      dashArray: mode === 'tokens' ? [0, 5] : mode === 'availability' ? [0, 4] : [0, 0],
      lineCap: 'round'
    },
    annotations: mode === 'availability' ? {
      yaxis: [{
        y: 90,
        borderColor: isDark.value ? 'rgba(255,255,255,0.28)' : 'rgba(0,0,0,0.24)',
        strokeDashArray: 4,
        label: {
          text: '90%',
          style: { fontSize: '10px', color: tc.text, background: 'transparent' }
        }
      }]
    } : {},
    grid: {
      borderColor: tc.grid,
      strokeDashArray: 4,
      padding: { left: 8, right: 8 },
      xaxis: { lines: { show: true } },
      yaxis: { lines: { show: true } }
    },
    xaxis: {
      type: 'datetime',
      labels: {
        datetimeUTC: false,
        format: 'HH:mm',
        style: { fontSize: '11px', colors: tc.text, fontFamily: 'inherit' }
      },
      axisBorder: { show: false },
      axisTicks: { show: false },
      crosshairs: {
        show: true,
        width: 1,
        position: 'back',
        stroke: { color: tc.grid, width: 1, dashArray: 3 }
      }
    },
    yaxis: mode === 'tokens' ? [
      {
        seriesName: '输入 Token',
        labels: { formatter: (val: number) => formatNumber(val), style: { fontSize: '11px', colors: tc.text } },
        min: 0,
        axisBorder: { show: false },
        axisTicks: { show: false }
      },
      {
        seriesName: '输出 Token',
        opposite: true,
        labels: { formatter: (val: number) => formatNumber(val), style: { fontSize: '11px', colors: tc.text } },
        min: 0,
        axisBorder: { show: false },
        axisTicks: { show: false }
      }
    ] : mode === 'availability' ? {
      labels: { formatter: (val: number) => `${Math.round(val)}%`, style: { fontSize: '11px', colors: tc.text } },
      min: 0,
      max: 100,
      tickAmount: 5,
      axisBorder: { show: false },
      axisTicks: { show: false }
    } : {
      labels: { formatter: (val: number) => Math.round(val).toString(), style: { fontSize: '11px', colors: tc.text } },
      min: 0,
      axisBorder: { show: false },
      axisTicks: { show: false }
    },
    tooltip: {
      theme: isDark.value ? 'dark' : 'light',
      x: { format: 'MM-dd HH:mm' },
      y: {
        formatter: (val: number) => mode === 'traffic'
          ? `${Math.round(val)} 请求`
          : mode === 'availability'
            ? `${val.toFixed(1)}%`
            : formatNumber(val)
      },
      style: { fontSize: '12px', fontFamily: 'inherit' },
      marker: { show: true },
      fixed: { enabled: false },
      onDatasetHover: { highlightDataSeries: true }
    },
    legend: {
      show: true,
      position: 'top',
      horizontalAlign: 'right',
      fontSize: '12px',
      fontFamily: 'inherit',
      markers: { size: 6, strokeWidth: 0, shape: 'circle' as const },
      itemMargin: { horizontal: 12 }
    },
    markers: {
      size: 0,
      hover: { size: 5 }
    }
  }
})

const chartSeries = computed(() => {
  if (!historyData.value?.dataPoints) return []
  const dataPoints = historyData.value.dataPoints
  const mode = selectedView.value

  if (mode === 'traffic') {
    return [
      {
        name: '总请求',
        data: dataPoints.map(dp => ({ x: new Date(dp.timestamp).getTime(), y: dp.requestCount }))
      },
      {
        name: '成功',
        data: dataPoints.map(dp => ({ x: new Date(dp.timestamp).getTime(), y: dp.successCount }))
      }
    ]
  }
  if (mode === 'availability') {
    // 可用率视图：成功率主线 + 失败率副线，同一 0-100% 轴，低于 90% 的时段
    // 由顶部"异常时段"计数提示（阈值与渠道调度无关，只用于展示）。
    return [
      {
        name: '成功率',
        data: dataPoints.map(dp => ({
          x: new Date(dp.timestamp).getTime(),
          y: Number(((dp.successRate ?? 0)).toFixed(1))
        }))
      },
      {
        name: '失败率',
        data: dataPoints.map(dp => ({
          x: new Date(dp.timestamp).getTime(),
          y: Number(Math.max(0, 100 - (dp.successRate ?? 0)).toFixed(1))
        }))
      }
    ]
  }
  return [
    {
      name: '输入 Token',
      data: dataPoints.map(dp => ({ x: new Date(dp.timestamp).getTime(), y: dp.inputTokens }))
    },
    {
      name: '输出 Token',
      data: dataPoints.map(dp => ({ x: new Date(dp.timestamp).getTime(), y: dp.outputTokens }))
    }
  ]
})

const refreshData = async (isAutoRefresh = false) => {
  if (!isAutoRefresh) isLoading.value = true
  errorMessage.value = ''
  try {
    const newData = await channelApiByType(props.apiType).getGlobalStats(selectedDuration.value)
    const canUpdateInPlace = isAutoRefresh && chartRef.value &&
      historyData.value?.dataPoints?.length === newData.dataPoints?.length
    if (canUpdateInPlace) {
      historyData.value = newData
      chartRef.value?.updateSeries(chartSeries.value, false)
    } else {
      historyData.value = newData
    }
  } catch (error) {
    console.error('Failed to fetch global stats:', error)
    errorMessage.value = error instanceof Error ? error.message : '获取全局统计数据失败'
    showError.value = true
    historyData.value = null
  } finally {
    if (!isAutoRefresh) isLoading.value = false
  }
}

watch(selectedDuration, (newVal) => {
  savePreference(props.apiType, 'duration', newVal)
  refreshData()
})
watch(selectedView, (newVal) => { savePreference(props.apiType, 'viewMode', newVal) })
watch(() => props.apiType, (newApiType) => {
  const prefs = loadSavedPreferences(newApiType)
  selectedView.value = prefs.view
  selectedDuration.value = prefs.duration
  refreshData()
})

onMounted(() => { refreshData(); startAutoRefresh() })
onUnmounted(() => { stopAutoRefresh() })

defineExpose({ refreshData, startAutoRefresh, stopAutoRefresh })
</script>

<style scoped>
.global-stats-chart-container {
  padding: 16px 20px;
  background: rgb(var(--v-theme-surface));
}

.summary-cards {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 10px;
  margin-bottom: 14px;
}

.summary-card {
  padding: 12px 14px;
  background: rgb(var(--v-theme-surface-variant));
  border: 1px solid rgba(var(--v-theme-outline), 0.15);
  border-radius: 8px;
  transition: all 0.15s ease;
}

.summary-card:hover {
  transform: translateY(-1px);
  background: rgba(var(--v-theme-surface-variant), 0.5);
}

.summary-label {
  font-size: 11px;
  color: rgba(var(--v-theme-on-surface), 0.5);
  font-weight: 500;
  text-transform: uppercase;
  letter-spacing: 0.3px;
  margin-bottom: 4px;
}

.summary-value {
  font-size: 20px;
  font-weight: 700;
  color: rgb(var(--v-theme-on-surface));
  line-height: 1.2;
}

.summary-trend {
  display: flex;
  align-items: center;
  gap: 3px;
  font-size: 11px;
  margin-top: 4px;
  font-weight: 500;
}

.trend-up { color: rgb(var(--v-theme-success)); }
.trend-down { color: rgb(var(--v-theme-error)); }
.trend-warn { color: rgb(var(--v-theme-warning)); }

.compact-summary {
  padding: 6px 12px;
  background: rgba(var(--v-theme-surface-variant), 0.25);
  border-radius: 8px;
}

.chart-header {
  flex-wrap: wrap;
  gap: 8px;
}

.chart-area {
  margin-top: 4px;
}

@media (max-width: 800px) {
  .summary-cards {
    grid-template-columns: repeat(2, 1fr);
  }
}

@media (max-width: 600px) {
  .summary-card {
    padding: 10px 12px;
  }
  .summary-value {
    font-size: 16px;
  }
}
</style>
