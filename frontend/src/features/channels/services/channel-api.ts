// 各协议渠道操作契约；类型与 HTTP 实现分离，避免调用方依赖完整 API 门面。
import type {
  ApiTab,
  Channel,
  ChannelDashboardResponse,
  ChannelKeyMetricsHistoryResponse,
  ChannelMetrics,
  ChannelStatus,
  ChannelsResponse,
  CreatedChannelResponse,
  GlobalStatsHistoryResponse,
  MetricsHistoryResponse,
  MappingTargetChannelsResponse,
  ModelsResponse,
  PingResult
} from '@/types'

/** 渠道 API 类型，与 @/types 的 ApiTab 保持一致。 */
export const API_TABS = ['messages', 'responses', 'gemini', 'chat', 'images', 'embeddings', 'decisions'] as const satisfies readonly ApiTab[]
export type { ApiTab } from '@/types'

/** 各渠道协议的展示名，供跨协议渠道列表标注来源。 */
export const API_TAB_LABELS: Record<ApiTab, string> = {
  messages: 'Messages', responses: 'Responses', gemini: 'Gemini', chat: 'Chat',
  images: 'Images', embeddings: 'Embeddings', decisions: 'Decisions'
}

/**
 * 对话类入口协议：具备会话语义，支持图片理解与对话缓存等特性。
 * Embeddings / Decisions 为纯透传，不属于此类。与 intent.ts 的 INTENT_PROTOCOLS 语义不同，
 * 不要用后者做 UI 能力判断。
 */
export const CONVERSATION_PROTOCOLS = ['messages', 'responses', 'gemini', 'chat'] as const
export type ConversationProtocol = (typeof CONVERSATION_PROTOCOLS)[number]
export const isConversationProtocol = (value: string): value is ConversationProtocol =>
  (CONVERSATION_PROTOCOLS as readonly string[]).includes(value)

/**
 * 定义各组渠道共用的操作方法和返回类型。
 * channels.ts 中的 channelApi 工厂按入口协议生成对应实现。
 */
export interface ChannelApi {
  getChannels(): Promise<ChannelsResponse>
  addChannel(channel: Omit<Channel, 'id' | 'index' | 'latency' | 'status'>): Promise<CreatedChannelResponse>
  updateChannel(id: number, channel: Partial<Channel>, expectedChannelId?: string): Promise<void>
  deleteChannel(id: number): Promise<void>
  addKey(channelId: number, apiKey: string): Promise<void>
  removeKey(channelId: number, apiKey: string): Promise<void>
  moveKeyToTop(channelId: number, apiKey: string): Promise<void>
  moveKeyToBottom(channelId: number, apiKey: string): Promise<void>
  reorder(order: number[]): Promise<void>
  setStatus(channelId: number, status: ChannelStatus): Promise<void>
  setPromotion(channelId: number, durationSeconds: number, count?: number): Promise<void>
  pingChannel(id: number): Promise<PingResult>
  pingAllChannels(): Promise<Array<{ id: number; name: string; latency: number; status: string }>>
  updateLoadBalance(strategy: string): Promise<void>
  resumeChannel(channelId: number): Promise<void>
  getChannelMetrics(): Promise<ChannelMetrics[]>
  getChannelMetricsHistory(duration?: '1h' | '6h' | '24h'): Promise<MetricsHistoryResponse[]>
  getChannelKeyMetricsHistory(
    channelId: number,
    duration?: '1h' | '6h' | '24h' | 'today'
  ): Promise<ChannelKeyMetricsHistoryResponse>
  getGlobalStats(duration?: '1h' | '6h' | '24h' | 'today'): Promise<GlobalStatsHistoryResponse>
  getChannelDashboard(): Promise<ChannelDashboardResponse>
  listMappingTargets(excludeId?: string): Promise<MappingTargetChannelsResponse>
  listMappingTargetModels(channelId: string): Promise<ModelsResponse>
}
