// API服务模块
import { useAuthStore } from '@/stores/auth'
import { readSSEStream } from '@/utils/sse'
// 本文件主体（ApiService）自身使用的类型，从 @/types 具名导入。
import type {
  AgentPromptAgent,
  AgentPromptFile,
  AppSettings,
  AssistantChatOutcome,
  AssistantMessage,
  AssistantSession,
  AssistantStatusResponse,
  BlockedLogEntry,
  BlockedLogFilters,
  BlockedLogGroupEntriesResponse,
  BlockedLogsResponse,
  Channel,
  ChannelDashboardResponse,
  ChannelKeyMetricsHistoryResponse,
  ChannelLogsResponse,
  ChannelMetrics,
  ChannelPool,
  ChannelStatus,
  ChannelsResponse,
  ClaudeCodeSettings,
  CodexSettings,
  ConversationEntry,
  ConversationKind,
  ConversationRouteOptionsResponse,
  ConversationsResponse,
  CreatedChannelResponse,
  DSHSettings,
  EvalChannelLatest,
  EvalProbe,
  EvalRun,
  EvalStartRunRequest,
  EvalSuite,
  EvalValidateReport,
  EvalWatchConfig,
  GateDecision,
  GateDecisionResponse,
  GatePending,
  GatePendingResponse,
  GlobalStatsHistoryResponse,
  LogAPIType,
  LogDatesResponse,
  MetricsHistoryResponse,
  MappingTargetChannelsResponse,
  ModelsResponse,
  OpenCodeConfig,
  PiAgentDiscoverResult,
  PiAgentModelSettings,
  PiAgentModelSettingsResponse,
  PiAgentProbeResult,
  PiAgentProvider,
  PiAgentProvidersResponse,
  PiAgentStatus,
  PurgeLogsResult,
  PingResult,
  RemoteSkillPreview,
  RequestLogsResponse,
  SystemLogDetail,
  SystemLogsResponse,
  SaveClaudeCodeSettings,
  SaveCodexModelCatalog,
  SaveCodexSettings,
  SaveDSHSettings,
  SaveOpenCodeProvider,
  SkillBackup,
  SkillSearchResponse,
  SkillsResponse,
  ToolVersionInfo,
  UpstreamModelsRequest
} from '@/types'

export class ApiError extends Error {
  readonly status: number
  readonly details?: unknown
  /** 业务错误码（如 CONFIG_COMMITTED：配置已原子提交但收尾失败）。 */
  readonly code?: string
  /**
   * 配置写操作是否已过提交点（文件已写入，内存与磁盘一致）。
   * 为 true 时不得按"未提交"自动重试（一次新增会被重试成两次），
   * 应提示用户刷新查看实际状态（R3 committed 语义，与后端配套）。
   */
  readonly committed?: boolean

  constructor(
    message: string,
    status: number,
    details?: unknown,
    code?: string,
    committed?: boolean
  ) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.details = details
    this.code = code
    this.committed = committed
  }
}

// 从环境变量读取配置
const getApiBase = () => {
  // 在生产环境中，API调用会直接请求当前域名
  if (import.meta.env.PROD) {
    return '/api'
  }

  // 在开发环境中，支持从环境变量配置后端地址
  const backendUrl = import.meta.env.VITE_BACKEND_URL
  const apiBasePath = import.meta.env.VITE_API_BASE_PATH || '/api'

  if (backendUrl) {
    return `${backendUrl}${apiBasePath}`
  }

  // fallback到默认配置
  return '/api'
}

const API_BASE = getApiBase()

// 打印当前API配置（仅开发环境）
if (import.meta.env.DEV) {
  console.log('🔗 API Configuration:', {
    API_BASE,
    BACKEND_URL: import.meta.env.VITE_BACKEND_URL,
    IS_DEV: import.meta.env.DEV,
    IS_PROD: import.meta.env.PROD
  })
}

// 类型定义已按领域拆分到 @/types（channel / eval / logs / metrics / ...）。
// 此处转发，使既有 import { Channel } from '@/services/api' 全部保持可用。
export * from '@/types'

