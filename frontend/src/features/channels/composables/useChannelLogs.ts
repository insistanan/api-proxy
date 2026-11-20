// 渠道日志查看：维护一次弹窗查询与趋势派生，渠道配置仍由 store 管理。
import { computed, ref } from 'vue'
import type { ApexOptions } from 'apexcharts'
import { api, type ApiTab, type Channel, type ChannelLogEntry } from '@/api'
import { useChartColors } from '@/shared/theme'
import { formatShortDateTime } from '@/shared/utils/datetime'

export function useChannelLogs(props: { channelType: ApiTab }) {
  const showLogsDialog = ref(false)
  const logsChannel = ref<Channel | null>(null)
  const channelLogs = ref<ChannelLogEntry[]>([])
  const isLoadingLogs = ref(false)
  const logsError = ref('')

  const chartColors = useChartColors()
  const trendModes = [
    { value: 'traffic', label: '请求' },
    { value: 'tokens', label: 'Token' },
    { value: 'cache', label: '缓存' },
    { value: 'duration', label: '耗时' }
  ] as const
  const trendMode = ref<(typeof trendModes)[number]['value']>('traffic')

  const logSummary = computed(() => {
    const records = channelLogs.value
    const durations = records.map(log => log.durationMs).filter(ms => ms > 0).sort((a, b) => a - b)
    return {
      success: records.filter(log => log.success).length,
      unfinished: records.filter(log => !log.success).length,
      retried: records.filter(log => log.retried).length,
      p95: durations.length ? durations[Math.ceil(durations.length * 0.95) - 1] : undefined
    }
  })

  type LogTrendBucket = {
    x: number
    success: number
    unfinished: number
    inputTokens: number
    outputTokens: number
    cacheCreationTokens: number
    cacheReadTokens: number
    durationMs: number
    durationCount: number
  }

  const logTrendBuckets = computed(() => {
    const buckets = new Map<number, LogTrendBucket>()
    for (const log of channelLogs.value) {
      const timestamp = new Date(log.timestamp).getTime()
      if (!Number.isFinite(timestamp)) continue
      const x = Math.floor(timestamp / 60000) * 60000
      const bucket = buckets.get(x) ?? {
        x, success: 0, unfinished: 0, inputTokens: 0, outputTokens: 0,
        cacheCreationTokens: 0, cacheReadTokens: 0, durationMs: 0, durationCount: 0
      }
      if (log.success) bucket.success++
      else bucket.unfinished++
      bucket.inputTokens += log.inputTokens ?? 0
      bucket.outputTokens += log.outputTokens ?? 0
      bucket.cacheCreationTokens += log.cacheCreationTokens ?? 0
      bucket.cacheReadTokens += log.cacheReadTokens ?? 0
      if (log.durationMs > 0) {
        bucket.durationMs += log.durationMs
        bucket.durationCount++
      }
      buckets.set(x, bucket)
    }
    return [...buckets.values()].sort((a, b) => a.x - b.x)
  })

  const logTrendSeries = computed(() => {
    const points = logTrendBuckets.value
    const series = (name: string, value: (point: LogTrendBucket) => number | null) => ({
      name, data: points.map(point => ({ x: point.x, y: value(point) }))
    })
    switch (trendMode.value) {
      case 'tokens':
        return [series('输入', point => point.inputTokens), series('输出', point => point.outputTokens)]
      case 'cache':
        return [series('读取', point => point.cacheReadTokens), series('写入', point => point.cacheCreationTokens)]
      case 'duration':
        return [series('平均耗时', point => point.durationCount ? point.durationMs / point.durationCount : null)]
      default:
        return [series('成功', point => point.success), series('未成功', point => point.unfinished)]
    }
  })

  const logTrendChartOptions = computed<ApexOptions>(() => {
    const mode = chartColors.value.isDark ? 'dark' : 'light'
    const colors = chartColors.value
    const axisLabel = trendMode.value === 'duration' ? (value: number) => value === 0 ? '0' : formatDurationMs(value) : formatCompactNumber
    const valueLabel = trendMode.value === 'duration' ? formatDurationMs : formatNumber
    const points = logTrendBuckets.value
    const first = points[0]?.x
    const last = points[points.length - 1]?.x
    const padding = points.length === 1 ? 60000 : 30000
    const longRange = first !== undefined && last !== undefined && last - first > 24 * 3600000
    const seriesColors = {
      traffic: [chartColors.value.success, chartColors.value.failure],
      tokens: [chartColors.value.violet, chartColors.value.copper],
      cache: [chartColors.value.teal, chartColors.value.ochre],
      duration: [chartColors.value.indigo]
    }
    return {
      theme: { mode },
      chart: { background: 'transparent', foreColor: colors.text, toolbar: { show: false }, animations: { enabled: false }, fontFamily: 'inherit' },
      colors: seriesColors[trendMode.value],
      dataLabels: { enabled: false },
      plotOptions: { bar: { borderRadius: 2, columnWidth: points.length === 1 ? '20%' : '50%' } },
      fill: { opacity: 0.75 },
      stroke: { show: false },
      grid: { borderColor: colors.grid, strokeDashArray: 3 },
      xaxis: {
        type: 'datetime', min: first === undefined ? undefined : first - padding,
        max: last === undefined ? undefined : last + padding,
        labels: { datetimeUTC: false, format: longRange ? 'MM/dd HH:mm' : 'HH:mm', hideOverlappingLabels: true },
        axisBorder: { show: false }, axisTicks: { show: false }
      },
      yaxis: { min: 0, labels: { formatter: axisLabel } },
      tooltip: { theme: mode, shared: true, intersect: false, x: { format: 'MM/dd HH:mm' }, y: { formatter: valueLabel } },
      legend: { position: 'top', horizontalAlign: 'right', showForSingleSeries: true, labels: { colors: colors.text }, markers: { strokeWidth: 0, size: 5, shape: 'circle' } },
      noData: { text: '暂无趋势数据' }
    }
  })

  let requestVersion = 0
  const openLogsDialog = async (channel: Channel) => {
    const version = ++requestVersion
    logsChannel.value = channel
    trendMode.value = 'traffic'
    showLogsDialog.value = true
    logsError.value = ''
    channelLogs.value = []
    isLoadingLogs.value = true
    try {
      const response = await api.getChannelLogs(channel.kind ?? props.channelType, channel.index)
      if (version === requestVersion && showLogsDialog.value) channelLogs.value = response.logs ?? []
    } catch (error) {
      if (version !== requestVersion || !showLogsDialog.value) return
      console.error('加载渠道日志失败', error)
      logsError.value = error instanceof Error ? error.message : '加载请求日志失败'
    } finally {
      if (version === requestVersion) isLoadingLogs.value = false
    }
  }

  const closeLogsDialog = () => {
    requestVersion++
    isLoadingLogs.value = false
    showLogsDialog.value = false
    logsChannel.value = null
    channelLogs.value = []
    logsError.value = ''
  }

  const formatLogTime = (value: string): string => {
    if (!value) return '--'
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return value
    return formatShortDateTime(date)
  }

  const getLogStatusColor = (status: string): string => {
    if (status === 'completed') return 'success'
    if (status === 'cancelled') return 'grey'
    return 'error'
  }

  const formatLogStatus = (status: string, statusCode?: number): string => {
    const labels: Record<string, string> = {
      completed: '成功',
      failed: '失败',
      cancelled: '取消',
      blocked: '拦截',
      interrupted: '中止'
    }
    const label = labels[status] || status || '--'
    return statusCode ? `${label} ${statusCode}` : label
  }

  const formatLogBaseUrl = (value?: string): string => {
    const raw = String(value || '').trim()
    if (!raw) return '--'
    try {
      const url = new URL(raw)
      const path = url.pathname && url.pathname !== '/' ? url.pathname : ''
      return `${url.host}${path}`
    } catch {
      return raw
    }
  }

  const formatDurationMs = (value?: number): string => {
    const numeric = Number(value ?? 0)
    if (!Number.isFinite(numeric) || numeric <= 0) return '—'
    if (numeric >= 1000) return `${(numeric / 1000).toFixed(numeric >= 10000 ? 0 : 1)}s`
    return `${Math.round(numeric)}ms`
  }

  const formatLogError = (log: ChannelLogEntry): string => {
    return [log.errorType, log.errorMessage].filter(Boolean).join('\n') || '--'
  }

  const formatNumber = (value?: number): string => {
    const numeric = Number(value ?? 0)
    if (!Number.isFinite(numeric)) return '0'
    return new Intl.NumberFormat().format(Math.round(numeric))
  }

  const formatCompactNumber = (value?: number): string => {
    const numeric = Number(value ?? 0)
    if (!Number.isFinite(numeric)) return '0'
    return new Intl.NumberFormat(undefined, {
      notation: 'compact',
      maximumFractionDigits: 1
    }).format(numeric)
  }

  const formatLogTokenPair = (left?: number, right?: number): string => {
    return `${formatNumber(left)} / ${formatNumber(right)}`
  }

  return {
    showLogsDialog, logsChannel, channelLogs, isLoadingLogs, logsError,
    trendMode, trendModes, logSummary, logTrendSeries, logTrendChartOptions, openLogsDialog, closeLogsDialog,
    formatLogTime, getLogStatusColor, formatLogStatus, formatLogBaseUrl,
    formatDurationMs, formatLogError, formatNumber, formatLogTokenPair
  }
}
