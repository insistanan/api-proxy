// 渠道与指标 API：复用统一 HTTP 基础层，保留端点与返回契约。
import { apiRequest } from '@/shared/services/http'
import type {
  ChannelDeletionImpact,
  ChannelDashboardResponse,
  ChannelKeyMetricsHistoryResponse,
  ChannelLogsResponse,
  ChannelMetrics,
  ChannelPool,
  GlobalStatsHistoryResponse,
  MetricsHistoryResponse,
  ModelsResponse,
  UpstreamModelsRequest,
  VisionChannelsResponse
} from '@/types'
import type { ApiTab, ChannelApi } from '@/features/channels/services/channel-api'

function channelIdentity(expectedChannelId: string): Record<string, string> {
  if (!expectedChannelId) throw new Error('操作目标缺少稳定渠道 ID，请刷新后重试')
  return { 'X-Channel-ID': expectedChannelId }
}

function normalizePromotionValue(value: unknown): number {
  const parsed = typeof value === 'number' ? value : Number(String(value ?? '').trim())

  if (!Number.isSafeInteger(parsed) || parsed < 0) {
    throw new Error('促销限制必须是有效的非负整数')
  }

  return parsed
}

export const channelsApi = {
  getChannelDeletionImpact(type: ApiTab, channelId: number): Promise<ChannelDeletionImpact> {
    return apiRequest(`/${type}/channels/${channelId}/deletion-impact`)
  },
  getChannelPoolDeletionImpact(type: ApiTab, poolId: string): Promise<ChannelDeletionImpact> {
    return apiRequest(`/${type}/pools/${encodeURIComponent(poolId)}/deletion-impact`)
  },
  /**
   * 根据入口协议构造管理 API 的路径前缀。
   * 五组渠道共用这套操作，避免分别维护重复的 CRUD 实现。
   */
  channelApi(type: ApiTab): ChannelApi {
    const p = `/${type}`
    return {
      getChannels: () => apiRequest(`${p}/channels`),
      addChannel: channel => apiRequest(`${p}/channels`, { method: 'POST', body: JSON.stringify(channel) }),
      updateChannel: (id, channel, expectedChannelId) =>
        apiRequest(`${p}/channels/${id}`, { method: 'PUT', body: JSON.stringify({ ...channel, expectedChannelId }) }),
      deleteChannel: (id, expectedId) => apiRequest(`${p}/channels/${id}`, { method: 'DELETE', headers: channelIdentity(expectedId) }),
      addKey: (channelId, apiKey, expectedId) =>
        apiRequest(`${p}/channels/${channelId}/keys`, { method: 'POST', headers: channelIdentity(expectedId), body: JSON.stringify({ apiKey }) }),
      removeKey: (channelId, apiKey, expectedId) =>
        apiRequest(`${p}/channels/${channelId}/keys`, { method: 'DELETE', headers: channelIdentity(expectedId), body: JSON.stringify({ apiKey }) }),
      moveKeyToTop: (channelId, apiKey, expectedId) =>
        apiRequest(`${p}/channels/${channelId}/keys/top`, { method: 'POST', headers: channelIdentity(expectedId), body: JSON.stringify({ apiKey }) }),
      moveKeyToBottom: (channelId, apiKey, expectedId) =>
        apiRequest(`${p}/channels/${channelId}/keys/bottom`, { method: 'POST', headers: channelIdentity(expectedId), body: JSON.stringify({ apiKey }) }),
      reorder: order => apiRequest(`${p}/channels/reorder`, { method: 'POST', body: JSON.stringify({ order }) }),
      setStatus: (channelId, status, expectedId) =>
        apiRequest(`${p}/channels/${channelId}/status`, { method: 'PATCH', headers: channelIdentity(expectedId), body: JSON.stringify({ status }) }),
      setPromotion: (channelId, durationSeconds, count, expectedId) => {
        const duration = normalizePromotionValue(durationSeconds)
        const normalizedCount = normalizePromotionValue(count)
        return apiRequest(`${p}/channels/${channelId}/promotion`, {
          method: 'POST',
          headers: channelIdentity(expectedId),
          body: JSON.stringify({ duration, count: normalizedCount })
        })
      },
      pingChannel: id => apiRequest(`${p}/ping/${id}`),
      pingAllChannels: () => apiRequest(`${p}/ping`),
      updateLoadBalance: strategy =>
        apiRequest(`${p}/loadbalance`, { method: 'PUT', body: JSON.stringify({ strategy }) }),
      resumeChannel: (channelId, expectedId) => apiRequest(`${p}/channels/${channelId}/resume`, { method: 'POST', headers: channelIdentity(expectedId) }),
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

  async getChannelPools(type: ApiTab): Promise<{ pools: ChannelPool[] }> {
    return apiRequest(`/${type}/pools`)
  },

  // 四个会话协议共享的公用纯图片理解池，渠道自带所属协议。
  async getVisionChannels(): Promise<VisionChannelsResponse> {
    return apiRequest('/vision-channels')
  },

  // 四个会话协议共享的文本摘要池，渠道自带所属协议。
  async getSummaryChannels(): Promise<VisionChannelsResponse> {
    return apiRequest('/summary-channels')
  },

  async createChannelPool(
    type: ApiTab,
    pool: Pick<ChannelPool, 'name' | 'modelMatcher'>
  ): Promise<{ pool: ChannelPool }> {
    return apiRequest(`/${type}/pools`, { method: 'POST', body: JSON.stringify(pool) })
  },

  async updateChannelPool(
    type: ApiTab,
    id: string,
    pool: Partial<Pick<ChannelPool, 'name' | 'modelMatcher'>>
  ): Promise<void> {
    await apiRequest(`/${type}/pools/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(pool) })
  },

  async deleteChannelPool(type: ApiTab, id: string): Promise<void> {
    await apiRequest(`/${type}/pools/${encodeURIComponent(id)}`, { method: 'DELETE' })
  },

  async saveChannelPoolLayout(
    type: ApiTab,
    pools: Array<{ poolId: string; channelIds: string[] }>
  ): Promise<void> {
    await apiRequest(`/${type}/pools/layout`, { method: 'PUT', body: JSON.stringify({ pools }) })
  },

  // ============== 多渠道调度 API ==============

  // 复制渠道
  async duplicateChannel(
    type: ApiTab,
    channelId: number
  ): Promise<void> {
    await apiRequest(`/${type}/channels/${channelId}/duplicate`, {
      method: 'POST'
    })
  },

  async tidyProblemChannels(type: ApiTab): Promise<void> {
    await apiRequest(`/${type}/channels/tidy`, {
      method: 'POST'
    })
  },

  // 获取渠道指标
  async getChannelMetrics(): Promise<ChannelMetrics[]> {
    return apiRequest('/messages/channels/metrics')
  },

  // 获取调度器统计信息
  async getSchedulerStats(type?: ApiTab): Promise<{
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
  async getChannelDashboard(type: ApiTab = 'messages'): Promise<ChannelDashboardResponse> {
    return apiRequest(`/${type}/channels/dashboard`)
  },

  async getChannelLogs(type: ApiTab, channelId: number): Promise<ChannelLogsResponse> {
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
