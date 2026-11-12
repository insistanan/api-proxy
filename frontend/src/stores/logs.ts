// 日志状态唯一来源：稳定会话选择、摘要分页和有界详情缓存；正文不持久化到浏览器。
import { computed, ref, shallowRef } from 'vue'
import { defineStore } from 'pinia'
import { api } from '@/services/api'
import type { LogAPIType, LogConversationSummary, SystemLogDetail, SystemLogSummary } from '@/types/logs'
import { logConversationKey } from '@/utils/logConversation'

const MAX_CACHE_BYTES = 16 * 1024 * 1024
const MAX_CACHE_ENTRIES = 40

export function summaryVersion(summary: SystemLogSummary): string {
  return JSON.stringify([summary.unixMilli, summary.status, summary.statusCode, summary.phases, summary.missingPhases, summary.stream, summary.truncated, summary.channelName, summary.model, summary.client, summary.apiType, summary.conversationId, summary.requestHash, summary.errorType, summary.errorMessage, summary.promptPreview, summary.clientFamily, summary.identitySource, summary.context])
}

export function conversationVersion(group: LogConversationSummary | null): string {
  return group ? JSON.stringify([group.latest.requestId, group.requestCount, summaryVersion(group.latest)]) : ''
}

export const useLogsStore = defineStore('logs', () => {
  // ===== 状态 =====
  const typeFilter = ref<LogAPIType | ''>('')
  const searchFilter = ref('')
  const statusFilter = ref('')
  const fromFilter = ref('')
  const toFilter = ref('')
  const totalConversations = ref<number | null>(null)
  const conversations = shallowRef<LogConversationSummary[]>([])
  const nextConversationCursor = ref('')
  const selectedKey = ref('')
  const selectedConversation = shallowRef<LogConversationSummary | null>(null)
  const requests = shallowRef<SystemLogSummary[]>([])
  const nextRequestCursor = ref('')
  const anchored = ref(false)
  const listLoading = ref(false)
  const roundsLoading = ref(false)
  const listError = ref('')
  const roundsError = ref('')
  const details = shallowRef<Record<string, SystemLogDetail>>({})
  const detailErrors = ref<Record<string, string>>({})
  // 非响应式的请求句柄与缓存记账，不承载 UI 状态。
  const pending = new Map<string, { version: string; relatedVersion: string; promise: Promise<SystemLogDetail> }>()
  const sizes = new Map<string, number>()
  const relatedVersions = new Map<string, string>()
  let listRevision = 0
  let roundsRevision = 0
  let selectionRevision = 0
  let lifetime = 0

  // ===== 计算属性 =====
  const active = computed(() => conversations.value.some(group => ['processing', 'retrying'].includes(group.latest.status || '')))

  function cacheDetail(detail: SystemLogDetail) {
    const id = detail.requestId
    // 按 UTF-16 字符保守估算正文与头部占用，不为计数重新序列化整个详情。
    const bytes = 2048 + 2 * (JSON.stringify(detail.contexts || []).length + JSON.stringify(detail.toolLinks || {}).length) + detail.trafficLogs.reduce((sum, log) => sum + 2 * ((log.body?.length || 0) + (log.headers?.length || 0)), 0)
    if (bytes > MAX_CACHE_BYTES) throw new Error('单次请求详情超过 16 MiB 浏览器缓存预算，请使用 CLI 查询。')
    sizes.delete(id)
    sizes.set(id, bytes)
    const next = { ...details.value, [id]: detail }
    let total = Array.from(sizes.values()).reduce((sum, size) => sum + size, 0)
    for (const [key, size] of sizes) {
      if (total <= MAX_CACHE_BYTES && sizes.size <= MAX_CACHE_ENTRIES) break
      if (key === id) continue
      delete next[key]
      sizes.delete(key)
      relatedVersions.delete(key)
      total -= size
    }
    details.value = next
  }

  async function loadDetail(id: string, summary?: SystemLogSummary, force = false): Promise<SystemLogDetail> {
    const cached = details.value[id]
    const version = summary ? summaryVersion(summary) : ''
    const knownSummary = summary || cached?.summary
    const relatedVersion = knownSummary && logConversationKey(knownSummary) === selectedKey.value ? conversationVersion(selectedConversation.value) : ''
    if (!force && cached && relatedVersions.get(id) === relatedVersion && (summary ? summaryVersion(cached.summary) === version : !['processing', 'retrying'].includes(cached.summary.status || ''))) {
      return cached
    }
    const inFlight = pending.get(id)
    if (inFlight && inFlight.relatedVersion === relatedVersion && (!version || inFlight.version === version)) return inFlight.promise
    const epoch = lifetime
    const promise: Promise<SystemLogDetail> = api.getSystemLog(id).then(detail => {
      if (epoch === lifetime && pending.get(id)?.promise === promise) {
        cacheDetail(detail)
        relatedVersions.set(id, relatedVersion)
        delete detailErrors.value[id]
      }
      return detail
    }).catch((error: unknown) => {
      if (epoch === lifetime && pending.get(id)?.promise === promise) detailErrors.value[id] = error instanceof Error ? error.message : '读取请求详情失败'
      throw error
    }).finally(() => { if (pending.get(id)?.promise === promise) pending.delete(id) })
    pending.set(id, { version, relatedVersion, promise })
    return promise
  }

  async function loadConversations(more = false) {
    const revision = ++listRevision
    listLoading.value = true
    listError.value = ''
    try {
      const page = await api.getLogConversations({ type: typeFilter.value, q: searchFilter.value, status: statusFilter.value, from: fromFilter.value, to: toFilter.value, cursor: more ? nextConversationCursor.value : undefined, limit: 50 })
      if (revision !== listRevision) return
      // 首屏明确为空时，清掉已删除日志的选择与正文，避免继续钉住旧会话。
      if (!more && page.conversations.length === 0 && !page.nextCursor) { reset(); totalConversations.value = page.total ?? 0; return }
      if (!more || page.conversations.length) totalConversations.value = page.total ?? null
      const merged = new Map((more ? conversations.value : []).map(group => [group.key, group]))
      page.conversations.forEach(group => merged.set(group.key, group))
      if (!more && !searchFilter.value && !statusFilter.value && !fromFilter.value && !toFilter.value && selectedConversation.value && !merged.has(selectedKey.value)) merged.set(selectedKey.value, selectedConversation.value)
      conversations.value = Array.from(merged.values()).sort((a, b) => b.latest.unixMilli - a.latest.unixMilli || a.key.localeCompare(b.key))
      nextConversationCursor.value = page.nextCursor || ''
      selectedConversation.value = merged.get(selectedKey.value) || selectedConversation.value
    } catch (error) {
      if (revision === listRevision) listError.value = error instanceof Error ? error.message : '加载会话列表失败'
    } finally { if (revision === listRevision) listLoading.value = false }
  }

  async function loadRounds(options: { more?: boolean; anchor?: string; latest?: boolean } = {}) {
    const key = selectedKey.value
    if (!key) return
    const revision = ++roundsRevision
    const initial = requests.value.length === 0
    roundsLoading.value = true
    roundsError.value = ''
    try {
      const page = await api.getLogConversationRequests({ key, before: options.more ? nextRequestCursor.value : undefined, anchor: options.anchor, limit: 20 })
      if (revision !== roundsRevision || key !== selectedKey.value) return
      if (!options.more && !options.anchor && page.logs.length === 0) {
        selectionRevision++
        conversations.value = conversations.value.filter(group => group.key !== key)
        selectedKey.value = ''
        selectedConversation.value = null
        requests.value = []
        nextRequestCursor.value = ''
        anchored.value = false
        const remaining = { ...details.value }
        for (const [id, detail] of Object.entries(remaining)) {
          if (logConversationKey(detail.summary) !== key) continue
          delete remaining[id]
          sizes.delete(id)
          relatedVersions.delete(id)
          pending.delete(id)
          delete detailErrors.value[id]
        }
        details.value = remaining
        return
      }
      const replace = Boolean(options.anchor || options.latest)
      const merged = new Map((replace ? [] : requests.value).map(summary => [summary.requestId, summary]))
      page.logs.forEach(summary => merged.set(summary.requestId, summary))
      requests.value = Array.from(merged.values()).sort((a, b) => (a.startedAt || a.unixMilli) - (b.startedAt || b.unixMilli) || a.requestId.localeCompare(b.requestId))
      if (options.more || replace || initial) nextRequestCursor.value = page.nextCursor || ''
      if (options.anchor) anchored.value = true
      if (options.latest) anchored.value = false
    } catch (error) {
      if (revision === roundsRevision) roundsError.value = error instanceof Error ? error.message : '加载会话调用失败'
    } finally { if (revision === roundsRevision) roundsLoading.value = false }
  }

  async function chooseConversation(group: LogConversationSummary) {
    if (selectedKey.value === group.key) return
    roundsRevision++
    selectedKey.value = group.key
    selectedConversation.value = group
    requests.value = []
    nextRequestCursor.value = ''
    anchored.value = false
    await loadRounds({ latest: true })
  }

  async function selectConversation(group: LogConversationSummary) {
    selectionRevision++
    await chooseConversation(group)
  }

  async function locateRequest(id: string) {
    const epoch = lifetime
    const selection = ++selectionRevision
    const detail = await loadDetail(id)
    if (epoch !== lifetime || selection !== selectionRevision) return
    const key = logConversationKey(detail.summary)
    const group = conversations.value.find(item => item.key === key) || { key, latest: detail.summary, requestCount: 1 }
    if (key !== selectedKey.value) await chooseConversation(group)
    if (epoch !== lifetime || selection !== selectionRevision || key !== selectedKey.value) return
    if (!requests.value.some(summary => summary.requestId === id)) await loadRounds({ anchor: id })
  }

  async function refresh() {
    await loadConversations()
    if (selectedKey.value && !anchored.value) await loadRounds()
  }

  function reset() {
    lifetime++
    selectionRevision++
    listRevision++
    roundsRevision++
    pending.clear()
    sizes.clear()
    relatedVersions.clear()
    details.value = {}
    detailErrors.value = {}
    conversations.value = []
    requests.value = []
    selectedKey.value = ''
    selectedConversation.value = null
    nextConversationCursor.value = ''
    totalConversations.value = null
    nextRequestCursor.value = ''
    listLoading.value = false
    roundsLoading.value = false
    listError.value = ''
    roundsError.value = ''
    anchored.value = false
  }

  return { typeFilter, searchFilter, statusFilter, fromFilter, toFilter, totalConversations, conversations, nextConversationCursor, selectedKey, selectedConversation, requests, nextRequestCursor, anchored, listLoading, roundsLoading, listError, roundsError, details, detailErrors, active, loadDetail, loadConversations, loadRounds, selectConversation, locateRequest, refresh, reset }
})
