import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { api } from '@/services/api'
import type { LogCallAttempts, LogCallSummary, LogCallsResponse } from '@/types/logs'
import { useLogCallsStore } from './logCalls'

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))
const call = (id: string, attemptCount = 1): LogCallSummary => ({ summary: { requestId: id, unixMilli: 1, startedAt: 1, timestamp: '', phases: ['request_start', 'request_end'], status: 'completed', stream: false, truncated: false }, attemptCount })
const chain = (notice = ''): LogCallAttempts => ({ attempts: [], limited: false, notice })
beforeEach(() => setActivePinia(createPinia()))
afterEach(() => vi.restoreAllMocks())

describe('调用分页', () => {
  it('筛选交给服务端，旧筛选的晚到响应不能覆盖新列表', async () => {
    let finish!: (_page: LogCallsResponse) => void
    const old = new Promise<LogCallsResponse>(resolve => { finish = resolve })
    const fetch = vi.spyOn(api, 'getLogCalls').mockReturnValueOnce(old).mockResolvedValueOnce({ calls: [call('new')] })
    const store = useLogCallsStore()
    const pending = store.load({ type: 'messages' })
    await store.load({ type: 'responses', from: '2026-11-01T00:00:00Z' })
    finish({ calls: [call('old')], nextCursor: 'old-cursor' })
    await pending
    expect(fetch).toHaveBeenLastCalledWith(expect.objectContaining({ type: 'responses', from: '2026-11-01T00:00:00Z' }))
    expect(store.calls.map(item => item.summary.requestId)).toEqual(['new'])
    expect(store.nextCursor).toBe('')
  })

  it('按调用游标读取更早记录并合并重复调用', async () => {
    const fetch = vi.spyOn(api, 'getLogCalls').mockResolvedValueOnce({ calls: [call('b')], nextCursor: 'cursor' }).mockResolvedValueOnce({ calls: [call('b', 2), call('a')] })
    const store = useLogCallsStore()
    await store.load({})
    await store.load({}, true)
    expect(fetch).toHaveBeenLastCalledWith(expect.objectContaining({ cursor: 'cursor' }))
    expect(store.calls.map(item => item.summary.requestId)).toEqual(['b', 'a'])
    expect(store.calls[0]!.attemptCount).toBe(2)
    expect(store.nextCursor).toBe('')
  })

  it('重置后晚到请求不能恢复已清理的调用', async () => {
    let finish!: (_page: LogCallsResponse) => void
    vi.spyOn(api, 'getLogCalls').mockReturnValue(new Promise(resolve => { finish = resolve }))
    const store = useLogCallsStore()
    const pending = store.load({})
    store.reset()
    finish({ calls: [call('old')] })
    await pending
    expect(store.calls).toEqual([])
  })
})

describe('尝试指标按需加载', () => {
  it('仅查询展开调用的指标，复用同版本并发读取，不下载正文', async () => {
    const fetch = vi.spyOn(api, 'getLogCallAttempts').mockResolvedValue(chain())
    const body = vi.spyOn(api, 'getSystemLog')
    const store = useLogCallsStore()
    await Promise.all([store.loadAttempts(call('one')), store.loadAttempts(call('one'))])
    await store.loadAttempts(call('one'))
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(body).not.toHaveBeenCalled()
  })

  it('同毫秒新增尝试也刷新缓存，旧响应不能覆盖新的尝试链', async () => {
    let finish!: (_chain: LogCallAttempts) => void
    const old = new Promise<LogCallAttempts>(resolve => { finish = resolve })
    const fetch = vi.spyOn(api, 'getLogCallAttempts').mockReturnValueOnce(old).mockResolvedValueOnce(chain('已故障转移'))
    const store = useLogCallsStore()
    const pending = store.loadAttempts(call('one'))
    await store.loadAttempts(call('one', 2))
    finish(chain('旧记录'))
    await pending
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(store.attempts.one!.notice).toBe('已故障转移')
  })

  it('尝试 ID 与调用摘要未变时，新增指标版本仍刷新缓存', async () => {
    const fetch = vi.spyOn(api, 'getLogCallAttempts').mockResolvedValue(chain())
    const store = useLogCallsStore()
    await store.loadAttempts({ ...call('one'), attemptsVersion: 1 })
    fetch.mockResolvedValue(chain('指标更新'))
    await store.loadAttempts({ ...call('one'), attemptsVersion: 2 })
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(store.attempts.one!.notice).toBe('指标更新')
  })

  it('读取失败显示错误，重试成功清除错误', async () => {
    const fetch = vi.spyOn(api, 'getLogCallAttempts').mockRejectedValueOnce(new Error('指标读取失败')).mockResolvedValueOnce(chain())
    const store = useLogCallsStore()
    await store.loadAttempts(call('one'))
    expect(store.attemptErrors.one).toBe('指标读取失败')
    await store.loadAttempts(call('one'))
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(store.attemptErrors.one).toBeUndefined()
  })
})
