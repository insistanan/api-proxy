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
}

export function groupLogConversations(entries: SystemLogSummary[]): LogConversationGroup[] {
  const groups = new Map<string, LogConversationGroup>()
  for (const entry of entries) {
    const key = entry.conversationId ? `${entry.apiType?.toLowerCase()}:${entry.conversationId}` : `request:${entry.requestId}`
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
    ['upstream_error', 'error'], ['upstream_headers', 'headers']
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
    if (item && typeof item === 'object' && !Array.isArray(item)) {
      return Object.fromEntries(Object.entries(item).sort(([a], [b]) => a.localeCompare(b)))
    }
    return item
  })
}

function splitRequest(log?: TrafficLog) {
  if (!log?.body || log.truncated) return undefined
  let data: Record<string, unknown>
  try { data = JSON.parse(log.body) as Record<string, unknown> } catch { return undefined }
  if (!data || typeof data !== 'object' || Array.isArray(data)) return undefined
  const field = ['messages', 'input', 'contents'].find(key => Array.isArray(data[key]))
  if (!field) return undefined
  const top = { ...data }
  const messages = top[field] as unknown[]
  delete top[field]
  return { top, messages, field }
}

export function getRequestDelta(previous: TrafficLog | undefined, current: TrafficLog) {
  const next = splitRequest(current)
  if (!next) return { top: '', removedTopFields: [] as string[], body: current.body || '', notice: current.truncated ? '正文已截断，无法可靠比较历史' : '正文无法按消息拆分，展示原始记录', sharedCount: 0, repeated: false }
  const prior = splitRequest(previous)
  let sharedCount = 0
  if (prior?.field === next.field) {
    while (sharedCount < prior.messages.length && sharedCount < next.messages.length && stableJSON(prior.messages[sharedCount]) === stableJSON(next.messages[sharedCount])) sharedCount++
  }
  const sameTop = prior !== undefined && stableJSON(prior.top) === stableJSON(next.top)
  const removedTopFields = prior ? Object.keys(prior.top).filter(key => !Object.prototype.hasOwnProperty.call(next.top, key)) : []
  const changedTop = prior ? Object.fromEntries(Object.entries(next.top).filter(([key, value]) => stableJSON(value) !== stableJSON(prior.top[key]))) : next.top
  const repeated = Boolean(prior && sameTop && sharedCount === prior.messages.length && sharedCount === next.messages.length)
  const historyChanged = Boolean(prior && sharedCount < prior.messages.length)
  return {
    top: sameTop ? '' : JSON.stringify(changedTop, null, 2),
    removedTopFields,
    body: JSON.stringify({ [next.field]: next.messages.slice(sharedCount) }),
    notice: repeated ? '客户端重发相同请求，没有新增输入' : historyChanged ? `历史从第 ${sharedCount + 1} 条起发生变化，展示实际替换内容` : prior ? `相同的 ${sharedCount} 条历史已折叠` : '当前加载范围的首轮，展示完整配置与输入',
    sharedCount,
    repeated
  }
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
