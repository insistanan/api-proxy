// 五协议渠道操作契约；类型与 HTTP 实现分离，避免调用方依赖完整 API 门面。
import type {
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

/**
 * 渠道 API 类型（对应后端 scheduler.ChannelKind 与路由前缀）
 */
export type ApiTab = 'messages' | 'responses' | 'gemini' | 'chat' | 'images'

/**
 * 某一种渠道类型的统一 API 方法集。
 * 收敛前：getChannels / getResponsesChannels / getChatChannels / getGeminiChannels / getImagesChannels
 * 五组命名方法各自实现一遍 CRUD，仅 URL 前缀不同。工厂按 ApiTab 查表，一份实现覆盖五种渠道。
 */
export interface ChannelApi {
  getChannels(): Promise<ChannelsResponse>
  addChannel(channel: Omit<Channel, 'id' | 'index' | 'latency' | 'status'>): Promise<CreatedChannelResponse>
  updateChannel(id: number, channel: Partial<Channel>): Promise<void>
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
