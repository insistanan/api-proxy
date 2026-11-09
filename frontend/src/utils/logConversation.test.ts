import { describe, expect, it } from 'vitest'
import type { SystemLogSummary, TrafficLog } from '@/types/logs'
import { getLogAttempts, getRequestDelta, groupLogConversations, logStatus, requestInputLog } from './logConversation'

const traffic = (body: unknown, extra: Partial<TrafficLog> = {}): TrafficLog => ({ requestId: 'request', timestamp: '', phase: 'client_request', body: JSON.stringify(body), ...extra })
const summary = (requestId: string, conversationId?: string, unixMilli = 1): SystemLogSummary => ({ requestId, conversationId, unixMilli, timestamp: '', apiType: 'messages', phases: [], stream: false, truncated: false })
const user = (content: string) => ({ role: 'user', content })

describe('会话分组', () => {
  it('按可靠会话身份分组，缺少身份的请求保持独立', () => {
    const groups = groupLogConversations([summary('a', 'one', 3), summary('b', 'one', 2), summary('c'), summary('d')])
    expect(groups).toHaveLength(3)
    expect(groups[0]!.requests.map(entry => entry.requestId)).toEqual(['b', 'a'])
    expect(groups[0]!.latest.requestId).toBe('a')
  })

  it('并发完成顺序不覆盖已记录的请求开始顺序', () => {
    const a = { ...summary('a', 'one', 100), startedAt: 1 }
    const b = { ...summary('b', 'one', 90), startedAt: 2 }
    expect(groupLogConversations([b, a])[0]!.requests.map(entry => entry.requestId)).toEqual(['a', 'b'])
  })
})

describe('请求增量', () => {
  it('首轮展示完整顶部和消息，后续只展示新增消息', () => {
    const first = traffic({ system: '系统提示', tools: [], messages: [user('一')] })
    const next = traffic({ system: '系统提示', tools: [], messages: [user('一'), user('二')] })
    expect(JSON.parse(getRequestDelta(undefined, first).top).system).toBe('系统提示')
    const delta = getRequestDelta(first, next)
    expect(delta.top).toBe('')
    expect(JSON.parse(delta.body).messages).toEqual([user('二')])
    expect(delta.sharedCount).toBe(1)
  })

  it('标明完全相同的客户端重发，保留每次独立响应', () => {
    const first = traffic({ messages: [user('重复')] })
    const next = traffic({ messages: [user('重复')] }, { requestId: 'another-request' })
    const delta = getRequestDelta(first, next)
    expect(delta.repeated).toBe(true)
    expect(delta.notice).toContain('客户端重发')
    expect(JSON.parse(delta.body).messages).toEqual([])
  })

  it('配置改变时展示新版顶部，历史改写时展示替换段', () => {
    const first = traffic({ max_tokens: 100, messages: [user('一'), user('旧历史')] })
    const next = traffic({ max_tokens: 200, messages: [user('一'), user('新摘要')] })
    const delta = getRequestDelta(first, next)
    expect(JSON.parse(delta.top).max_tokens).toBe(200)
    expect(JSON.parse(delta.body).messages).toEqual([user('新摘要')])
    expect(delta.notice).toContain('历史')
  })

  it('历史裁剪到更短时明确标记，不能误判成相同重发', () => {
    const first = traffic({ messages: [user('一'), user('二')] })
    const next = traffic({ messages: [user('一')] })
    expect(getRequestDelta(first, next).repeated).toBe(false)
    expect(getRequestDelta(first, next).notice).toContain('发生变化')
  })

  it('顶部只展示修改的字段，明确标记删除，不重复铺开相同 tools', () => {
    const first = traffic({ system: '系统', tools: [{ name: 'tool' }], max_tokens: 100, previous_response_id: 'old', messages: [user('一')] })
    const next = traffic({ system: '系统', tools: [{ name: 'tool' }], max_tokens: 200, messages: [user('一'), user('二')] })
    const delta = getRequestDelta(first, next)
    expect(JSON.parse(delta.top)).toEqual({ max_tokens: 200 })
    expect(delta.removedTopFields).toEqual(['previous_response_id'])
  })

  it('普通请求的 _log 字段仍按业务配置比较', () => {
    const first = traffic({ _log: { custom: '旧' }, messages: [user('一')] })
    const next = traffic({ _log: { custom: '新' }, messages: [user('一')] })
    const delta = getRequestDelta(first, next)
    expect(delta.repeated).toBe(false)
    expect(JSON.parse(delta.top)._log).toEqual({ custom: '新' })
  })

  it('忽略 JSON 对象键序，保留数组顺序与内容变化', () => {
    const first = traffic({ tools: [{ name: 'tool', description: '工具' }], messages: [{ role: 'user', content: '一' }] })
    const next = traffic({ messages: [{ content: '一', role: 'user' }], tools: [{ description: '工具', name: 'tool' }] })
    expect(getRequestDelta(first, next).repeated).toBe(true)
  })

  it.each(['input', 'contents'])('支持 %s 消息数组，不能把响应内容自动补入历史', field => {
    const first = traffic({ [field]: [user('一')] })
    const next = traffic({ [field]: [user('一'), user('二')] })
    expect(JSON.parse(getRequestDelta(first, next).body)[field]).toEqual([user('二')])
  })

  it('正文截断或不完整 JSON 时保留原文并说明无法比较', () => {
    const broken = traffic({}, { body: '{"messages":[', truncated: true })
    const delta = getRequestDelta(undefined, broken)
    expect(delta.body).toBe(broken.body)
    expect(delta.notice).toContain('截断')
  })
})

