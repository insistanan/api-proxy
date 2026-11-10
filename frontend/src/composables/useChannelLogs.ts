// 渠道日志查看：维护一次弹窗查询与趋势派生，渠道配置仍由 store 管理。
import { computed, ref } from 'vue'
import type { ApexOptions } from 'apexcharts'
import { api, type ApiTab, type Channel, type ChannelLogEntry } from '@/services/api'

export function useChannelLogs(props: { channelType: ApiTab }) {
  const showLogsDialog = ref(false)
  const logsChannel = ref<Channel | null>(null)
  const channelLogs = ref<ChannelLogEntry[]>([])
  const isLoadingLogs = ref(false)
  const logsError = ref('')

  type LogTrendBucket = {
    x: number
    requests: number
    inputTokens: number
    outputTokens: number
    cacheCreationTokens: number
    cacheReadTokens: number
  }

  const logTrendBuckets = computed<LogTrendBucket[]>(() => {
    const buckets = new Map<number, LogTrendBucket>()
    for (const log of channelLogs.value) {
      const timestamp = new Date(log.timestamp).getTime()
      if (!Number.isFinite(timestamp)) continue
      const bucketTime = Math.floor(timestamp / 60000) * 60000
      const bucket = buckets.get(bucketTime) ?? {
        x: bucketTime,
        requests: 0,
        inputTokens: 0,
        outputTokens: 0,
        cacheCreationTokens: 0,
        cacheReadTokens: 0
      }

      bucket.requests += 1
      bucket.inputTokens += log.inputTokens ?? 0
      bucket.outputTokens += log.outputTokens ?? 0
      bucket.cacheCreationTokens += log.cacheCreationTokens ?? 0
      bucket.cacheReadTokens += log.cacheReadTokens ?? 0
      buckets.set(bucketTime, bucket)
    }

    return Array.from(buckets.values()).sort((a, b) => a.x - b.x)
  })

  const logTrendSeries = computed(() => {
    const points = logTrendBuckets.value
    return [
      { name: '输入Token', data: points.map(point => [point.x, point.inputTokens]) },
      { name: '输出Token', data: points.map(point => [point.x, point.outputTokens]) },
      { name: '缓存创建Token', data: points.map(point => [point.x, point.cacheCreationTokens]) },
      { name: '缓存读取Token', data: points.map(point => [point.x, point.cacheReadTokens]) },
      { name: '请求数', data: points.map(point => [point.x, point.requests]) }
    ]
  })

  const logTrendChartOptions = computed<ApexOptions>(() => ({
    chart: {
      toolbar: { show: false },
      zoom: { enabled: false },
      animations: { enabled: false },
      fontFamily: 'inherit'
    },
    colors: ['#5C6BC8', '#2FA478', '#C1804A', '#8A72C0', '#7B8494'],
    dataLabels: { enabled: false },
    stroke: {
      width: [2, 2, 2, 2, 2],
      curve: 'smooth'
    },
    markers: {
      size: 3,
      strokeWidth: 0
    },
    grid: {
      borderColor: 'rgba(128, 134, 148, 0.24)',
      strokeDashArray: 4
    },
    xaxis: {
      type: 'datetime',
      labels: {
        datetimeUTC: false
      }
    },
    yaxis: [
      {
        seriesName: ['输入Token', '输出Token', '缓存创建Token', '缓存读取Token'],
        title: { text: 'Tokens' },
        labels: { formatter: value => formatCompactNumber(value) }
      },
      {
        seriesName: '请求数',
        opposite: true,
        title: { text: '请求数' },
        labels: { formatter: value => formatCompactNumber(value) }
      }
    ],
    tooltip: {
      shared: true,
      x: { format: 'HH:mm:ss' },
      y: { formatter: value => formatNumber(value) }
    },
    legend: {
      position: 'bottom',
      fontSize: '12px'
    }
  }))

  const openLogsDialog = async (channel: Channel) => {
    logsChannel.value = channel
    showLogsDialog.value = true
    logsError.value = ''
    channelLogs.value = []
    isLoadingLogs.value = true
    try {
      const response = await api.getChannelLogs(props.channelType, channel.index)
      channelLogs.value = response.logs || []
    } catch (error) {
      console.error('Failed to load channel logs:', error)
      logsError.value = error instanceof Error ? error.message : '加载请求日志失败'
    } finally {
      isLoadingLogs.value = false
    }
  }

  const closeLogsDialog = () => {
    showLogsDialog.value = false
    logsChannel.value = null
    channelLogs.value = []
    logsError.value = ''
  }

  const formatLogTime = (value: string): string => {
    if (!value) return '--'
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return value
    return date.toLocaleString()
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
      cancelled: '取消'
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
    if (!Number.isFinite(numeric) || numeric <= 0) return '0ms'
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
    logTrendSeries, logTrendChartOptions, openLogsDialog, closeLogsDialog,
    formatLogTime, getLogStatusColor, formatLogStatus, formatLogBaseUrl,
    formatDurationMs, formatLogError, formatNumber, formatLogTokenPair
  }
}
