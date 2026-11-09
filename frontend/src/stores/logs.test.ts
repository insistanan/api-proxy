import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { api } from '@/services/api'
import { useLogsStore } from './logs'
import type { SystemLogDetail, SystemLogSummary } from '@/types/logs'

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))
const summary = (id: string, version = 1, conversationId = 'one'): SystemLogSummary => ({ requestId: id, conversationId, timestamp: '', unixMilli: version, apiType: 'responses', phases: [], stream: false, truncated: false, status: 'completed' })
const detail = (id: string, version = 1, conversationId = 'one'): SystemLogDetail => ({ requestId: id, summary: summary(id, version, conversationId), trafficLogs: [] })
beforeEach(() => setActivePinia(createPinia()))
afterEach(() => vi.restoreAllMocks())

describe('日志详情缓存', () => {
  it('相同版本复用并发请求与已完成正文，有变化时重新读取', async () => {
    const fetch = vi.spyOn(api, 'getSystemLog').mockResolvedValue(detail('one'))
    const store = useLogsStore()
    await Promise.all([store.loadDetail('one', summary('one')), store.loadDetail('one', summary('one'))])
    await store.loadDetail('one', summary('one'))
    expect(fetch).toHaveBeenCalledTimes(1)
    fetch.mockResolvedValue(detail('one', 2))
    await store.loadDetail('one', summary('one', 2))
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('旧版本的晚到结果不能覆盖新版本缓存', async () => {
    let resolve!: (_detail: SystemLogDetail) => void
    const old = new Promise<SystemLogDetail>(done => { resolve = done })
    vi.spyOn(api, 'getSystemLog').mockReturnValueOnce(old).mockResolvedValueOnce(detail('one', 2))
    const store = useLogsStore()
    const pending = store.loadDetail('one', summary('one'))
    await store.loadDetail('one', summary('one', 2))
    resolve(detail('one'))
    await pending
    expect(store.details.one!.summary.unixMilli).toBe(2)
  })

  it('重置后晚到请求不能重新填入缓存', async () => {
    let resolve!: (_detail: SystemLogDetail) => void
    vi.spyOn(api, 'getSystemLog').mockImplementation(() => new Promise(done => { resolve = done }))
    const store = useLogsStore()
    const pending = store.loadDetail('one')
    store.reset()
    resolve(detail('one'))
    await pending
    expect(store.details).toEqual({})
  })

  it('相同毫秒的元数据变化也会刷新详情', async () => {
    const fetch = vi.spyOn(api, 'getSystemLog').mockResolvedValue(detail('one'))
    const store = useLogsStore()
    await store.loadDetail('one', summary('one'))
    const updated = { ...detail('one'), summary: { ...summary('one'), model: 'new-model', errorType: 'network' } }
    fetch.mockResolvedValue(updated)
    await store.loadDetail('one', updated.summary)
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(store.details.one!.summary.model).toBe('new-model')
  })

  it('超过正文预算显式报错并保留可重试状态', async () => {
    const oversized = { ...detail('one'), trafficLogs: [{ requestId: 'one', timestamp: '', phase: 'client_request', body: 'x'.repeat(8 * 1024 * 1024) }] }
    const fetch = vi.spyOn(api, 'getSystemLog').mockResolvedValue(oversized)
    const store = useLogsStore()
    await expect(store.loadDetail('one')).rejects.toThrow('16 MiB')
    expect(store.details.one).toBeUndefined()
    expect(store.detailErrors.one).toContain('16 MiB')
    fetch.mockResolvedValue(detail('one'))
    await store.loadDetail('one')
    expect(store.detailErrors.one).toBeUndefined()
  })
})

describe('会话选择与分页', () => {
  it('首屏为空时清除已删除的会话、轮次及正文', async () => {
    vi.spyOn(api, 'getSystemLog').mockResolvedValue(detail('old'))
    vi.spyOn(api, 'getLogConversationRequests').mockResolvedValue({ logs: [summary('old')] })
    vi.spyOn(api, 'getLogConversations').mockResolvedValue({ conversations: [] })
    const store = useLogsStore()
    await store.locateRequest('old')
    await store.refresh()
    expect(store.selectedKey).toBe('')
    expect(store.requests).toEqual([])
    expect(store.details).toEqual({})
  })

  it('已选会话被清理时移除旧会话，保留其他会话', async () => {
    vi.spyOn(api, 'getSystemLog').mockResolvedValue(detail('old'))
    const rounds = vi.spyOn(api, 'getLogConversationRequests').mockResolvedValue({ logs: [summary('old')] })
    vi.spyOn(api, 'getLogConversations').mockResolvedValue({ conversations: [{ key: 'responses:two', latest: summary('other', 2, 'two'), requestCount: 1 }] })
    const store = useLogsStore()
    await store.locateRequest('old')
    rounds.mockResolvedValue({ logs: [] })
    await store.refresh()
    expect(store.selectedKey).toBe('')
    expect(store.conversations.map(group => group.key)).toEqual(['responses:two'])
    expect(store.details.old).toBeUndefined()
  })

  it('新请求更新原会话，选择不随最新 requestId 改变', async () => {
    const fetch = vi.spyOn(api, 'getLogConversations').mockResolvedValue({ conversations: [{ key: 'responses:one', latest: summary('first'), requestCount: 1 }] })
    vi.spyOn(api, 'getLogConversationRequests').mockResolvedValue({ logs: [summary('first')] })
    const store = useLogsStore()
    await store.loadConversations()
    await store.selectConversation(store.conversations[0]!)
    fetch.mockResolvedValue({ conversations: [{ key: 'responses:one', latest: summary('second', 2), requestCount: 2 }] })
    vi.spyOn(api, 'getLogConversationRequests').mockResolvedValue({ logs: [summary('second', 2)] })
    await store.refresh()
    expect(store.conversations).toHaveLength(1)
    expect(store.selectedKey).toBe('responses:one')
    expect(store.requests.map(item => item.requestId)).toEqual(['first', 'second'])
  })

  it('旧请求通过 anchor 单独定位，不要求出现在最新分页', async () => {
    vi.spyOn(api, 'getSystemLog').mockResolvedValue(detail('old'))
    const fetch = vi.spyOn(api, 'getLogConversationRequests').mockImplementation(params => Promise.resolve({ logs: [summary(params.anchor || 'latest', params.anchor ? 1 : 300)] }))
    const store = useLogsStore()
    await store.locateRequest('old')
    expect(fetch).toHaveBeenLastCalledWith(expect.objectContaining({ key: 'responses:one', anchor: 'old' }))
    expect(store.requests[0]!.requestId).toBe('old')
    expect(store.anchored).toBe(true)
  })

  it('较早的定位请求晚到时不能抢回当前选中会话', async () => {
    let resolve!: (_detail: SystemLogDetail) => void
    const old = new Promise<SystemLogDetail>(done => { resolve = done })
    vi.spyOn(api, 'getSystemLog').mockReturnValueOnce(old).mockResolvedValueOnce(detail('new', 2, 'two'))
    vi.spyOn(api, 'getLogConversationRequests').mockResolvedValue({ logs: [summary('new', 2, 'two')] })
    const store = useLogsStore()
    const pending = store.locateRequest('old')
    await store.locateRequest('new')
    resolve(detail('old'))
    await pending
    expect(store.selectedKey).toBe('responses:two')
  })
})