class ApiService {
  /**
   * 返回指定渠道类型的统一 API 方法集（前端渠道管理收敛核心）。
   * 收敛前 getChannels/getResponsesChannels/... 五组命名方法各实现一遍，仅 URL 前缀不同；
   * 现在按 ApiTab 查表，一份实现覆盖五种渠道。
   */
  channelApi(type: ApiTab): ChannelApi {
    const p = `/${type}`
    return {
      getChannels: () => this.request(`${p}/channels`),
      addChannel: channel => this.request(`${p}/channels`, { method: 'POST', body: JSON.stringify(channel) }),
      updateChannel: (id, channel) =>
        this.request(`${p}/channels/${id}`, { method: 'PUT', body: JSON.stringify(channel) }),
      deleteChannel: id => this.request(`${p}/channels/${id}`, { method: 'DELETE' }),
      addKey: (channelId, apiKey) =>
        this.request(`${p}/channels/${channelId}/keys`, { method: 'POST', body: JSON.stringify({ apiKey }) }),
      removeKey: (channelId, apiKey) =>
        this.request(`${p}/channels/${channelId}/keys/${encodeURIComponent(apiKey)}`, { method: 'DELETE' }),
      moveKeyToTop: (channelId, apiKey) =>
        this.request(`${p}/channels/${channelId}/keys/${encodeURIComponent(apiKey)}/top`, { method: 'POST' }),
      moveKeyToBottom: (channelId, apiKey) =>
        this.request(`${p}/channels/${channelId}/keys/${encodeURIComponent(apiKey)}/bottom`, { method: 'POST' }),
      reorder: order => this.request(`${p}/channels/reorder`, { method: 'POST', body: JSON.stringify({ order }) }),
      setStatus: (channelId, status) =>
        this.request(`${p}/channels/${channelId}/status`, { method: 'PATCH', body: JSON.stringify({ status }) }),
      setPromotion: (channelId, durationSeconds, count) => {
        const duration = this.normalizePromotionValue(durationSeconds)
        const normalizedCount = this.normalizePromotionValue(count)
        return this.request(`${p}/channels/${channelId}/promotion`, {
          method: 'POST',
          body: JSON.stringify({ duration, count: normalizedCount })
        })
      },
      pingChannel: id => this.request(`${p}/ping/${id}`),
      pingAllChannels: () => this.request(`${p}/ping`),
      updateLoadBalance: strategy =>
        this.request(`${p}/loadbalance`, { method: 'PUT', body: JSON.stringify({ strategy }) }),
      resumeChannel: channelId => this.request(`${p}/channels/${channelId}/resume`, { method: 'POST' }),
      getChannelMetrics: () => this.request(`${p}/channels/metrics`),
      getChannelMetricsHistory: (duration = '24h') =>
        this.request(`${p}/channels/metrics/history?duration=${duration}`),
      getChannelKeyMetricsHistory: (channelId, duration = '6h') =>
        this.request(`${p}/channels/${channelId}/keys/metrics/history?duration=${duration}`),
      getGlobalStats: (duration = '24h') => this.request(`${p}/global/stats/history?duration=${duration}`),
      getChannelDashboard: () => this.request(`${p}/channels/dashboard`),
      listMappingTargets: excludeId => {
        const query = excludeId ? `?excludeId=${encodeURIComponent(excludeId)}` : ''
        return this.request(`${p}/mapping-targets${query}`)
      },
      listMappingTargetModels: channelId =>
        this.request(`${p}/mapping-targets/${encodeURIComponent(channelId)}/models`)
    }
  }

  // 获取当前 API Key（从 AuthStore）
  private getApiKey(): string | null {
    const authStore = useAuthStore()
    return authStore.apiKey
  }

  private normalizePromotionValue(value: unknown): number {
    const parsed = typeof value === 'number' ? value : Number(String(value ?? '').trim())

    if (!Number.isFinite(parsed) || parsed <= 0) {
      return 0
    }

    return Math.floor(parsed)
  }

  private async parseResponseBody(response: Response): Promise<unknown> {
    const text = await response.text()
    if (!text) return null
    try {
      return JSON.parse(text)
    } catch {
      return text
    }
  }

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  private async request(url: string, options: RequestInit = {}): Promise<any> {
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      ...(options.headers as Record<string, string>)
    }

    // 从 AuthStore 获取 API 密钥并添加到请求头
    const apiKey = this.getApiKey()
    if (apiKey) {
      headers['x-api-key'] = apiKey
    }

    const response = await fetch(`${API_BASE}${url}`, {
      ...options,
      headers
    })

    if (!response.ok) {
      const errorBody = await this.parseResponseBody(response)
      const errorMessage =
        (typeof errorBody === 'object' &&
        errorBody &&
        'error' in errorBody &&
        typeof (errorBody as { error?: unknown }).error === 'string'
          ? (errorBody as { error: string }).error
          : typeof errorBody === 'object' &&
              errorBody &&
              'error' in errorBody &&
              typeof (errorBody as { error?: { message?: unknown } }).error?.message === 'string'
            ? (errorBody as { error: { message: string } }).error.message
            : typeof errorBody === 'object' &&
                errorBody &&
                'message' in errorBody &&
                typeof (errorBody as { message?: unknown }).message === 'string'
              ? (errorBody as { message: string }).message
              : typeof errorBody === 'string'
                ? errorBody
                : null) || `Request failed (${response.status})`

      // 如果是401错误，清除认证信息并提示用户重新登录
      if (response.status === 401) {
        const authStore = useAuthStore()
        authStore.clearAuth()
        // 记录认证失败(前端日志)
        if (import.meta.env.DEV) {
          console.warn('🔒 认证失败 - 时间:', new Date().toISOString())
        }
        throw new ApiError('认证失败，请重新输入访问密钥', response.status, errorBody)
      }

      // 提取后端的业务错误码与已提交标记（R3 committed 语义）。
      const extractField = <T>(field: string): T | undefined => {
        if (typeof errorBody === 'object' && errorBody !== null && field in errorBody) {
          return (errorBody as Record<string, unknown>)[field] as T
        }
        return undefined
      }
      const errorCode = extractField<string>('code')
      const committed = extractField<boolean>('committed')

      throw new ApiError(errorMessage, response.status, errorBody, errorCode, committed)
    }

