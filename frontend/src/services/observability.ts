// 日志与对话 API：复用统一 HTTP 基础层，保留端点与返回契约。
import { apiRequest } from './http'
import type {
  BlockedLogEntry,
  BlockedLogFilters,
  BlockedLogGroupEntriesResponse,
  BlockedLogsResponse,
  ConversationEntry,
  ConversationKind,
  ConversationRouteOptionsResponse,
  ContextOverview,
  ConversationsResponse,
  LogAPIType,
  LogCallAttempts,
  LogCallFilters,
  LogCallsResponse,
  LogDatesResponse,
  LogConversationsResponse,
  LogConversationRequestsResponse,
  PurgeLogsResult,
  RequestLogsResponse,
  RuntimeLogsResponse,
  SystemLogDetail,
  SystemLogsResponse
} from '@/types'

export const observabilityApi = {
  async getLogCalls(params: LogCallFilters & { cursor?: string; limit?: number } = {}): Promise<LogCallsResponse> {
    const search = new URLSearchParams()
    for (const key of ['type', 'q', 'status', 'from', 'to', 'cursor'] as const) {
      const value = params[key]
      if (value) search.set(key, value)
    }
    if (params.limit) search.set('limit', String(params.limit))
    return apiRequest(`/log-calls?${search}`)
  },

  async getLogCallAttempts(requestId: string): Promise<LogCallAttempts> {
    return apiRequest(`/log-call-attempts?${new URLSearchParams({ requestId })}`)
  },

  async getRequestLogs(
    params: { type?: LogAPIType | ''; limit?: number; from?: string; to?: string } = {}
  ): Promise<RequestLogsResponse> {
    const search = new URLSearchParams()
    if (params.type) search.set('type', params.type)
    if (params.limit) search.set('limit', String(params.limit))
    if (params.from) search.set('from', params.from)
    if (params.to) search.set('to', params.to)
    const query = search.toString()
    return apiRequest(`/request-logs${query ? `?${query}` : ''}`)
  },

  async getSystemLogs(params: { type?: LogAPIType | ''; limit?: number } = {}): Promise<SystemLogsResponse> {
    const search = new URLSearchParams()
    if (params.type) search.set('type', params.type)
    if (params.limit) search.set('limit', String(params.limit))
    const query = search.toString()
    return apiRequest(`/system-logs${query ? `?${query}` : ''}`)
  },

  async getSystemLog(requestId: string): Promise<SystemLogDetail> {
    return apiRequest(`/system-logs/${encodeURIComponent(requestId)}`)
  },

  async getLogConversations(params: { type?: LogAPIType | ''; cursor?: string; limit?: number; q?: string; status?: string; from?: string; to?: string } = {}): Promise<LogConversationsResponse> {
    const search = new URLSearchParams()
    if (params.type) search.set('type', params.type)
    if (params.cursor) search.set('cursor', params.cursor)
    if (params.q) search.set('q', params.q)
    if (params.status) search.set('status', params.status)
    if (params.from) search.set('from', params.from)
    if (params.to) search.set('to', params.to)
    if (params.limit) search.set('limit', String(params.limit))
    return apiRequest(`/log-conversations?${search}`)
  },

  async getLogConversationRequests(params: { key: string; before?: string; anchor?: string; limit?: number }): Promise<LogConversationRequestsResponse> {
    const search = new URLSearchParams({ key: params.key })
    if (params.before) search.set('before', params.before)
    if (params.anchor) search.set('anchor', params.anchor)
    if (params.limit) search.set('limit', String(params.limit))
    return apiRequest(`/log-conversation-requests?${search}`)
  },

  async getLogContextOverview(key: string): Promise<ContextOverview> {
    return apiRequest(`/log-context-overview?${new URLSearchParams({ key })}`)
  },

  async getRuntimeLogs(params: { q?: string; before?: string } = {}): Promise<RuntimeLogsResponse> {
    const search = new URLSearchParams()
    if (params.q) search.set('q', params.q)
    if (params.before) search.set('before', params.before)
    return apiRequest(`/runtime-logs?${search}`)
  },

  async getBlockedLogs(params: BlockedLogFilters = {}): Promise<BlockedLogsResponse> {
    const search = new URLSearchParams()
    if (params.apiType) search.set('apiType', params.apiType)
    if (params.blockType) search.set('blockType', params.blockType)
    if (params.category) search.set('category', params.category)
    if (params.activeOnly) search.set('activeOnly', 'true')
    if (params.from) search.set('from', params.from)
    if (params.to) search.set('to', params.to)
    if (params.page) search.set('page', String(params.page))
    if (params.pageSize) search.set('pageSize', String(params.pageSize))
    if (params.groupBy) search.set('groupBy', params.groupBy)
    if (params.groupKey) search.set('groupKey', params.groupKey)
    const query = search.toString()
    return apiRequest(`/blocked-logs${query ? `?${query}` : ''}`)
  },

  async getBlockedLogGroupEntries(params: BlockedLogFilters): Promise<BlockedLogGroupEntriesResponse> {
    const search = new URLSearchParams()
    if (params.apiType) search.set('apiType', params.apiType)
    if (params.blockType) search.set('blockType', params.blockType)
    if (params.category) search.set('category', params.category)
    if (params.activeOnly) search.set('activeOnly', 'true')
    if (params.from) search.set('from', params.from)
    if (params.to) search.set('to', params.to)
    if (params.page) search.set('page', String(params.page))
    search.set('pageSize', String(params.pageSize ?? 50))
    search.set('groupKey', params.groupKey ?? '')
    const query = search.toString()
    return apiRequest(`/blocked-logs?${query}`)
  },

  async getBlockedLog(id: number): Promise<BlockedLogEntry> {
    return apiRequest(`/blocked-logs/${id}`)
  },

  async deleteBlockedLog(id: number): Promise<void> {
    await apiRequest(`/blocked-logs/${id}`, { method: 'DELETE' })
  },

  async clearBlockedLogs(): Promise<{ deleted: number }> {
    return apiRequest('/blocked-logs', { method: 'DELETE' })
  },

  async getLogDates(): Promise<LogDatesResponse> {
    return apiRequest('/logs/dates')
  },

  async purgeLogs(body: { scope: 'all' | 'day'; date?: string }): Promise<{ success: boolean; purged: PurgeLogsResult }> {
    return apiRequest('/logs/purge', { method: 'POST', body: JSON.stringify(body) })
  },

  async getConversations(params?: { q?: string; kind?: ConversationKind; page?: number; pageSize?: number; status?: 'all' | 'active' | 'idle'; order?: 'created' | 'status' }): Promise<ConversationsResponse> {
    const search = new URLSearchParams()
    if (params?.q) search.set('q', params.q)
    if (params?.kind) search.set('kind', params.kind)
    if (params?.page) search.set('page', String(params.page))
    if (params?.pageSize) search.set('pageSize', String(params.pageSize))
    if (params?.status) search.set('status', params.status)
    if (params?.order) search.set('order', params.order)
    const suffix = search.toString() ? `?${search.toString()}` : ''
    return apiRequest(`/conversations${suffix}`)
  },

  async getConversation(id: string): Promise<ConversationEntry> {
    return apiRequest(`/conversations/${encodeURIComponent(id)}`)
  },

  async setConversationName(id: string, name: string): Promise<ConversationEntry> {
    return apiRequest(`/conversations/${encodeURIComponent(id)}/name`, {
      method: 'PUT',
      body: JSON.stringify({ name })
    })
  },

  async deleteConversation(id: string): Promise<void> {
    await apiRequest(`/conversations/${encodeURIComponent(id)}`, {
      method: 'DELETE'
    })
  },

  async deleteAllConversations(): Promise<void> {
    await apiRequest('/conversations', {
      method: 'DELETE'
    })
  },

  async getConversationRouteOptions(): Promise<ConversationRouteOptionsResponse> {
    return apiRequest('/conversations/route-options')
  },

  async setConversationRoute(id: string, kind: ConversationKind, channelIndex: number): Promise<ConversationEntry> {
    return apiRequest(`/conversations/${encodeURIComponent(id)}/route`, {
      method: 'PUT',
      body: JSON.stringify({ kind, channelIndex })
    })
  },

  async clearConversationRoute(id: string): Promise<ConversationEntry> {
    return apiRequest(`/conversations/${encodeURIComponent(id)}/route`, {
      method: 'DELETE'
    })
  },
}