describe('超大请求结构化摘要', () => {
  const hash = (number: number) => number.toString(16).padStart(64, '0')
  const snapshot = (offset: number, contents: string[], requestHash: string) => traffic({
    model: 'test',
    input: contents.map(user),
    _log: { messageOffset: offset, originalMessages: offset + contents.length, messageHashes: contents.map((_, index) => hash(offset + index)), topHash: hash(100), requestHash, notice: '原始正文已省略，展示有界摘要' }
  }, { phase: 'client_input' })

  it('摘要窗口前移时按消息位置和哈希比较新增输入', () => {
    const previous = snapshot(10, ['十', '十一', '十二'], hash(200))
    const current = snapshot(11, ['十一', '十二', '十三'], hash(201))
    const delta = getRequestDelta(previous, current)
    expect(JSON.parse(delta.body).input).toEqual([user('十三')])
    expect(delta.notice).toContain('有界摘要')
    expect(delta.repeated).toBe(false)
  })

  it('尾部相同但全量请求不同，不能误判成重发', () => {
    const previous = snapshot(10, ['尾部'], hash(200))
    const current = snapshot(10, ['尾部'], hash(201))
    expect(getRequestDelta(previous, current).repeated).toBe(false)
  })

  it('原始正文省略时优先使用结构化输入记录', () => {
    const original = traffic({}, { body: '[正文已省略]', truncated: true })
    const compact = snapshot(10, ['新增'], hash(200))
    expect(requestInputLog([original, compact])).toBe(compact)
  })
})

describe('尝试关联和结果展示', () => {
  it('第一个尝试无响应时，第二个响应仍关联第二个请求', () => {
    const entries = [
      traffic({}, { phase: 'upstream_request', attemptId: 'one' }),
      traffic({}, { phase: 'upstream_error', attemptId: 'one' }),
      traffic({}, { phase: 'upstream_request', attemptId: 'two' }),
      traffic({}, { phase: 'upstream_response', attemptId: 'two', statusCode: 200 })
    ]
    const attempts = getLogAttempts(entries)
    expect(attempts).toHaveLength(2)
    expect(attempts[0]!.response).toBeUndefined()
    expect(attempts[0]!.error).toBeDefined()
    expect(attempts[1]!.response?.attemptId).toBe('two')
  })

  it('历史多次尝试缺少 ID 时不按数组位置猜测对应关系', () => {
    const attempts = getLogAttempts([
      traffic({}, { id: 1, phase: 'upstream_request' }),
      traffic({}, { id: 2, phase: 'upstream_request' }),
      traffic({}, { id: 3, phase: 'upstream_response' })
    ])
    expect(attempts).toHaveLength(3)
    expect(attempts.every(attempt => !(attempt.request && attempt.response))).toBe(true)
    expect(attempts[2]!.label).toContain('关联未知')
  })

  it('处理中不显示缺段，HTTP 200 不覆盖取消结果', () => {
    expect(logStatus({ ...summary('active'), status: 'processing', missingPhases: ['upstream_response'] }).label).toBe('处理中')
    expect(logStatus({ ...summary('cancelled'), status: 'cancelled', statusCode: 200 }).label).toBe('已取消')
    expect(logStatus(summary('legacy')).label).toBe('结束状态未知')
  })
})
