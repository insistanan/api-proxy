import { describe, expect, it } from 'vitest'
import type { SystemLogSummary, TrafficLog } from '@/types/logs'
import { getLogAttempts, getRequestDelta, groupLogConversations, logStatus, requestInputLog } from '@/features/observability/utils/logConversation'

const traffic = (body: unknown, extra: Partial<TrafficLog> = {}): TrafficLog => ({ requestId: 'request', timestamp: '', phase: 'client_request', body: JSON.stringify(body), ...extra })
const summary = (requestId: string, conversationId?: string, unixMilli = 1): SystemLogSummary => ({ requestId, conversationId, unixMilli, timestamp: '', apiType: 'messages', phases: [], stream: false, truncated: false })
const user = (content: string) => ({ role: 'user', content })

describe('日志会话与请求摘要', () => {
  it('按会话身份分组并按请求开始顺序稳定排序', () => {
    const groups = groupLogConversations([summary('a', 'one', 3), summary('b', 'one', 2), summary('c'), summary('d')])
    expect(groups).toHaveLength(3)
    expect(groups[0]!.requests.map(entry => entry.requestId)).toEqual(['b', 'a'])
    expect(groups[0]!.latest.requestId).toBe('a')

    const started = groupLogConversations([
      { ...summary('a', 'one', 100), startedAt: 1 },
      { ...summary('b', 'one', 90), startedAt: 2 }
    ])
    expect(started[0]!.requests.map(entry => entry.requestId)).toEqual(['a', 'b'])
  })

  it('计算首轮、增量、重发、历史变化和截断请求', () => {
    const first = traffic({ system: '系统提示', tools: [], messages: [user('一')] })
    const next = traffic({ system: '系统提示', tools: [], messages: [user('一'), user('二')] })
    expect(JSON.parse(getRequestDelta(undefined, first).top).system).toBe('系统提示')
    const delta = getRequestDelta(first, next)
    expect(delta.top).toBe('')
    expect(JSON.parse(delta.body).messages).toEqual([user('二')])
    expect(delta.sharedCount).toBe(1)

    const repeated = getRequestDelta(traffic({ messages: [user('重复')] }), traffic({ messages: [user('重复')] }, { requestId: 'another-request' }))
    expect(repeated.repeated).toBe(true)
    expect(repeated.notice).toContain('客户端重发')
    expect(JSON.parse(repeated.body).messages).toEqual([])

    const changed = getRequestDelta(
      traffic({ max_tokens: 100, messages: [user('一'), user('旧历史')] }),
      traffic({ max_tokens: 200, messages: [user('一'), user('新摘要')] })
    )
    expect(JSON.parse(changed.top).max_tokens).toBe(200)
    expect(JSON.parse(changed.body).messages).toEqual([user('新摘要')])
    expect(changed.notice).toContain('历史')

    const shortened = getRequestDelta(traffic({ messages: [user('一'), user('二')] }), traffic({ messages: [user('一')] }))
    expect(shortened.repeated).toBe(false)
    expect(shortened.notice).toContain('发生变化')

    const top = getRequestDelta(
      traffic({ system: '系统', tools: [{ name: 'tool' }], max_tokens: 100, previous_response_id: 'old', messages: [user('一')] }),
      traffic({ system: '系统', tools: [{ name: 'tool' }], max_tokens: 200, messages: [user('一'), user('二')] })
    )
    expect(JSON.parse(top.top)).toEqual({ max_tokens: 200 })
    expect(top.removedTopFields).toEqual(['previous_response_id'])

    expect(getRequestDelta(
      traffic({ tools: [{ name: 'tool', description: '工具' }], messages: [{ role: 'user', content: '一' }] }),
      traffic({ messages: [{ content: '一', role: 'user' }], tools: [{ description: '工具', name: 'tool' }] })
    ).repeated).toBe(true)

    for (const field of ['input', 'contents'] as const) {
      const body = JSON.parse(getRequestDelta(traffic({ [field]: [user('一')] }), traffic({ [field]: [user('一'), user('二')] })).body)
      expect(body[field]).toEqual([user('二')])
    }

    const broken = traffic({}, { body: '{"messages":[', truncated: true })
    const brokenDelta = getRequestDelta(undefined, broken)
    expect(brokenDelta.body).toBe(broken.body)
    expect(brokenDelta.notice).toContain('截断')
  })

  it('使用结构化摘要处理大请求并优先选择未省略正文', () => {
    const hash = (number: number) => number.toString(16).padStart(64, '0')
    const snapshot = (offset: number, contents: string[], requestHash: string) => traffic({
      model: 'test', input: contents.map(user),
      _log: { messageOffset: offset, originalMessages: offset + contents.length, messageHashes: contents.map((_, index) => hash(offset + index)), topHash: hash(100), requestHash, notice: '原始正文已省略，展示有界摘要' }
    }, { phase: 'client_input' })

    const delta = getRequestDelta(snapshot(10, ['十', '十一', '十二'], hash(200)), snapshot(11, ['十一', '十二', '十三'], hash(201)))
    expect(JSON.parse(delta.body).input).toEqual([user('十三')])
    expect(delta.notice).toContain('有界摘要')
    expect(delta.repeated).toBe(false)

    expect(getRequestDelta(snapshot(10, ['尾部'], hash(200)), snapshot(10, ['尾部'], hash(201))).repeated).toBe(false)
    const original = traffic({}, { body: '[正文已省略]', truncated: true })
    const compact = snapshot(10, ['新增'], hash(200))
    expect(requestInputLog([original, compact])).toBe(compact)
  })

  it('关联多次上游尝试并展示处理、取消和未知状态', () => {
    const attempts = getLogAttempts([
      traffic({}, { phase: 'upstream_request', attemptId: 'one' }),
      traffic({}, { phase: 'upstream_error', attemptId: 'one' }),
      traffic({}, { phase: 'upstream_request', attemptId: 'two' }),
      traffic({}, { phase: 'upstream_response', attemptId: 'two', statusCode: 200 })
    ])
    expect(attempts).toHaveLength(2)
    expect(attempts[0]!.response).toBeUndefined()
    expect(attempts[0]!.error).toBeDefined()
    expect(attempts[1]!.response?.attemptId).toBe('two')

    const unknown = getLogAttempts([
      traffic({}, { id: 1, phase: 'upstream_request' }),
      traffic({}, { id: 2, phase: 'upstream_request' }),
      traffic({}, { id: 3, phase: 'upstream_response' })
    ])
    expect(unknown).toHaveLength(3)
    expect(unknown.every(attempt => !(attempt.request && attempt.response))).toBe(true)
    expect(unknown[2]!.label).toContain('关联未知')

    expect(logStatus({ ...summary('active'), status: 'processing', missingPhases: ['upstream_response'] }).label).toBe('处理中')
    expect(logStatus({ ...summary('cancelled'), status: 'cancelled', statusCode: 200 }).label).toBe('已取消')
    expect(logStatus(summary('legacy')).label).toBe('结束状态未知')
  })
})
