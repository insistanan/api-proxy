import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { api } from '@/api'
import { useLogsStore } from '@/features/observability/stores/logs'
import type { LogStorageStatus, SystemLogDetail, SystemLogSummary } from '@/types/logs'

vi.mock('@/shared/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))
const summary = (id: string, version = 1, conversationId = 'one'): SystemLogSummary => ({ requestId: id, conversationId, timestamp: '', unixMilli: version, apiType: 'responses', phases: [], stream: false, truncated: false, status: 'completed' })
const detail = (id: string, version = 1, conversationId = 'one'): SystemLogDetail => ({ requestId: id, summary: summary(id, version, conversationId), trafficLogs: [] })
const freshStore = () => {
  vi.clearAllMocks()
  setActivePinia(createPinia())
  return useLogsStore()
}
beforeEach(() => setActivePinia(createPinia()))
afterEach(() => vi.restoreAllMocks())

describe('日志状态与会话缓存', () => {
  it('合并验证详情缓存失效、并发竞态、预算保护和可重试状态', async () => {
    {
      const status: LogStorageStatus = { policy: { runtimeDays: 2, callDays: 7, bodyDays: 1, budgetMB: 0 }, usedBytes: 4096, freeBytes: 0, databaseBytes: 4096, walBytes: 0, bodyCapturePaused: false, maintenance: { deletedRows: 0, expiredBodies: 0 }, budgetEvents: [] }
      const storage = vi.spyOn(api, 'getLogStorageStatus').mockResolvedValue(status)
      const fetch = vi.spyOn(api, 'getSystemLog').mockResolvedValue(detail('one'))
      const store = freshStore()
      await store.loadStorageStatus()
      await store.loadDetail('one', summary('one'))
      storage.mockResolvedValue({ ...status, maintenance: { ...status.maintenance, expiredBodies: 1 } })
      await store.loadStorageStatus()
      await store.loadDetail('one', summary('one'))
      expect(fetch).toHaveBeenCalledTimes(2)
    }

    {
      let resolve!: (_detail: SystemLogDetail) => void
      const old = new Promise<SystemLogDetail>(done => { resolve = done })
      const expired: SystemLogDetail = { ...detail('one'), trafficLogs: [{ requestId: 'one', timestamp: '', phase: 'client_request', bodyState: 'expired' }] }
      vi.spyOn(api, 'getSystemLog').mockReturnValueOnce(old).mockResolvedValueOnce(expired)
      const store = freshStore()
      const pending = store.loadDetail('one')
      await store.loadDetail('one', undefined, true)
      resolve(detail('one'))
      await pending
      expect(store.details.one?.trafficLogs[0]?.bodyState).toBe('expired')
    }

    {
      const fetch = vi.spyOn(api, 'getLogStorageStatus').mockRejectedValue(new Error('日志数据库不可读'))
      const store = freshStore()
      await store.loadStorageStatus()
      expect(store.storageError).toContain('日志数据库不可读')
      expect(store.storageLoading).toBe(false)
      expect(fetch).toHaveBeenCalledTimes(1)
    }

    {
      const fetch = vi.spyOn(api, 'getSystemLog').mockResolvedValue(detail('one'))
      const store = freshStore()
      await Promise.all([store.loadDetail('one', summary('one')), store.loadDetail('one', summary('one'))])
      await store.loadDetail('one', summary('one'))
      expect(fetch).toHaveBeenCalledTimes(1)
      fetch.mockResolvedValue(detail('one', 2))
      await store.loadDetail('one', summary('one', 2))
      expect(fetch).toHaveBeenCalledTimes(2)
    }

    {
      let resolve!: (_detail: SystemLogDetail) => void
      const old = new Promise<SystemLogDetail>(done => { resolve = done })
      vi.spyOn(api, 'getSystemLog').mockReturnValueOnce(old).mockResolvedValueOnce(detail('one', 2))
      const store = freshStore()
      const pending = store.loadDetail('one', summary('one'))
      await store.loadDetail('one', summary('one', 2))
      resolve(detail('one'))
      await pending
      expect(store.details.one!.summary.unixMilli).toBe(2)
    }

    {
      let resolve!: (_detail: SystemLogDetail) => void
      const old = new Promise<SystemLogDetail>(done => { resolve = done })
      const updated = { ...detail('one'), toolLinks: { observations: [], limited: false, notice: '已包含后续结果' } }
      const fetch = vi.spyOn(api, 'getSystemLog').mockReturnValueOnce(old).mockResolvedValueOnce(updated)
      const store = freshStore()
      store.selectedKey = 'responses:one'
      store.selectedConversation = { key: 'responses:one', latest: summary('one'), requestCount: 1 }
      const pending = store.loadDetail('one', summary('one'))
      store.selectedConversation = { key: 'responses:one', latest: summary('two'), requestCount: 2 }
      await store.loadDetail('one', summary('one'))
      resolve(detail('one'))
      await pending
      await store.loadDetail('one', summary('one'))
      expect(fetch).toHaveBeenCalledTimes(2)
      expect(store.details.one!.toolLinks?.notice).toBe('已包含后续结果')
    }

    {
      let resolve!: (_detail: SystemLogDetail) => void
      vi.spyOn(api, 'getSystemLog').mockImplementation(() => new Promise(done => { resolve = done }))
      const store = freshStore()
      const pending = store.loadDetail('one')
      store.reset()
      resolve(detail('one'))
      await pending
      expect(store.details).toEqual({})
    }

    {
      const fetch = vi.spyOn(api, 'getSystemLog').mockResolvedValue(detail('one'))
      const store = freshStore()
      await store.loadDetail('one', summary('one'))
      const updated = { ...detail('one'), summary: { ...summary('one'), endedAt: 1, durationMs: 0 } }
      fetch.mockResolvedValue(updated)
      await store.loadDetail('one', updated.summary)
      expect(fetch).toHaveBeenCalledTimes(2)
      expect(store.details.one!.summary.durationMs).toBe(0)
    }

    {
      const oversized: SystemLogDetail = { ...detail('one'), requestLogs: [{ requestId: 'one', attemptId: 'a', timestamp: '', apiType: 'messages', status: 'failed', success: false, durationMs: 1, channelIndex: 0, baseUrl: '', keyMask: '', retried: false, stream: false, errorMessage: 'x'.repeat(8 * 1024 * 1024) }] }
      vi.spyOn(api, 'getSystemLog').mockResolvedValue(oversized)
      const store = freshStore()
      await expect(store.loadDetail('one')).rejects.toThrow('16 MiB')
      expect(store.details.one).toBeUndefined()
    }

    {
      const oversized = { ...detail('one'), trafficLogs: [{ requestId: 'one', timestamp: '', phase: 'client_request', body: 'x'.repeat(8 * 1024 * 1024) }] }
      const fetch = vi.spyOn(api, 'getSystemLog').mockResolvedValue(oversized)
      const store = freshStore()
      await expect(store.loadDetail('one')).rejects.toThrow('16 MiB')
      expect(store.details.one).toBeUndefined()
      expect(store.detailErrors.one).toContain('16 MiB')
      fetch.mockResolvedValue(detail('one'))
      await store.loadDetail('one')
      expect(store.detailErrors.one).toBeUndefined()
    }
  })

  it('合并验证筛选竞态、删除清理、会话更新和旧请求定位', async () => {
    {
      let resolve!: (_page: { conversations: []; total: number }) => void
      const old = new Promise<{ conversations: []; total: number }>(done => { resolve = done })
      const fetch = vi.spyOn(api, 'getLogConversations').mockReturnValueOnce(old).mockResolvedValueOnce({ conversations: [{ key: 'responses:one', latest: summary('new'), requestCount: 3 }], total: 1 })
      const store = freshStore()
      store.searchFilter = '旧任务'
      const pending = store.loadConversations()
      store.searchFilter = '搜索_100%'
      store.statusFilter = 'failed'
      store.fromFilter = '2026-11-01T00:00:00Z'
      store.toFilter = '2026-11-12T00:00:00Z'
      await store.loadConversations()
      resolve({ conversations: [], total: 0 })
      await pending
      expect(fetch).toHaveBeenLastCalledWith(expect.objectContaining({ q: '搜索_100%', status: 'failed', from: store.fromFilter, to: store.toFilter }))
      expect(store.conversations[0]?.latest.requestId).toBe('new')
      expect(store.totalConversations).toBe(1)
    }

    {
      vi.spyOn(api, 'getSystemLog').mockResolvedValue(detail('old'))
      vi.spyOn(api, 'getLogConversationRequests').mockResolvedValue({ logs: [summary('old')] })
      vi.spyOn(api, 'getLogConversations').mockResolvedValue({ conversations: [] })
      const store = freshStore()
      await store.locateRequest('old')
      await store.refresh()
      expect(store.selectedKey).toBe('')
      expect(store.requests).toEqual([])
      expect(store.details).toEqual({})
    }

    {
      vi.spyOn(api, 'getSystemLog').mockResolvedValue(detail('old'))
      const rounds = vi.spyOn(api, 'getLogConversationRequests').mockResolvedValue({ logs: [summary('old')] })
      vi.spyOn(api, 'getLogConversations').mockResolvedValue({ conversations: [{ key: 'responses:two', latest: summary('other', 2, 'two'), requestCount: 1 }] })
      const store = freshStore()
      await store.locateRequest('old')
      rounds.mockResolvedValue({ logs: [] })
      await store.refresh()
      expect(store.selectedKey).toBe('')
      expect(store.conversations.map(group => group.key)).toEqual(['responses:two'])
      expect(store.details.old).toBeUndefined()
    }

    {
      const fetch = vi.spyOn(api, 'getLogConversations').mockResolvedValue({ conversations: [{ key: 'responses:one', latest: summary('first'), requestCount: 1 }] })
      vi.spyOn(api, 'getLogConversationRequests').mockResolvedValue({ logs: [summary('first')] })
      const store = freshStore()
      await store.loadConversations()
      await store.selectConversation(store.conversations[0]!)
      fetch.mockResolvedValue({ conversations: [{ key: 'responses:one', latest: summary('second', 2), requestCount: 2 }] })
      vi.spyOn(api, 'getLogConversationRequests').mockResolvedValue({ logs: [summary('second', 2)] })
      await store.refresh()
      expect(store.conversations).toHaveLength(1)
      expect(store.selectedKey).toBe('responses:one')
      expect(store.requests.map(item => item.requestId)).toEqual(['first', 'second'])
    }

    {
      vi.spyOn(api, 'getSystemLog').mockResolvedValue(detail('old'))
      const fetch = vi.spyOn(api, 'getLogConversationRequests').mockImplementation(params => Promise.resolve({ logs: [summary(params.anchor || 'latest', params.anchor ? 1 : 300)] }))
      const store = freshStore()
      await store.locateRequest('old')
      expect(fetch).toHaveBeenLastCalledWith(expect.objectContaining({ key: 'responses:one', anchor: 'old' }))
      expect(store.requests[0]!.requestId).toBe('old')
      expect(store.anchored).toBe(true)
    }

    {
      let resolve!: (_detail: SystemLogDetail) => void
      const old = new Promise<SystemLogDetail>(done => { resolve = done })
      vi.spyOn(api, 'getSystemLog').mockReturnValueOnce(old).mockResolvedValueOnce(detail('new', 2, 'two'))
      vi.spyOn(api, 'getLogConversationRequests').mockResolvedValue({ logs: [summary('new', 2, 'two')] })
      const store = freshStore()
      const pending = store.locateRequest('old')
      await store.locateRequest('new')
      resolve(detail('old'))
      await pending
      expect(store.selectedKey).toBe('responses:two')
    }
  })
})