    if (response.status === 204) return null
    return this.parseResponseBody(response)
  }

  async getChannelPools(
    type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images'
  ): Promise<{ pools: ChannelPool[] }> {
    return this.request(`/${type}/pools`)
  }

  async createChannelPool(
    type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images',
    pool: Pick<ChannelPool, 'name' | 'modelMatcher'>
  ): Promise<{ pool: ChannelPool }> {
    return this.request(`/${type}/pools`, { method: 'POST', body: JSON.stringify(pool) })
  }

  async updateChannelPool(
    type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images',
    id: string,
    pool: Partial<Pick<ChannelPool, 'name' | 'modelMatcher'>>
  ): Promise<void> {
    await this.request(`/${type}/pools/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(pool) })
  }

  async deleteChannelPool(type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images', id: string): Promise<void> {
    await this.request(`/${type}/pools/${encodeURIComponent(id)}`, { method: 'DELETE' })
  }

  async saveChannelPoolLayout(
    type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images',
    pools: Array<{ poolId: string; channelIds: string[] }>
  ): Promise<void> {
    await this.request(`/${type}/pools/layout`, { method: 'PUT', body: JSON.stringify({ pools }) })
  }

  // ============== Responses 渠道管理 API ==============

  // ============== Chat 渠道管理 API ==============

  // ============== 多渠道调度 API ==============

  // 重新排序渠道优先级

  async duplicateChannel(
    type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images',
    channelId: number
  ): Promise<void> {
    await this.request(`/${type}/channels/${channelId}/duplicate`, {
      method: 'POST'
    })
  }

  async tidyProblemChannels(type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images'): Promise<void> {
    await this.request(`/${type}/channels/tidy`, {
      method: 'POST'
    })
  }

  // 设置渠道状态

  // 恢复熔断渠道（重置错误计数）

  // 获取渠道指标
  async getChannelMetrics(): Promise<ChannelMetrics[]> {
    return this.request('/messages/channels/metrics')
  }

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
    return this.request(`/messages/channels/scheduler/stats${query}`)
  }

  // 获取渠道仪表盘数据（合并 channels + metrics + stats）
  async getChannelDashboard(
    type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images' = 'messages'
  ): Promise<ChannelDashboardResponse> {
    return this.request(`/${type}/channels/dashboard`)
  }

  // ============== Responses 多渠道调度 API ==============

  // 重新排序 Responses 渠道优先级

  // 设置 Responses 渠道状态

  // 恢复 Responses 熔断渠道

  // 获取 Responses 渠道指标

  // ============== Chat 多渠道调度 API ==============

  async getChannelLogs(
    type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images',
    channelId: number
  ): Promise<ChannelLogsResponse> {
    return this.request(`/${type}/channels/${channelId}/logs`)
  }

  async getRequestLogs(
    params: { type?: LogAPIType | ''; limit?: number; from?: string; to?: string } = {}
  ): Promise<RequestLogsResponse> {
    const search = new URLSearchParams()
    if (params.type) search.set('type', params.type)
    if (params.limit) search.set('limit', String(params.limit))
    if (params.from) search.set('from', params.from)
    if (params.to) search.set('to', params.to)
    const query = search.toString()
    return this.request(`/request-logs${query ? `?${query}` : ''}`)
  }

  async getSystemLogs(params: { type?: LogAPIType | ''; limit?: number } = {}): Promise<SystemLogsResponse> {
    const search = new URLSearchParams()
    if (params.type) search.set('type', params.type)
    if (params.limit) search.set('limit', String(params.limit))
    const query = search.toString()
    return this.request(`/system-logs${query ? `?${query}` : ''}`)
  }

  async getSystemLog(requestId: string): Promise<SystemLogDetail> {
    return this.request(`/system-logs/${encodeURIComponent(requestId)}`)
  }

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
    return this.request(`/blocked-logs${query ? `?${query}` : ''}`)
  }

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
    return this.request(`/blocked-logs?${query}`)
  }

  async getBlockedLog(id: number): Promise<BlockedLogEntry> {
    return this.request(`/blocked-logs/${id}`)
  }

  async deleteBlockedLog(id: number): Promise<void> {
    await this.request(`/blocked-logs/${id}`, { method: 'DELETE' })
  }

  async clearBlockedLogs(): Promise<{ deleted: number }> {
    return this.request('/blocked-logs', { method: 'DELETE' })
  }

  async getLogDates(): Promise<LogDatesResponse> {
    return this.request('/logs/dates')
  }

  async purgeLogs(body: { scope: 'all' | 'day'; date?: string }): Promise<{ success: boolean; purged: PurgeLogsResult }> {
    return this.request('/logs/purge', { method: 'POST', body: JSON.stringify(body) })
  }

  // 危险命令 AI 闸门：待确认列表 / 用户决定 / 会话级白黑名单
  async getGatePending(signal?: AbortSignal): Promise<GatePendingResponse> {
    return this.request('/security/gate/pending', { signal })
  }

  async decideGatePending(
    id: number,
    decision: 'allow' | 'block',
    signal?: AbortSignal
  ): Promise<{ pending: GatePending }> {
    return this.request(`/security/gate/pending/${id}/decision`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ decision }),
      signal
    })
  }

  async listGateDecisions(): Promise<GateDecisionResponse> {
    return this.request('/security/gate/decisions')
  }

  async addGateDecision(body: {
    conversationId: string
    decision: 'allow' | 'block'
    commandHash: string
    rule?: string
    command?: string
  }): Promise<{ decision: GateDecision }> {
    return this.request('/security/gate/decisions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    })
  }

  async removeGateDecisions(
    conversationId: string,
    commandHash: string
  ): Promise<{ removed: number }> {
    return this.request(
      `/security/gate/decisions/${encodeURIComponent(conversationId)}?commandHash=${encodeURIComponent(commandHash)}`,
      { method: 'DELETE' }
    )
  }

  // AI 配置助手
  async getAssistantStatus(): Promise<AssistantStatusResponse> {
    return this.request('/assistant/status')
  }

  async createAssistantSession(title?: string): Promise<AssistantSession> {
    const response: { session: AssistantSession } = await this.request('/assistant/sessions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: title ?? '' })
    })
    return response.session
  }

  async listAssistantSessions(): Promise<AssistantSession[]> {
    const response: { sessions: AssistantSession[] } = await this.request('/assistant/sessions')
    return response.sessions || []
  }

  async deleteAssistantSession(id: number): Promise<void> {
    await this.request(`/assistant/sessions/${id}`, { method: 'DELETE' })
  }

  async getAssistantMessages(sessionId: number): Promise<AssistantMessage[]> {
    const response: { messages: AssistantMessage[] } = await this.request(`/assistant/sessions/${sessionId}/messages`)
    return response.messages || []
  }

  async sendAssistantMessage(sessionId: number, content: string): Promise<AssistantChatOutcome> {
    return this.request(`/assistant/sessions/${sessionId}/messages`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ content })
    })
  }

  async getConversations(params?: { q?: string; kind?: ConversationKind }): Promise<ConversationsResponse> {
    const search = new URLSearchParams()
    if (params?.q) search.set('q', params.q)
    if (params?.kind) search.set('kind', params.kind)
    const suffix = search.toString() ? `?${search.toString()}` : ''
    return this.request(`/conversations${suffix}`)
  }

  async getConversation(id: string): Promise<ConversationEntry> {
    return this.request(`/conversations/${encodeURIComponent(id)}`)
  }

  async setConversationName(id: string, name: string): Promise<ConversationEntry> {
    return this.request(`/conversations/${encodeURIComponent(id)}/name`, {
      method: 'PUT',
      body: JSON.stringify({ name })
    })
  }

  async deleteConversation(id: string): Promise<void> {
    await this.request(`/conversations/${encodeURIComponent(id)}`, {
      method: 'DELETE'
    })
  }

  async deleteAllConversations(): Promise<void> {
    await this.request('/conversations', {
      method: 'DELETE'
    })
  }

  async getConversationRouteOptions(): Promise<ConversationRouteOptionsResponse> {
    return this.request('/conversations/route-options')
  }

  async setConversationRoute(id: string, kind: ConversationKind, channelIndex: number): Promise<ConversationEntry> {
    return this.request(`/conversations/${encodeURIComponent(id)}/route`, {
      method: 'PUT',
      body: JSON.stringify({ kind, channelIndex })
    })
  }

  async clearConversationRoute(id: string): Promise<ConversationEntry> {
    return this.request(`/conversations/${encodeURIComponent(id)}/route`, {
      method: 'DELETE'
    })
  }

  // ============== 促销期管理 API ==============

  // 设置 Messages 渠道促销期

  // 设置 Responses 渠道促销期

  // 设置 Chat 渠道促销期

  // ============== Fuzzy 模式 API ==============

  async getSettings(): Promise<AppSettings> {
    return this.request('/settings')
  }

  async updateSettings(settings: Partial<AppSettings>): Promise<AppSettings> {
    return this.request('/settings', {
      method: 'PUT',
      body: JSON.stringify(settings)
    })
  }

  /** 弹出原生文件选择对话框选择 CC Switch 路径并保存；用户取消返回空串 */
  async pickCcsPath(): Promise<{ path: string }> {
    return this.request('/settings/ccs/pick-path', { method: 'POST' })
  }

  /** 以 deeplink 为参数拉起 CC Switch，由用户在其确认框中完成导入 */
  async importToCcs(url: string): Promise<{ success: boolean }> {
    return this.request('/settings/ccs/import', {
      method: 'POST',
      body: JSON.stringify({ url })
    })
  }

  async discoverUpstreamModels(request: UpstreamModelsRequest): Promise<ModelsResponse> {
    return this.request('/upstream/models', {
      method: 'POST',
      body: JSON.stringify(request)
    })
  }

  // 获取 Fuzzy 模式状态
  async getFuzzyMode(): Promise<{ fuzzyModeEnabled: boolean }> {
    return this.request('/settings/fuzzy-mode')
  }

  // 设置 Fuzzy 模式状态
  async setFuzzyMode(enabled: boolean): Promise<void> {
    await this.request('/settings/fuzzy-mode', {
      method: 'PUT',
      body: JSON.stringify({ enabled })
    })
  }

  // 获取 Messages / Responses 客户端伪装状态
  async getClientDisguise(): Promise<{
    claudeCodeDisguiseEnabled: boolean
    codexDisguiseEnabled: boolean
  }> {
    return this.request('/settings/client-disguise')
  }

  // 设置指定协议的客户端伪装状态
  async setClientDisguise(protocol: 'messages' | 'responses', enabled: boolean): Promise<void> {
    await this.request('/settings/client-disguise', {
      method: 'PUT',
      body: JSON.stringify({ protocol, enabled })
    })
  }

  // ============== OpenCode 配置 API ==============

  async getOpenCodeConfig(): Promise<OpenCodeConfig> {
    return this.request('/settings/opencode')
  }

  async saveOpenCodeConfig(config: { providers: SaveOpenCodeProvider[] }): Promise<{ success: boolean; path: string }> {
    return this.request('/settings/opencode', {
      method: 'PUT',
      body: JSON.stringify(config)
    })
  }

  // ============== Claude Code 配置 API ==============

  async getClaudeCodeSettings(): Promise<ClaudeCodeSettings> {
    return this.request('/settings/claude-code')
  }

  async saveClaudeCodeSettings(settings: SaveClaudeCodeSettings): Promise<{ success: boolean; path: string }> {
    return this.request('/settings/claude-code', {
      method: 'PUT',
      body: JSON.stringify(settings)
    })
  }

  // ============== GPT / Codex 配置 API ==============

  async getCodexSettings(): Promise<CodexSettings> {
    return this.request('/settings/codex')
  }

  async saveCodexSettings(
    settings: SaveCodexSettings
  ): Promise<{ success: boolean; configPath: string; authPath: string }> {
    return this.request('/settings/codex', {
      method: 'PUT',
      body: JSON.stringify(settings)
    })
  }

  async saveCodexModelCatalog(
    payload: SaveCodexModelCatalog
  ): Promise<{ success: boolean; modelsPath: string; configPath: string }> {
    return this.request('/settings/codex/model-catalog', {
      method: 'PUT',
      body: JSON.stringify(payload)
    })
  }

  // ============== 本代理模型列表（客户端配置页用） ==============

  /**
   * 获取本代理自身暴露的模型列表（等同 /v1/models 的内容，但走 Web 鉴权）。
   * 返回条目的 owned_by 形如 "pool:messages" / "pool:messages,responses"，
   * 标识该模型属于哪些协议分组（包含同名家族别名）；"api-proxy" 表示未配置同名分组的静态别名。
   */
  async getProxyModels(): Promise<ModelsResponse> {
    return this.request('/proxy-models')
  }

  // ============== CLI 工具版本检测（客户端配置页用） ==============

  /**
   * 查询指定 CLI 工具（claude / codex / opencode / pi）的当前版本、
   * npm 最新版本与当前平台的安装命令。force=true 绕过后端 1 小时缓存。
   */
  async getToolVersion(tool: string, force = false): Promise<ToolVersionInfo> {
    const query = force ? '?force=true' : ''
    return this.request(`/tools/${encodeURIComponent(tool)}/version${query}`)
  }

  // ============== DSH 配置 API ==============

  async getDSHSettings(): Promise<DSHSettings> {
    return this.request('/settings/dsh')
  }

  async saveDSHSettings(settings: SaveDSHSettings): Promise<{ success: boolean; path: string; credentialsPath: string }> {
    return this.request('/settings/dsh', {
      method: 'PUT',
      body: JSON.stringify(settings)
    })
  }

  // ============== 智能体全局提示词 API（客户端配置页"全局提示词"卡片） ==============

  /** 读取指定智能体的全局提示词文件（文件不存在返回 exists=false，不算错误） */
  async getAgentPrompt(agent: AgentPromptAgent): Promise<AgentPromptFile> {
    return this.request(`/settings/agent-prompt/${encodeURIComponent(agent)}`)
  }

  /** 整体替换指定智能体的全局提示词文件；后端会先备份原文件再原子写入 */
  async saveAgentPrompt(agent: AgentPromptAgent, content: string): Promise<{ success: boolean; path: string; sizeBytes: number }> {
    return this.request(`/settings/agent-prompt/${encodeURIComponent(agent)}`, {
      method: 'PUT',
      body: JSON.stringify({ content })
    })
  }

  // ============== pi-agent 配置管理 API ==============

  async getPiAgentStatus(): Promise<PiAgentStatus> {
    return this.request('/settings/pi-agent')
  }

  async getPiAgentProviders(): Promise<PiAgentProvidersResponse> {
    return this.request('/settings/pi-agent/providers')
  }

  async createPiAgentProvider(payload: Record<string, unknown>): Promise<{ success: boolean; revision: string }> {
    return this.request('/settings/pi-agent/providers', {
      method: 'POST',
      body: JSON.stringify(payload)
    })
  }

  async updatePiAgentProvider(
    id: string,
    payload: Record<string, unknown>
  ): Promise<{ success: boolean; revision: string }> {
    return this.request(`/settings/pi-agent/providers/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(payload)
    })
  }

  async deletePiAgentProvider(id: string, revision: string): Promise<{ success: boolean; revision: string }> {
    return this.request(`/settings/pi-agent/providers/${encodeURIComponent(id)}`, {
      method: 'DELETE',
      body: JSON.stringify({ revision })
    })
  }

  async validatePiAgentProvider(payload: {
    id: string
    provider: Partial<PiAgentProvider>
  }): Promise<{ valid: boolean; errors: string[] }> {
    return this.request('/settings/pi-agent/validate', {
      method: 'POST',
      body: JSON.stringify(payload)
    })
  }

  async testPiAgentProvider(
    id: string,
    payload: { baseUrl?: string; apiKey?: string; api?: string }
  ): Promise<PiAgentProbeResult> {
    return this.request(`/settings/pi-agent/providers/${encodeURIComponent(id)}/test`, {
      method: 'POST',
      body: JSON.stringify(payload)
    })
  }

  async discoverPiAgentModels(
    id: string,
    payload: { baseUrl?: string; apiKey?: string; api?: string }
  ): Promise<PiAgentDiscoverResult> {
    return this.request(`/settings/pi-agent/providers/${encodeURIComponent(id)}/discover-models`, {
      method: 'POST',
      body: JSON.stringify(payload)
    })
  }

  async getPiAgentModelSettings(): Promise<PiAgentModelSettingsResponse> {
    return this.request('/settings/pi-agent/model-settings')
  }

  async updatePiAgentModelSettings(payload: {
    revision: string
    settings: Partial<PiAgentModelSettings>
  }): Promise<{ success: boolean; revision: string }> {
    return this.request('/settings/pi-agent/model-settings', {
      method: 'PATCH',
      body: JSON.stringify(payload)
    })
  }

  // ============== 本机 Skills 管理 API ==============

  async getSkills(): Promise<SkillsResponse> {
    return this.request('/skills')
  }

  async getSkillContent(locationKey: string, name: string): Promise<{ content: string; path: string }> {
    return this.request('/skills/content', { method: 'POST', body: JSON.stringify({ locationKey, name }) })
  }

  async getLatestSkillBackup(locationKey: string, name: string): Promise<SkillBackup> {
    return this.request('/skills/backup/latest', { method: 'POST', body: JSON.stringify({ locationKey, name }) })
  }

  async updateSkillNote(name: string, note: string): Promise<{ success: boolean; note: string }> {
    return this.request('/skills/note', { method: 'POST', body: JSON.stringify({ name, note }) })
  }

  async consolidateSkills(): Promise<{ success: boolean; total: number; imported: number; duplicates: number }> {
    return this.request('/skills/consolidate', { method: 'POST' })
  }

  async importSkill(
    fileName: string,
    contentBase64: string,
    targets: string[]
  ): Promise<{ success: boolean; name: string }> {
    return this.request('/skills/import', {
      method: 'POST',
      body: JSON.stringify({ fileName, contentBase64, targets })
    })
  }

  async searchSkills(query: string): Promise<SkillSearchResponse> {
    return this.request(`/skills/search?q=${encodeURIComponent(query)}`)
  }

  async inspectRemoteSkill(id: string): Promise<RemoteSkillPreview> {
    return this.request('/skills/remote/inspect', { method: 'POST', body: JSON.stringify({ id }) })
  }

  async installRemoteSkill(
    id: string,
    targets: string[]
  ): Promise<{ success: boolean; name: string; source: string; repositoryUrl: string }> {
    return this.request('/skills/remote/install', { method: 'POST', body: JSON.stringify({ id, targets }) })
  }

  async copySkill(
    locationKey: string,
    name: string,
    targets: string[]
  ): Promise<{ success: boolean; name: string; targets: string[] }> {
    return this.request('/skills/copy', { method: 'POST', body: JSON.stringify({ locationKey, name, targets }) })
  }

  async deleteSkill(locationKey: string, name: string): Promise<void> {
    await this.request('/skills', { method: 'DELETE', body: JSON.stringify({ locationKey, name }) })
  }

  async backupSkill(payload: {
    locationKey: string
    name: string
    original: string
    translated: string
    model?: string
    channelName?: string
  }): Promise<{ success: boolean; path: string }> {
    return this.request('/skills/backup', { method: 'POST', body: JSON.stringify(payload) })
  }

  // ============== 历史指标 API ==============

  // 获取 Messages 渠道历史指标（用于时间序列图表）
  async getChannelMetricsHistory(duration: '1h' | '6h' | '24h' = '24h'): Promise<MetricsHistoryResponse[]> {
    return this.request(`/messages/channels/metrics/history?duration=${duration}`)
  }

  // 获取 Responses 渠道历史指标

  // 获取 Chat 渠道历史指标

  // ============== Key 级别历史指标 API ==============

  // 获取 Messages 渠道 Key 级别历史指标（用于 Key 趋势图表）
  async getChannelKeyMetricsHistory(
    channelId: number,
    duration: '1h' | '6h' | '24h' | 'today' = '6h'
  ): Promise<ChannelKeyMetricsHistoryResponse> {
    return this.request(`/messages/channels/${channelId}/keys/metrics/history?duration=${duration}`)
  }

  // 获取 Responses 渠道 Key 级别历史指标

  // 获取 Chat 渠道 Key 级别历史指标

  // ============== 全局统计 API ==============

  // 获取 Messages 全局统计历史
  async getMessagesGlobalStats(duration: '1h' | '6h' | '24h' | 'today' = '24h'): Promise<GlobalStatsHistoryResponse> {
    return this.request(`/messages/global/stats/history?duration=${duration}`)
  }

  // 获取 Responses 全局统计历史

  // 获取 Chat 全局统计历史

  // 获取 Images 全局统计历史
  async getImagesGlobalStats(duration: '1h' | '6h' | '24h' | 'today' = '24h'): Promise<GlobalStatsHistoryResponse> {
    return this.request(`/images/global/stats/history?duration=${duration}`)
  }

  // ============== Gemini 渠道管理 API ==============

  // ============== Gemini 多渠道调度 API ==============

  // Gemini 恢复渠道（降级实现：后端未实现 resume 端点，直接设置状态为 active）

  // ============== Gemini 历史指标 API ==============

  // 获取 Gemini 渠道历史指标

  // 获取 Gemini 渠道 Key 级别历史指标

  // 获取 Gemini 全局统计历史

  // Gemini Dashboard（使用后端统一接口）

  // ===== Images API =====

  async listEvalProbes(): Promise<{ probes: EvalProbe[] }> {
    return this.request('/eval/probes')
  }

  async createEvalProbe(probe: Partial<EvalProbe>): Promise<{ probe: EvalProbe; validate: EvalValidateReport }> {
    return this.request('/eval/probes', { method: 'POST', body: JSON.stringify(probe) })
  }

  async updateEvalProbe(id: string, probe: Partial<EvalProbe>): Promise<{ probe: EvalProbe }> {
    return this.request(`/eval/probes/${id}`, { method: 'PUT', body: JSON.stringify(probe) })
  }

  async deleteEvalProbe(id: string): Promise<void> {
    return this.request(`/eval/probes/${id}`, { method: 'DELETE' })
  }

  async validateEvalProbe(probe: Partial<EvalProbe>): Promise<EvalValidateReport> {
    return this.request('/eval/probes/validate', { method: 'POST', body: JSON.stringify(probe) })
  }

  async listEvalSuites(): Promise<{ suites: EvalSuite[] }> {
    return this.request('/eval/suites')
  }

  async createEvalSuite(suite: Partial<EvalSuite>): Promise<{ suite: EvalSuite }> {
    return this.request('/eval/suites', { method: 'POST', body: JSON.stringify(suite) })
  }

  async updateEvalSuite(id: string, suite: Partial<EvalSuite>): Promise<{ suite: EvalSuite }> {
    return this.request(`/eval/suites/${id}`, { method: 'PUT', body: JSON.stringify(suite) })
  }

  async deleteEvalSuite(id: string): Promise<void> {
    return this.request(`/eval/suites/${id}`, { method: 'DELETE' })
  }

  async startEvalRun(payload: EvalStartRunRequest): Promise<{ run: EvalRun }> {
    return this.request('/eval/runs', { method: 'POST', body: JSON.stringify(payload) })
  }

  /**
   * 评测批次历史。channelId 为渠道稳定 UUID：渠道行深链进来时按渠道过滤，
   * 否则该渠道的历史会被全局最近 N 条挤掉（后端 limit 默认 30、上限 200）。
   */
  async listEvalRuns(options?: {
    channelId?: string
    limit?: number
  }): Promise<{ runs: EvalRun[]; busy: boolean; currentRunId: string }> {
    const params = new URLSearchParams()
    if (options?.channelId) params.set('channel', options.channelId)
    if (options?.limit) params.set('limit', String(options.limit))
    const query = params.toString()
    return this.request(`/eval/runs${query ? `?${query}` : ''}`)
  }

  async getEvalRun(id: string): Promise<{ run: EvalRun }> {
    return this.request(`/eval/runs/${id}`)
  }

  async cancelEvalRun(id: string): Promise<{ ok: boolean }> {
    return this.request(`/eval/runs/${id}/cancel`, { method: 'POST' })
  }

  async getEvalLatestMap(): Promise<{
    channels: Record<string, EvalChannelLatest>
    watch: EvalWatchConfig
    busy: boolean
  }> {
    return this.request('/eval/channels/latest-map')
  }

  async getEvalWatch(): Promise<{ watch: EvalWatchConfig }> {
    return this.request('/eval/watch')
  }

  async putEvalWatch(watch: Partial<EvalWatchConfig>): Promise<{ watch: EvalWatchConfig }> {
    return this.request('/eval/watch', { method: 'PUT', body: JSON.stringify(watch) })
  }

  async estimateEvalRun(payload: EvalStartRunRequest): Promise<{ estimatedCalls: number; suiteCheap: boolean }> {
    return this.request('/eval/estimate', { method: 'POST', body: JSON.stringify(payload) })
  }

  /**
   * 订阅评测批次进度。后端只在 status / 已完成格子数变化时发帧，跑完自动关闭。
   * EventSource 带不了 x-api-key，所以走 fetch + readSSEStream。
   */
  async streamEvalRun(
    id: string,
    onData: (payload: { run?: EvalRun; error?: string }) => void,
    options?: { signal?: AbortSignal }
  ): Promise<void> {
    const headers: Record<string, string> = { Accept: 'text/event-stream' }
    const apiKey = this.getApiKey()
    if (apiKey) {
      headers['x-api-key'] = apiKey
    }
    const response = await fetch(`${API_BASE}/eval/runs/${encodeURIComponent(id)}/events`, {
      headers,
      signal: options?.signal
    })
    if (!response.ok) {
      throw new ApiError(`评测进度流打开失败: HTTP ${response.status}`, response.status)
    }
    await readSSEStream<{ run?: EvalRun; error?: string }>(response, onData, options)
  }
}

