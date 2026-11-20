// 指标与统计类型：渠道/Key 历史、全局统计与实时活跃度。

import type { LogAPIType } from './logs'

// 历史数据点（用于时间序列图表）
export interface HistoryDataPoint {
  timestamp: string
  requestCount: number
  successCount: number
  failureCount: number
  successRate: number
}

// 渠道历史指标响应
export interface MetricsHistoryResponse {
  channelIndex: number
  channelName: string
  dataPoints: HistoryDataPoint[]
}

// Key 级别历史数据点（包含 Token 数据）
export interface KeyHistoryDataPoint {
  timestamp: string
  requestCount: number
  successCount: number
  failureCount: number
  successRate: number
  inputTokens: number
  outputTokens: number
  cacheCreationTokens: number
  cacheReadTokens: number
  model?: string
  cacheHitRate?: number
}

// 单个 Key 的历史数据
export interface KeyHistoryData {
  keyMask: string
  color: string
  dataPoints: KeyHistoryDataPoint[]
}

// 渠道 Key 级别历史指标响应
export interface ChannelKeyMetricsHistoryResponse {
  channelIndex: number
  channelName: string
  keys: KeyHistoryData[]
}

// ============== 全局统计类型 ==============

// 全局历史数据点（包含 Token 数据）
export interface GlobalHistoryDataPoint {
  timestamp: string
  requestCount: number
  successCount: number
  failureCount: number
  successRate: number
  inputTokens: number
  outputTokens: number
  cacheCreationTokens: number
  cacheReadTokens: number
}

// 全局统计汇总
export interface GlobalStatsSummary {
  totalRequests: number
  totalSuccess: number
  totalFailure: number
  totalInputTokens: number
  totalOutputTokens: number
  totalCacheCreationTokens: number
  totalCacheReadTokens: number
  avgSuccessRate: number
  duration: string
}

export type StatsDuration = '1h' | '6h' | '24h' | '7d' | '30d' | 'today' | 'custom'

export type RequestStatsDimensionSort = 'volume' | 'failures' | 'failureRate' | 'tokens' | 'duration'

export interface RequestStatsParams {
  duration?: Exclude<StatsDuration, 'custom'>
  from?: string
  to?: string
  interval?: string
  type?: LogAPIType | ''
  channel?: string
  model?: string
  status?: string
  errorType?: string
  upstream?: string
  keyMask?: string
  q?: string
  compare?: boolean
  dimensionSort?: RequestStatsDimensionSort
}

export interface RequestStatsSummary extends GlobalStatsSummary {
  totalCalls: number
  totalRetried: number
  retryRate: number
  cacheHitRate: number
  avgDurationMs: number
  p50DurationMs: number
  p95DurationMs: number
  maxDurationMs: number
  avgFirstTokenMs: number
  p50FirstTokenMs: number
  p95FirstTokenMs: number
  avgTps: number
  durationSamples: number
  firstTokenSamples: number
}

export interface RequestStatsPoint extends GlobalHistoryDataPoint {
  retriedCount: number
  avgDurationMs: number
  avgFirstTokenMs: number
}

export interface RequestStatsDimension {
  name: string
  requestCount: number
  successCount: number
  failureCount: number
  inputTokens: number
  outputTokens: number
  totalTokens: number
  avgDurationMs: number
  successRate: number
}

export interface RequestStatsResponse {
  from: string
  to: string
  interval: string
  retentionDays: number
  summary: RequestStatsSummary
  previousSummary?: RequestStatsSummary
  dataPoints: RequestStatsPoint[]
  channels: RequestStatsDimension[]
  models: RequestStatsDimension[]
  errors: RequestStatsDimension[]
  protocols: RequestStatsDimension[]
  statuses: RequestStatsDimension[]
  upstreams: RequestStatsDimension[]
  keys: RequestStatsDimension[]
  facets: { channels: string[]; models: string[] }
}

export interface RequestStatsDrill {
  from?: string
  to?: string
  type?: LogAPIType | ''
  channel?: string
  model?: string
  status?: string
  errorType?: string
  upstream?: string
  keyMask?: string
}

// 全局统计响应
export interface GlobalStatsHistoryResponse {
  dataPoints: GlobalHistoryDataPoint[]
  summary: GlobalStatsSummary
}

// ============== 渠道实时活跃度类型 ==============

// 活跃度分段数据（每 6 秒一段）
export interface ActivitySegment {
  requestCount: number
  successCount: number
  failureCount: number
  inputTokens: number
  outputTokens: number
}

// 渠道最近活跃度数据
export interface ChannelRecentActivity {
  channelIndex: number
  segments: ActivitySegment[] // 150 段，每段 6 秒，从旧到新（共 15 分钟）
  rpm: number // 15分钟平均 RPM
  tpm: number // 15分钟平均 TPM
}
