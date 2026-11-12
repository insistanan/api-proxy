// 日志类型：渠道日志、请求日志与内容安全拦截日志。

import type { ConversationKind } from './conversation'

export type LogAPIType = ConversationKind | 'cmd_gate' | 'llmgate'

export interface ChannelLogEntry {
  requestId: string
  attemptId: string
  timestamp: string
  status: string
  statusCode?: number
  success: boolean
  durationMs: number
  apiType: string
  model?: string
  inputTokens?: number
  outputTokens?: number
  cacheCreationTokens?: number
  cacheReadTokens?: number
  cacheCreation5mTokens?: number
  cacheCreation1hTokens?: number
  channelIndex: number
  channelName?: string
  baseUrl: string
  keyMask: string
  errorType?: string
  errorMessage?: string
  retried: boolean
  stream: boolean
}

export interface ChannelLogsResponse {
  channelIndex: number
  channelName?: string
  logs: ChannelLogEntry[]
}

export interface RequestLogEntry extends ChannelLogEntry {
  apiType: LogAPIType
  entry?: 'claude' | 'codex' | 'gemini' | 'chat' | 'images' | 'embeddings' | 'decisions' | 'cmd_gate' | 'llmgate'
  firstTokenMs?: number
  resolvedModel?: string
  transform?: string
  cacheTTL?: string
  tpm?: number
  conversationId?: string
  parentRequestId?: string
}

export interface RequestLogsResponse {
  logs: RequestLogEntry[]
  limit: number
}

export interface LogCallSummary {
  summary: SystemLogSummary
  attemptCount: number
  attemptsVersion?: number
}

export interface LogCallsResponse {
  calls: LogCallSummary[]
  nextCursor?: string
}

export interface LogCallAttempts {
  attempts: RequestLogEntry[]
  limited: boolean
  notice: string
}

export interface LogCallFilters {
  type?: LogAPIType | ''
  q?: string
  status?: string
  from?: string
  to?: string
}

export type TrafficLogPhase = 'client_request' | 'upstream_request' | 'upstream_response' | 'stream_synth'

export interface TrafficLog {
  id?: number
  timestamp: string
  unixMilli?: number
  requestId: string
  attemptId?: string
  phase: TrafficLogPhase | string
  apiType?: string
  method?: string
  url?: string
  statusCode?: number
  stream?: boolean
  headers?: string
  body?: string
  truncated?: boolean
  originalBytes?: number
}

export interface SystemLogSummary {
  requestId: string
  timestamp: string
  unixMilli: number
  apiType?: string
  phases: string[]
  missingPhases?: string[]
  statusCode?: number
  stream: boolean
  truncated: boolean
  channelName?: string
  model?: string
  status?: string
  conversationId?: string
  requestHash?: string
  client?: string
  errorType?: string
  errorMessage?: string
  startedAt?: number
  promptPreview?: string
  clientFamily?: string
  identitySource?: string
  context?: ContextSnapshot
}

export interface ContextPart {
  kind: string
  items: number
  bytes: number
  estimatedTokens: number
}

export interface ContextItem {
  path: string
  kind: string
  hash: string
  estimatedTokens: number
}

export interface ContextSnapshot {
  trafficId?: number
  phase?: string
  attemptId?: string
  completeness: 'captured' | 'summary' | 'unavailable'
  notice?: string
  previousResponseId?: string
  messageCount: number
  originalMessages: number
  estimatedTokens: number
  parts: ContextPart[]
  items?: ContextItem[]
  itemsLimited?: boolean
  inputChange?: ContextChange
}

export interface ContextChange {
  previousRequestId: string
  added: number
  removed: number
  estimatedDelta: number
  comparable: boolean
  orderChanged: boolean
  notice: string
}

export interface ContextTrendPoint {
  firstAt: number
  lastAt: number
  requestId?: string
  calls: number
  knownCalls: number
  peak?: ContextSnapshot
}

export interface ContextOverview {
  calls: number
  knownCalls: number
  bucketSize: number
  points: ContextTrendPoint[]
}