// 健康检查响应类型
export interface HealthResponse {
  version?: {
    version: string
    buildTime: string
    gitCommit: string
  }
  timestamp: string
  uptime: number
  mode: string
  /** 本代理监听端口，用于客户端配置页拼装默认 Base URL（http://localhost:{port}） */
  port?: number
}

/**
 * 获取健康检查信息（包含版本号）
 * 注意：/health 端点不需要认证，直接请求根路径
 */
export const fetchHealth = async (): Promise<HealthResponse> => {
  const baseUrl = import.meta.env.PROD ? '' : import.meta.env.VITE_BACKEND_URL || ''
  const response = await fetch(`${baseUrl}/health`)
  if (!response.ok) {
    throw new Error(`Health check failed: ${response.status}`)
  }
  return response.json()
}

export const api = new ApiService()

export function fetchUpstreamModels(
  baseUrl: string,
  apiKey: string = '',
  serviceType?: string,
  options: Omit<UpstreamModelsRequest, 'baseUrl' | 'apiKey' | 'serviceType'> = {}
): Promise<ModelsResponse> {
  return api.discoverUpstreamModels({
    baseUrl,
    apiKey,
    serviceType,
    ...options
  })
}

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

/**
 * 返回指定渠道类型的统一 API 方法集。
 * 这是前端渠道管理收敛的核心：store/组件按 ApiTab 取用，不再散落 5 组命名方法。
 */
