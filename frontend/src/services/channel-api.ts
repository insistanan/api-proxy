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

/** 渠道 API 类型，对应后端 scheduler.ChannelKind 与管理路由前缀。 */
export type ApiTab = 'messages' | 'responses' | 'gemini' | 'chat' | 'images'

/**
 * 定义五组渠道共用的操作方法和返回类型。
 * channels.ts 中的 channelApi 工厂按入口协议生成对应实现。
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
