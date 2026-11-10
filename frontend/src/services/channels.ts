// 渠道与指标 API：复用统一 HTTP 基础层，保留端点与返回契约。
import { apiRequest } from './http'
import type {
  ChannelDashboardResponse,
  ChannelKeyMetricsHistoryResponse,
  ChannelLogsResponse,
  ChannelMetrics,
  ChannelPool,
  GlobalStatsHistoryResponse,
  MetricsHistoryResponse,
  ModelsResponse,
  UpstreamModelsRequest
} from '@/types'
import type { ApiTab, ChannelApi } from './channel-api'

function normalizePromotionValue(value: unknown): number {
  const parsed = typeof value === 'number' ? value : Number(String(value ?? '').trim())

  if (!Number.isSafeInteger(parsed) || parsed < 0) {
    throw new Error('促销限制必须是有效的非负整数')
  }

  return parsed
}

export const channelsApi = {
  /**
   * 根据入口协议构造管理 API 的路径前缀。
   * 五组渠道共用这套操作，避免分别维护重复的 CRUD 实现。
   */
  channelApi(type: ApiTab): ChannelApi {
    const p = `/${type}`
    return {
      getChannels: () => apiRequest(`${p}/channels`),
      addChannel: channel => apiRequest(`${p}/channels`, { method: 'POST', body: JSON.stringify(channel) }),
      updateChannel: (id, channel) =>
        apiRequest(`${p}/channels/${id}`, { method: 'PUT', body: JSON.stringify(channel) }),
      deleteChannel: id => apiRequest(`${p}/channels/${id}`, { method: 'DELETE' }),
      addKey: (channelId, apiKey) =>
        apiRequest(`${p}/channels/${channelId}/keys`, { method: 'POST', body: JSON.stringify({ apiKey }) }),
      removeKey: (channelId, apiKey) =>
        apiRequest(`${p}/channels/${channelId}/keys/${encodeURIComponent(apiKey)}`, { method: 'DELETE' }),
      moveKeyToTop: (channelId, apiKey) =>
        apiRequest(`${p}/channels/${channelId}/keys/${encodeURIComponent(apiKey)}/top`, { method: 'POST' }),
      moveKeyToBottom: (channelId, apiKey) =>
        apiRequest(`${p}/channels/${channelId}/keys/${encodeURIComponent(apiKey)}/bottom`, { method: 'POST' }),
      reorder: order => apiRequest(`${p}/channels/reorder`, { method: 'POST', body: JSON.stringify({ order }) }),
      setStatus: (channelId, status) =>
        apiRequest(`${p}/channels/${channelId}/status`, { method: 'PATCH', body: JSON.stringify({ status }) }),
      setPromotion: (channelId, durationSeconds, count) => {
        const duration = normalizePromotionValue(durationSeconds)
        const normalizedCount = normalizePromotionValue(count)
        return apiRequest(`${p}/channels/${channelId}/promotion`, {
          method: 'POST',
          body: JSON.stringify({ duration, count: normalizedCount })
        })
      },
      pingChannel: id => apiRequest(`${p}/ping/${id}`),
      pingAllChannels: () => apiRequest(`${p}/ping`),
      updateLoadBalance: strategy =>
        apiRequest(`${p}/loadbalance`, { method: 'PUT', body: JSON.stringify({ strategy }) }),
      resumeChannel: channelId => apiRequest(`${p}/channels/${channelId}/resume`, { method: 'POST' }),
      getChannelMetrics: () => apiRequest(`${p}/channels/metrics`),
      getChannelMetricsHistory: (duration = '24h') =>
        apiRequest(`${p}/channels/metrics/history?duration=${duration}`),
      getChannelKeyMetricsHistory: (channelId, duration = '6h') =>
        apiRequest(`${p}/channels/${channelId}/keys/metrics/history?duration=${duration}`),
      getGlobalStats: (duration = '24h') => apiRequest(`${p}/global/stats/history?duration=${duration}`),
      getChannelDashboard: () => apiRequest(`${p}/channels/dashboard`),
      listMappingTargets: excludeId => {
        const query = excludeId ? `?excludeId=${encodeURIComponent(excludeId)}` : ''
        return apiRequest(`${p}/mapping-targets${query}`)
      },
      listMappingTargetModels: channelId =>
        apiRequest(`${p}/mapping-targets/${encodeURIComponent(channelId)}/models`)
    }
  },

  async getChannelPools(
    type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images'
  ): Promise<{ pools: ChannelPool[] }> {
    return apiRequest(`/${type}/pools`)
  },

  async createChannelPool(
    type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images',
    pool: Pick<ChannelPool, 'name' | 'modelMatcher'>
  ): Promise<{ pool: ChannelPool }> {
    return apiRequest(`/${type}/pools`, { method: 'POST', body: JSON.stringify(pool) })
  },

  async updateChannelPool(
    type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images',
    id: string,
    pool: Partial<Pick<ChannelPool, 'name' | 'modelMatcher'>>
  ): Promise<void> {
    await apiRequest(`/${type}/pools/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(pool) })
  },

  async deleteChannelPool(type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images', id: string): Promise<void> {
    await apiRequest(`/${type}/pools/${encodeURIComponent(id)}`, { method: 'DELETE' })
  },

  async saveChannelPoolLayout(
    type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images',
    pools: Array<{ poolId: string; channelIds: string[] }>
  ): Promise<void> {
    await apiRequest(`/${type}/pools/layout`, { method: 'PUT', body: JSON.stringify({ pools }) })
  },

  // ============== 多渠道调度 API ==============

  // 复制渠道
  async duplicateChannel(
    type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images',
    channelId: number
  ): Promise<void> {
    await apiRequest(`/${type}/channels/${channelId}/duplicate`, {
      method: 'POST'
    })
  },

  async tidyProblemChannels(type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images'): Promise<void> {
    await apiRequest(`/${type}/channels/tidy`, {
      method: 'POST'
    })
  },

  // 获取渠道指标
  async getChannelMetrics(): Promise<ChannelMetrics[]> {
    return apiRequest('/messages/channels/metrics')
  },

  // 获取调度器统计信息
  async getSchedulerStats(type?: 'messages' | 'responses' | 'gemini' | 'chat' | 'images'): Promise<{
    multiChannelMode: boolean
    activeChannelCount: number
    traceAffinityCount: number
    traceAffinityTTL: string
    failureThreshold: number
    windowSize: number
  }> {
    const query = type && type !== 'messages' ? `?type=${type}` : ''
    return apiRequest(`/messages/channels/scheduler/stats${query}`)
  },

  // 获取渠道仪表盘数据（合并 channels + metrics + stats）
  async getChannelDashboard(
    type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images' = 'messages'
  ): Promise<ChannelDashboardResponse> {
    return apiRequest(`/${type}/channels/dashboard`)
  },

  async getChannelLogs(
    type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images',
    channelId: number
  ): Promise<ChannelLogsResponse> {
    return apiRequest(`/${type}/channels/${channelId}/logs`)
  },

  async discoverUpstreamModels(request: UpstreamModelsRequest): Promise<ModelsResponse> {
    return apiRequest('/upstream/models', {
      method: 'POST',
      body: JSON.stringify(request)
    })
  },

  // ============== 历史指标 API ==============

  // 获取 Messages 渠道历史指标（用于时间序列图表）
  async getChannelMetricsHistory(duration: '1h' | '6h' | '24h' = '24h'): Promise<MetricsHistoryResponse[]> {
    return apiRequest(`/messages/channels/metrics/history?duration=${duration}`)
  },

  // ============== Key 级别历史指标 API ==============

  // 获取 Messages 渠道 Key 级别历史指标（用于 Key 趋势图表）
  async getChannelKeyMetricsHistory(
    channelId: number,
    duration: '1h' | '6h' | '24h' | 'today' = '6h'
  ): Promise<ChannelKeyMetricsHistoryResponse> {
    return apiRequest(`/messages/channels/${channelId}/keys/metrics/history?duration=${duration}`)
  },

  // ============== 全局统计 API ==============

  // 获取 Messages 全局统计历史
  async getMessagesGlobalStats(duration: '1h' | '6h' | '24h' | 'today' = '24h'): Promise<GlobalStatsHistoryResponse> {
    return apiRequest(`/messages/global/stats/history?duration=${duration}`)
  },

  // 获取 Images 全局统计历史
  async getImagesGlobalStats(duration: '1h' | '6h' | '24h' | 'today' = '24h'): Promise<GlobalStatsHistoryResponse> {
    return apiRequest(`/images/global/stats/history?duration=${duration}`)
  },
}
