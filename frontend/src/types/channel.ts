// 渠道领域类型：渠道实体、指标、资源池与渠道接口响应。

import type { ChannelRecentActivity } from './metrics'

/** 渠道入口协议，与后端 scheduler.ChannelKind 及管理路由前缀对齐。 */
export type ApiTab = 'messages' | 'responses' | 'gemini' | 'chat' | 'images' | 'embeddings' | 'decisions'

export interface ChannelDeletionImpact {
  name: string
  channelCount: number
  dependents: { kind: ApiTab; index: number; name: string }[]
}

// 渠道状态枚举
export type ChannelStatus = 'active' | 'suspended' | 'disabled' | 'deprecated' | 'deleted'

export type ImagesOutputFormat = '' | 'png' | 'jpeg' | 'webp' | 'gif' | 'svg'

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
  /** 仪表盘快照中，该渠道正在处理的独立对话数。 */
  activeConversations?: number
  /** 已保存上下文且仍在等待或执行心跳的会话数。 */
  activeKeepaliveSessions?: number
  /** 按会话与稳定线程、分支、Agent 标识去重；无标识的会话至少计一个。 */
  activeBranches?: number
  /** 有请求缺少稳定分支标识的活跃会话数，此时分支统计可能不完整。 */
  unidentifiedBranchConversations?: number
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
  cacheKeepalive?: CacheKeepaliveConfig
  builtinProvider?: 'kilo'
  maxConcurrency?: number // 每个 URL、实际协议、Key 组合的配置并发，默认 5
  maxBranchConcurrency?: number // 渠道分支并发上限，默认 3，同分支重复请求不额外占用
  effectiveConcurrency?: { baseUrl: string; protocol: string; keyIndex: number; maxConcurrency: number }[]
  clientIdentityMode?: 'inherit' | 'api' | 'compatibility'
  id?: string
  // 共享图片理解池返回的渠道自身协议；普通渠道接口不返回此字段。
  kind?: ApiTab
  poolId?: string
  name: string
  serviceType: 'openai' | 'gemini' | 'claude' | 'responses' | 'chat'
  baseUrl: string
  baseUrls?: string[] // 多 BaseURL 支持（failover 模式）
  useFullUrl?: boolean // 使用完整请求地址，不自动追加版本号和接口路径
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
  responsesModel?: string // Images 转 Responses 时的主模型，与图片模型映射分开配置
  imagesExtraParams?: Record<string, string> // Images 自定义上游参数，值为发送前再解析的文本
  imagesForceNonStream?: boolean // Images 强制上游非流式，回程仍按客户端要求返回 JSON 或 completed SSE
  imagesOutputFormat?: ImagesOutputFormat // 空值跟随请求，显式格式在两种上游协议中都生效
  imagesFrameCount?: number // GIF 帧数，零表示不覆盖
  imagesFrameDurationMs?: number // GIF 每帧时长（毫秒），零表示不覆盖
  embeddingsDimensions?: number // Embeddings 输出维度：大于零时覆盖请求中的 dimensions，零表示不覆盖
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
  reasoningOutputMode?: 'preserve' | 'hide' // Messages→Chat：默认保留思考
  reasoningEffort?: 'inherit' | 'auto' | 'none' | 'low' | 'high' | 'xhigh' | 'max' // inherit 跟随请求；auto 不传思考控制
  parseEmbeddedReasoning?: boolean // Messages→Chat：解析正文中的思考标签
  requireReasoningContent?: boolean // 严格渠道要求原始历史推理
  visionLayerEnabled?: boolean // 是否为当前渠道指定优先图片理解渠道
  visionLayerChannelId?: string // 优先调用的稳定渠道标识；未启用时自动使用公共池
  visionLayerModel?: string // 可选：覆盖透传给图片理解渠道的模型名
  temporary?: boolean // 临时渠道：一天后自动移入弃用池
  temporaryUntil?: string // 临时渠道到期时间
  deprecatedAt?: string // 移入弃用池时间
  injectDummyThoughtSignature?: boolean // Gemini 特定：为 functionCall 注入 dummy thought_signature（兼容第三方 API）
  stripThoughtSignature?: boolean // Gemini 特定：移除 thought_signature 字段（兼容旧版 Gemini API）
}

export interface CacheKeepaliveConfig {
  enabled: boolean
  intervalSeconds: number
  maxAttempts: number
  maxIdleMinutes: number
  prompt: string
  minCacheHitPercent: number
  maxOutputTokens: number
}

export interface CacheKeepaliveStatus {
  id: string
  entryProtocol: string
  upstreamProtocol: string
  cacheMode?: 'prefix' | 'implicit' | 'explicit'
  snapshotAt?: number
  nextHeartbeatAt?: number
  lastAt?: number
  expiresAt?: number
  conversationId: string
  channelId: string
  model: string
  state: 'active' | 'waiting' | 'preparing' | 'ready' | 'sending' | 'stopped'
  reason?: string
  attempts: number
  inputTokens: number
  cacheReadTokens: number
  cacheCreationTokens: number
  outputTokens: number
  cacheHitPercent: number
  baselineCacheHitPercent: number
  nextAt?: number
  updatedAt: number
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

// 共享公用图片理解渠道：kind 表示渠道自身所属协议。
export interface VisionChannel extends Channel {
  kind: ApiTab
}

export interface VisionChannelsResponse {
  channels: VisionChannel[]
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
