// 渠道领域类型：渠道实体、指标、资源池与渠道接口响应。

import type { ChannelRecentActivity } from './metrics'

// 渠道状态枚举
export type ChannelStatus = 'active' | 'suspended' | 'disabled' | 'deprecated' | 'deleted'

// 渠道指标
// 分时段统计
export interface TimeWindowStats {
  requestCount: number
  successCount: number
  failureCount: number
  successRate: number
  inputTokens?: number
  outputTokens?: number
  cacheCreationTokens?: number
  cacheReadTokens?: number
  cacheHitRate?: number
}

// 渠道级熔断器运行时状态（internal/circuit 三态；与配置状态 ChannelStatus 正交）
export type CircuitState = 'closed' | 'open' | 'half_open'

export interface ChannelMetrics {
  channelIndex: number
  requestCount: number
  successCount: number
  failureCount: number
  successRate: number // 0-100
  errorRate: number // 0-100
  consecutiveFailures: number
  latency: number // ms
  lastSuccessAt?: string
  lastFailureAt?: string
  // 渠道级三态熔断运行时状态（dashboard API 注入）
  circuitState?: CircuitState
  circuitConsecutiveFailures?: number
  circuitOpenedAt?: string
  // 分时段统计 (15m, 1h, 6h, 24h)
  timeWindows?: {
    '15m': TimeWindowStats
    '1h': TimeWindowStats
    '6h': TimeWindowStats
    '24h': TimeWindowStats
  }
}

export interface Channel {
  id?: string
  poolId?: string
  name: string
  serviceType: 'openai' | 'gemini' | 'claude' | 'responses' | 'chat'
  baseUrl: string
  baseUrls?: string[] // 多 BaseURL 支持（failover 模式）
  apiKeys: string[]
  description?: string
  website?: string
  insecureSkipVerify?: boolean
  proxyMode?: 'inherit' | 'direct' | 'custom'
  proxyUrl?: string
  modelMapping?: Record<string, string[]> // 模型重定向：源模型 -> 目标模型列表（支持多个备选）
  // 模型映射的目标渠道登记：源模型 -> 目标模型 -> 目标渠道 ID（缺省表示沿用本渠道）
  modelMappingChannels?: Record<string, Record<string, string>>
  defaultModel?: string
  latency?: number
  status?: ChannelStatus | 'healthy' | 'error' | 'unknown' | ''
  index: number
  pinned?: boolean
  // 多渠道调度相关字段
  priority?: number // 渠道优先级（数字越小优先级越高）
  metrics?: ChannelMetrics // 实时指标
  suspendReason?: string // 熔断原因
  promotionUntil?: string // 促销期截止时间（ISO 格式）
  promotionCount?: number // 促销期剩余请求次数
  latencyTestTime?: number // 延迟测试时间戳（用于 5 分钟后自动清除显示）
  lowQuality?: boolean // 低质量渠道标记：启用后强制本地估算 token，偏差>5%时使用本地值
  visionCapable?: boolean // 渠道是否原生支持图片理解
  excludeFromConversation?: boolean // 不参与常规对话调度，仅作为图片理解渠道使用
  disablePromptCacheKey?: boolean // 不向上游发送 prompt_cache_key
  visionLayerEnabled?: boolean // 是否为当前渠道指定优先图片理解渠道
  visionLayerChannelId?: string // 优先调用的稳定渠道标识；未启用时自动使用公共池
  visionLayerModel?: string // 可选：覆盖透传给图片理解渠道的模型名
  temporary?: boolean // 临时渠道：一天后自动移入弃用池
  temporaryUntil?: string // 临时渠道到期时间
  deprecatedAt?: string // 移入弃用池时间
  injectDummyThoughtSignature?: boolean // Gemini 特定：为 functionCall 注入 dummy thought_signature（兼容第三方 API）
  stripThoughtSignature?: boolean // Gemini 特定：移除 thought_signature 字段（兼容旧版 Gemini API）
}

export interface ChannelPool {
  id: string
  name: string
  modelMatcher: string
  priority: number
}

export interface CreatedChannelResponse {
  channel: {
    id: string
    index: number
  }
}

export interface ChannelsResponse {
  channels: Channel[]
  current: number
  loadBalance: string
}

// 模型映射"目标渠道"下拉候选项（同协议、启用、有密钥、未熔断 Open）
export interface MappingTargetChannel {
  id: string
  name: string
  index: number
  poolId: string
  serviceType: string
  status: string
  circuitState: string
}

export interface MappingTargetChannelsResponse {
  channels: MappingTargetChannel[]
}

// 渠道仪表盘响应（合并 channels + metrics + stats）
export interface ChannelDashboardResponse {
  channels: Channel[]
  loadBalance: string
  metrics: ChannelMetrics[]
  stats: {
    multiChannelMode: boolean
    activeChannelCount: number
    traceAffinityCount: number
    traceAffinityTTL: string
    failureThreshold: number
    windowSize: number
    circuitRecoveryTime: string
  }
  recentActivity?: ChannelRecentActivity[] // 最近 15 分钟分段活跃度
}

export interface PingResult {
  success: boolean
  latency: number
  status: string
  error?: string
}