export function channelApiByType(type: ApiTab): ChannelApi {
  return api.channelApi(type)
}

export interface TestChannelContext {
  purpose?: 'quick_test'
  channelIndex?: number
  thinking?: string
  sessionId?: string
  threadId?: string
  interactionId?: string
  onInteractionId?: (_id: string) => void
  responseId?: string
  onResponseId?: (_id: string) => void
  signal?: AbortSignal
  messages?: Array<{ role: 'user' | 'assistant'; content: string }>
}

export const testChannelWithModel = async (
  apiType: ApiTab,
  channelId: string,
  model: string | undefined,
  message: string,
  onChunk: (_chunk: string) => void,
  sessionContext?: TestChannelContext
): Promise<void> => {
  if (!channelId.trim()) throw new Error('渠道缺少稳定 ID，无法执行测试')
  const channelIndex = sessionContext?.channelIndex ?? Number(channelId)
  if (!Number.isInteger(channelIndex) || channelIndex < 0) {
    throw new Error('快捷测试缺少有效的渠道索引')
  }

  const authStore = useAuthStore()
  const accessKey = authStore.apiKey?.trim() || ''
  if (!accessKey) throw new Error('缺少访问密钥，请先完成登录认证')

  const baseUrl = import.meta.env.PROD ? '' : import.meta.env.VITE_BACKEND_URL || ''
  const generateUUID = () => crypto.randomUUID()
  const sessionId = sessionContext?.sessionId || generateUUID()
  const threadId = sessionContext?.threadId || `thread-${generateUUID()}`
  const selectedModel = model?.trim() || ''
  if (!selectedModel) throw new Error('请选择或输入用于快捷测试的模型')
  const testPurpose = sessionContext?.purpose || 'quick_test'
  const metadata = {
    channel_index: channelIndex,
    purpose: testPurpose,
    user_id: sessionId,
    session_id: sessionId,
    thread_id: threadId
  }

  let endpoint = ''
  let body: Record<string, unknown> = {}
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    'x-api-key': accessKey,
    'x-proxy-purpose': testPurpose
  }

  switch (apiType) {
    case 'messages':
      endpoint = '/v1/messages'
      body = {
        model: selectedModel,
        max_tokens: 1024,
        messages: [{ role: 'user', content: message }],
        metadata,
        stream: true
      }
      break
    case 'responses':
      endpoint = '/v1/responses'
      body = { model: selectedModel, input: message, metadata, stream: true }
      if (sessionContext?.responseId) body.previous_response_id = sessionContext.responseId
      break
    case 'gemini':
      endpoint = `/v1beta/models/${encodeURIComponent(selectedModel.replace(/^models\//, ''))}:streamGenerateContent`
      body = { contents: [{ role: 'user', parts: [{ text: message }] }], metadata }
      if (sessionContext?.interactionId) body.previous_interaction_id = sessionContext.interactionId
      break
    case 'chat':
      endpoint = '/v1/chat/completions'
      body = {
        model: selectedModel,
        messages: [{ role: 'user', content: message }],
        metadata,
        user: sessionId,
        stream: true
      }
      break
    case 'images':
      endpoint = '/v1/images/generations'
      body = { model: selectedModel, prompt: message, metadata, n: 1, response_format: 'url' }
      break
  }

  const response = await fetch(`${baseUrl}${endpoint}`, {
    method: 'POST',
    headers,
    body: JSON.stringify(body),
    signal: sessionContext?.signal
  })
  if (!response.ok) {
    const text = await response.text()
    let detail = ''
    try {
      const parsed = JSON.parse(text)
      detail = parsed.error?.message || parsed.error || parsed.message || ''
    } catch {
      detail = text
    }
    throw new Error(detail || `请求失败: ${response.status} ${response.statusText}`)
  }

  if (apiType === 'images') {
    const result = await response.json()
    // images 生成分支：每项可能是 URL、修订提示词或 base64 数据。
    const imageItems: Array<{ url?: string; revised_prompt?: string; b64_json?: string }> = Array.isArray(
      result.data
    )
      ? result.data
      : []
    const outputs = imageItems
      .map(item => item.url || item.revised_prompt || (item.b64_json ? '[base64 image data]' : ''))
      .filter(Boolean)
    onChunk(outputs.length > 0 ? outputs.join('\n') : JSON.stringify(result, null, 2))
    return
  }

  if (!response.body) throw new Error('上游响应缺少响应体')
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  const processLine = (line: string) => {
    if (!line.trim() || !line.startsWith('data:')) return
    const data = line.slice(5).trim()
    if (data === '[DONE]') return
    // SSE 事件载荷：各协议字段并集，按 apiType 取各自关心的字段。
    let parsed: {
      type?: string
      delta?: { text?: string }
      response?: { id?: string }
      id?: string
      candidates?: Array<{ content?: { parts?: Array<{ text?: string }> } }>
      choices?: Array<{ delta?: { content?: string } }>
      completion?: string
    }
    try {
      parsed = JSON.parse(data)
    } catch {
      return
    }
    if (apiType === 'responses' && parsed.type === 'response.completed' && parsed.response?.id) {
      sessionContext?.onResponseId?.(parsed.response.id)
      sessionContext?.onInteractionId?.(parsed.response.id)
    }
    if (apiType === 'gemini' && parsed.id) sessionContext?.onInteractionId?.(parsed.id)
    const content =
      apiType === 'messages'
        ? parsed.delta?.text
        : apiType === 'responses'
          ? parsed.type === 'response.output_text.delta'
            ? parsed.delta
            : parsed.completion
          : apiType === 'gemini'
            ? parsed.candidates?.[0]?.content?.parts?.[0]?.text
            : parsed.choices?.[0]?.delta?.content
    if (typeof content === 'string' && content) onChunk(content)
  }

  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''
      lines.forEach(processLine)
    }
    buffer += decoder.decode()
    if (buffer) processLine(buffer)
  } finally {
    reader.releaseLock()
  }
}

/**
 * 测试渠道连通性（旧版接口，使用默认模型，支持多轮会话）
 * 委托给 testChannelWithModel 实现
 */
export const testChannel = async (
  apiType: ApiTab,
  channelId: string,
  message: string,
  onChunk: (_chunk: string) => void,
  sessionContext?: TestChannelContext
): Promise<void> => {
  return testChannelWithModel(apiType, channelId, undefined, message, onChunk, sessionContext)
}

export default api
