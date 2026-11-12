// 调用摘要与尝试指标独立缓存；展开尝试不触发正文下载。
import { ref, shallowRef } from 'vue'
import { defineStore } from 'pinia'
import { api } from '@/services/api'
import type { LogCallAttempts, LogCallFilters, LogCallSummary } from '@/types/logs'
import { summaryVersion } from './logs'

export const callVersion = (call: LogCallSummary) => JSON.stringify([summaryVersion(call.summary), call.attemptCount, call.attemptsVersion])
const MAX_CALLS = 200
const MAX_ATTEMPT_BYTES = 8 * 1024 * 1024

export const useLogCallsStore = defineStore('logCalls', () => {
  const calls = shallowRef<LogCallSummary[]>([])
  const nextCursor = ref('')
  const capped = ref(false)
  const loading = ref(false)
  const error = ref('')
  const attempts = shallowRef<Record<string, LogCallAttempts>>({})
  const attemptErrors = ref<Record<string, string>>({})
  const attemptLoading = ref<Record<string, boolean>>({})
  const versions = new Map<string, string>()
  const sizes = new Map<string, number>()
  const pending = new Map<string, { version: string; promise: Promise<void> }>()
  let revision = 0
  let lifetime = 0
  let filterKey = ''

  async function load(filters: LogCallFilters, more = false) {
    const key = JSON.stringify(filters)
    if (key !== filterKey) { reset(); filterKey = key; more = false }
    if (more && (!nextCursor.value || capped.value)) return
    const current = ++revision
    loading.value = true
    error.value = ''
    try {
      const page = await api.getLogCalls({ ...filters, cursor: more ? nextCursor.value : undefined, limit: 50 })
      if (current !== revision) return
      const merged = new Map((more ? calls.value : []).map(call => [call.summary.requestId, call]))
      page.calls.forEach(call => merged.set(call.summary.requestId, call))
      calls.value = Array.from(merged.values()).sort((a, b) => (b.summary.startedAt || b.summary.unixMilli) - (a.summary.startedAt || a.summary.unixMilli) || b.summary.requestId.localeCompare(a.summary.requestId)).slice(0, MAX_CALLS)
      capped.value = calls.value.length >= MAX_CALLS && Boolean(page.nextCursor)
      nextCursor.value = page.nextCursor || ''
      for (const id of Object.keys(attemptErrors.value)) if (!merged.has(id)) delete attemptErrors.value[id]
    } catch (failure) {
      if (current === revision) error.value = failure instanceof Error ? failure.message : '读取模型调用失败'
    } finally { if (current === revision) loading.value = false }
  }

  async function loadAttempts(call: LogCallSummary) {
    const id = call.summary.requestId
    const version = callVersion(call)
    if (attempts.value[id] && versions.get(id) === version) return
    const running = pending.get(id)
    if (running?.version === version) return running.promise
    const epoch = lifetime
    attemptLoading.value[id] = true
    delete attemptErrors.value[id]
    const promise: Promise<void> = api.getLogCallAttempts(id).then(result => {
      if (epoch !== lifetime || pending.get(id)?.promise !== promise) return
      const bytes = 2 * JSON.stringify(result).length
      if (bytes > MAX_ATTEMPT_BYTES) throw new Error('单次调用的尝试指标超过 8 MiB 缓存预算，请使用 CLI 查询。')
      sizes.delete(id)
      sizes.set(id, bytes)
      const next = { ...attempts.value, [id]: result }
      let total = Array.from(sizes.values()).reduce((sum, size) => sum + size, 0)
      for (const [key, size] of sizes) {
        if (total <= MAX_ATTEMPT_BYTES && sizes.size <= 20) break
        if (key === id) continue
        delete next[key]
        versions.delete(key)
        sizes.delete(key)
        total -= size
      }
      attempts.value = next
      versions.set(id, version)
    }).catch((failure: unknown) => {
      if (epoch === lifetime && pending.get(id)?.promise === promise) attemptErrors.value[id] = failure instanceof Error ? failure.message : '读取调用尝试失败'
    }).finally(() => {
      if (epoch === lifetime && pending.get(id)?.promise === promise) { pending.delete(id); delete attemptLoading.value[id] }
    })
    pending.set(id, { version, promise })
    return promise
  }

  function reset() {
    revision++
    lifetime++
    filterKey = ''
    calls.value = []
    nextCursor.value = ''
    capped.value = false
    loading.value = false
    error.value = ''
    attempts.value = {}
    attemptErrors.value = {}
    attemptLoading.value = {}
    pending.clear()
    versions.clear()
    sizes.clear()
  }

  return { calls, nextCursor, capped, loading, error, attempts, attemptErrors, attemptLoading, load, loadAttempts, reset }
})
