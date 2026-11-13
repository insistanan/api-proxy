// 会话分组与增量展示只比较实际记录的请求，不把上游响应当作下一轮的输入历史。
import type { SystemLogSummary, TrafficLog } from '@/types/logs'

export interface LogConversationGroup {
  key: string
  latest: SystemLogSummary
  requests: SystemLogSummary[]
}

export interface LogUpstreamAttempt {
  key: string
  label: string
  request?: TrafficLog
  response?: TrafficLog
  error?: TrafficLog
  headers?: TrafficLog
  admission?: TrafficLog
}

export function logConversationKey(entry: SystemLogSummary): string {
  return entry.conversationId ? `${(entry.apiType || '').toLowerCase()}:${entry.conversationId}` : `request:${entry.requestId}`
}

export function requestInputLog(logs: TrafficLog[]): TrafficLog | undefined {
  return logs.find(log => log.phase === 'client_input') || logs.find(log => log.phase === 'client_request')
}

export function groupLogConversations(entries: SystemLogSummary[]): LogConversationGroup[] {
  const groups = new Map<string, LogConversationGroup>()
  for (const entry of entries) {
    const key = logConversationKey(entry)
    const group = groups.get(key) || { key, latest: entry, requests: [] }
    group.requests.push(entry)
    if (entry.unixMilli > group.latest.unixMilli) group.latest = entry
    groups.set(key, group)
  }
  return Array.from(groups.values()).map(group => ({
    ...group,
    requests: group.requests.sort((a, b) => (a.startedAt || a.unixMilli) - (b.startedAt || b.unixMilli))
  })).sort((a, b) => b.latest.unixMilli - a.latest.unixMilli)
}

export function getLogAttempts(logs: TrafficLog[]): LogUpstreamAttempt[] {
  const phases = new Map([
    ['upstream_request', 'request'], ['upstream_response', 'response'],
    ['upstream_error', 'error'], ['upstream_headers', 'headers'],
    ['upstream_admission', 'admission']
  ] as const)
  const relevant = logs.filter(log => phases.has(log.phase as 'upstream_request'))
  const legacyRequests = relevant.filter(log => log.phase === 'upstream_request' && !log.attemptId)
  const legacyResponses = relevant.filter(log => log.phase === 'upstream_response' && !log.attemptId)
  const singleLegacy = legacyRequests.length === 1 && legacyResponses.length <= 1 && relevant.every(log => !log.attemptId)
  const groups = new Map<string, LogUpstreamAttempt>()
  for (const entry of relevant) {
    const field = phases.get(entry.phase as 'upstream_request')!
    const key = entry.attemptId || (singleLegacy ? 'legacy-single' : `legacy-${entry.id || entry.timestamp}-${entry.phase}`)
    const group = groups.get(key) || { key, label: '' }
    group[field] = entry
    groups.set(key, group)
  }
  return Array.from(groups.values()).map((group, index) => ({
    ...group,
    label: group.key.startsWith('legacy-') && !singleLegacy ? '历史记录（尝试关联未知）' : `尝试 ${index + 1}`
  }))
}

function stableJSON(value: unknown): string {
  return JSON.stringify(value, (_key, item: unknown) => {
    if (item && typeof item === 'object' && !Array.isArray(item)) return Object.fromEntries(Object.entries(item).sort(([a], [b]) => a.localeCompare(b)))
    return item
  })
}

interface RequestParts {
  top: Record<string, unknown>
  topJSON: string
  topValues: Record<string, string>
  messages: unknown[]
  messageJSON: string[]
  hashes: string[]
  field: string
  offset: number
  total: number
  topHash: string
  requestHash: string
  notice: string
}
const requestPartsCache = new WeakMap<TrafficLog, { body: string; truncated: boolean; parts?: RequestParts }>()

function splitRequest(log?: TrafficLog): RequestParts | undefined {
  if (!log?.body || log.truncated) return undefined
  const cached = requestPartsCache.get(log)
  if (cached?.body === log.body && cached.truncated === Boolean(log.truncated)) return cached.parts
  let parts: RequestParts | undefined
  try {
    const data = JSON.parse(log.body) as Record<string, unknown>
    if (data && typeof data === 'object' && !Array.isArray(data)) {
      const field = ['messages', 'input', 'contents'].find(key => Array.isArray(data[key]) || (key === 'input' && typeof data[key] === 'string'))
      if (field) {
        const top = { ...data }
        const messages = typeof top[field] === 'string' ? [{ role: 'user', content: top[field] }] : top[field] as unknown[]
        delete top[field]
        const meta = log.phase === 'client_input' && top._log && typeof top._log === 'object' ? top._log as Record<string, unknown> : undefined
        if (meta) delete top._log
        const offset = typeof meta?.messageOffset === 'number' && Number.isSafeInteger(meta.messageOffset) && meta.messageOffset >= 0 ? meta.messageOffset : 0
        const hashes = Array.isArray(meta?.messageHashes) && meta.messageHashes.length === messages.length && meta.messageHashes.every(hash => typeof hash === 'string' && /^[a-f0-9]{64}$/.test(hash)) ? meta.messageHashes as string[] : []
        parts = {
          top, topJSON: stableJSON(top),
          topValues: Object.fromEntries(Object.entries(top).map(([key, value]) => [key, stableJSON(value)])),
          messages, messageJSON: messages.map(stableJSON), hashes, field, offset,
          total: typeof meta?.originalMessages === 'number' ? meta.originalMessages : messages.length,
          topHash: typeof meta?.topHash === 'string' ? meta.topHash : '',
          requestHash: typeof meta?.requestHash === 'string' ? meta.requestHash : '',
          notice: typeof meta?.notice === 'string' ? meta.notice : ''
        }
      }
    }
  } catch { parts = undefined }
  requestPartsCache.set(log, { body: log.body, truncated: Boolean(log.truncated), parts })
  return parts
}