export interface ToolObservation {
  callId: string
  role: 'call' | 'result'
  name?: string
  source: 'input_history' | 'model_output'
  path: string
  requestId: string
  attemptId?: string
  isError?: boolean
}

export interface ToolLinks {
  observations: ToolObservation[]
  limited: boolean
  notice: string
}

export interface SystemLogsResponse {
  logs: SystemLogSummary[]
  limit: number
}

export interface LogConversationSummary {
  key: string
  latest: SystemLogSummary
  requestCount: number
  title?: string
}

export interface LogConversationsResponse {
  conversations: LogConversationSummary[]
  nextCursor?: string
  total?: number
}

export interface LogConversationRequestsResponse {
  logs: SystemLogSummary[]
  nextCursor?: string
}

export interface RuntimeLogEntry {
  id: number
  timestamp: string
  unixMilli: number
  tag?: string
  message: string
}

export interface RuntimeLogsResponse {
  logs: RuntimeLogEntry[]
  nextCursor?: string
}

export interface SystemLogDetail {
  requestId: string
  summary: SystemLogSummary
  trafficLogs: TrafficLog[]
  requestLogs?: RequestLogEntry[]
  contexts?: ContextSnapshot[]
  contextChange?: ContextChange
  toolLinks?: ToolLinks
}

/** 一个存在日志记录的本地日历日（清理入口用）。 */
export interface LogAvailableDate {
  date: string
  count: number
}

export interface LogDatesResponse {
  dates: LogAvailableDate[]
}

/** 手动清理 logs.db 的删除行数统计。 */
export interface PurgeLogsResult {
  deletedAppLogs: number
  deletedTrafficLogs: number
  deletedRequestLogs: number
}

export type ContentSafetyAPIType = 'messages' | 'responses' | 'chat' | 'gemini'
export type BlockedLogType =
  | 'sensitive_word'
  | 'sensitive_info'
  | 'credential'
  | 'dangerous_cmd'
  | 'whitelist'
  | 'dangerous_cmd_ai'
  | 'dangerous_cmd_allow'
  | 'semantic_observe'
  | 'allowlist'
  | 'custom_rule'
  | 'restore_miss'

export interface BlockedLogEntry {
  id: number
  timestamp: string
  apiType: ContentSafetyAPIType
  blockType: BlockedLogType
  ruleName?: string
  promptSnippet?: string
  /** 危险命令的脱敏展示文本 */
  command?: string
  /** 原始完整命令及执行上下文的身份，名单操作使用此字段 */
  commandHash?: string
  channelName?: string
  model?: string
  requestId?: string
  conversationId?: string
  createdAt: string
  aiReview?: CommandAIReview
}

export interface CommandAIReview {
  requestId: string
  model?: string
  channelName?: string
  verdict: 'allow' | 'block' | 'error'
  reason: string
  durationMs: number
  reused?: boolean
}

export interface BlockedLogGroup {
  key: string
  conversationId?: string
  latestTimestamp: string
  count: number
  apiTypes?: ContentSafetyAPIType[]
  blockTypes?: BlockedLogType[]
  latestLog?: BlockedLogEntry
}

export interface BlockedLogsResponse {
  logs?: BlockedLogEntry[]
  groups?: BlockedLogGroup[]
  total: number
  totalGroups?: number
  page: number
  pageSize: number
}

export interface BlockedLogFilters {
  apiType?: ContentSafetyAPIType | ''
  blockType?: BlockedLogType | ''
  /** 类别筛选（dangerous_cmd 命中三种危险命令事件），与 blockType 互斥 */
  category?: 'dangerous_cmd' | ''
  /** 仅显示当前活跃会话（发送中或近 30 分钟有请求）的记录 */
  activeOnly?: boolean
  from?: string
  to?: string
  page?: number
  pageSize?: number
  groupBy?: 'conversation'
  groupKey?: string
}

export interface BlockedLogGroupEntriesResponse {
  logs: BlockedLogEntry[]
  total: number
  page: number
  pageSize: number
}