export function getRequestDelta(previous: TrafficLog | undefined, current: TrafficLog) {
  const next = splitRequest(current)
  if (!next) return { top: '', removedTopFields: [] as string[], body: current.body || '', notice: current.truncated ? '正文已截断，无法可靠比较历史' : '正文无法按消息拆分，展示原始记录', sharedCount: 0, repeated: false }
  const prior = splitRequest(previous)
  const commonOffset = Math.max(prior?.offset || 0, next.offset)
  let sharedCount = next.offset
  const comparable = prior?.field === next.field && commonOffset <= prior.offset + prior.messages.length
  if (prior && comparable) {
    sharedCount = commonOffset
    while (sharedCount < prior.offset + prior.messages.length && sharedCount < next.offset + next.messages.length) {
      const a = sharedCount - prior.offset
      const b = sharedCount - next.offset
      const equal = prior.hashes.length && next.hashes.length ? prior.hashes[a] === next.hashes[b] : prior.messageJSON[a] === next.messageJSON[b]
      if (!equal) break
      sharedCount++
    }
  }
  const sameTop = Boolean(prior && (prior.topHash && next.topHash ? prior.topHash === next.topHash : prior.topJSON === next.topJSON))
  const removedTopFields = prior && Boolean(prior.notice) === Boolean(next.notice) ? Object.keys(prior.top).filter(key => !Object.prototype.hasOwnProperty.call(next.top, key)) : []
  const changedTop = prior ? Object.fromEntries(Object.entries(next.top).filter(([key]) => next.topValues[key] !== prior.topValues[key])) : next.top
  const snapshot = Boolean(next.notice || prior?.notice)
  const repeated = Boolean(prior && sameTop && (snapshot ? next.requestHash && next.requestHash === prior.requestHash : comparable && sharedCount === prior.total && sharedCount === next.total))
  const historyChanged = Boolean(prior && ((comparable && sharedCount < prior.offset + prior.messages.length) || next.total < prior.total))
  const top = sameTop ? '' : JSON.stringify(Object.keys(changedTop).length || removedTopFields.length ? changedTop : { 提示: '配置发生变化，已保存的摘要未包含具体差异字段' }, null, 2)
  let notice = repeated ? '客户端重发相同请求，没有新增输入' : historyChanged ? `历史从第 ${sharedCount + 1} 条起发生变化，展示实际替换内容` : prior && comparable ? `已折叠 ${Math.max(0, sharedCount - commonOffset)} 条相同历史` : prior ? '上一轮没有可比较的历史，展示已保存输入' : '当前加载范围的首轮，展示已保存配置与输入'
  if (next.notice) notice += `。第 ${next.offset + 1}～${next.total} 条消息的摘要：${next.notice}`
  return { top, removedTopFields, body: JSON.stringify({ [next.field]: next.messages.slice(Math.max(0, sharedCount - next.offset)) }), notice, sharedCount, repeated }
}

export function logStatus(entry: SystemLogSummary): { label: string; color: string } {
  const statuses: Record<string, { label: string; color: string }> = {
    processing: { label: '处理中', color: 'info' }, retrying: { label: '上游重试中', color: 'warning' },
    completed: { label: '已完成', color: 'success' }, cancelled: { label: '已取消', color: 'secondary' },
    interrupted: { label: '未正常收尾', color: 'warning' }, blocked: { label: '已拦截', color: 'warning' }
  }
  if (entry.status && statuses[entry.status]) return statuses[entry.status]!
  if (entry.status === 'failed') {
    if (entry.errorMessage?.includes('timeout awaiting response headers')) return { label: '上游响应头超时', color: 'error' }
    if (entry.errorType === 'network') return { label: '网络请求失败', color: 'error' }
    if (entry.statusCode === 429) return { label: '上游限流', color: 'warning' }
    return { label: '失败', color: 'error' }
  }
  return { label: entry.missingPhases?.length ? '阶段记录不完整' : '结束状态未知', color: 'warning' }
}
