import { afterEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp, nextTick, reactive, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { api } from '@/services/api'
import type { SystemLogDetail, SystemLogSummary } from '@/types/logs'
import ConversationLogTimeline from './ConversationLogTimeline.vue'

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))
type State = Record<string, unknown>
type Props = { groupKey: string; requests: SystemLogSummary[]; seed?: SystemLogDetail }
const get = <T>(state: State, key: string) => (state[key] as Ref<T>).value
const summary = (id: string, order = 1): SystemLogSummary => ({ requestId: id, conversationId: 'conversation', timestamp: '', unixMilli: order, startedAt: order, apiType: 'messages', phases: [], stream: false, truncated: false, status: 'completed' })
const detail = (id: string, order = 1): SystemLogDetail => ({
  requestId: id, summary: summary(id, order), trafficLogs: [{ requestId: id, timestamp: '', phase: 'client_request', body: JSON.stringify({ system: '公共提示', messages: Array.from({ length: order }, (_, index) => ({ role: 'user', content: `消息${index + 1}` })) }) }]
})

async function mountSetup(props: Props) {
  let state!: State
  const setup = (ConversationLogTimeline as unknown as { setup: (_props: object, _context: SetupContext) => State }).setup
  await renderToString(createSSRApp({ setup(_props, context) { state = setup(props, { ...context, expose: vi.fn() }); return () => null } }))
  await nextTick()
  return state
}

afterEach(() => vi.restoreAllMocks())

describe('会话时间线加载', () => {
  it('首次只加载最近三轮，复用已有详情，加载更早轮次后保持增量正确', async () => {
    const requests = Array.from({ length: 6 }, (_, index) => summary(String(index + 1), index + 1))
    const fetch = vi.spyOn(api, 'getSystemLog').mockImplementation(id => Promise.resolve(detail(id, Number(id))))
    const state = await mountSetup({ groupKey: 'conversation', requests, seed: detail('6', 6) })
    expect(fetch.mock.calls.map(([id]) => id)).toEqual(['4', '5'])
    expect(get<unknown[]>(state, 'rows')).toHaveLength(3)
    ;(state.loadEarlier as () => void)()
    await nextTick()
    await nextTick()
    expect(fetch.mock.calls.map(([id]) => id)).toEqual(['4', '5', '1', '2', '3'])
    const rows = get<{ delta?: { top: string; body: string } }[]>(state, 'rows')
    expect(rows).toHaveLength(6)
    expect(rows[0]!.delta?.top).toContain('公共提示')
    expect(JSON.parse(rows[5]!.delta!.body).messages).toEqual([{ role: 'user', content: '消息6' }])
  })

  it('一轮加载失败不会隐藏其他轮次，刷新成功后清除错误', async () => {
    const fetch = vi.spyOn(api, 'getSystemLog').mockImplementation(id => id === 'bad' ? Promise.reject(new Error('读取失败')) : Promise.resolve(detail(id)))
    const state = await mountSetup({ groupKey: 'conversation', requests: [summary('ok'), summary('bad', 2)] })
    expect(get<Record<string, string>>(state, 'errors').bad).toBe('读取失败')
    expect(get<Record<string, SystemLogDetail>>(state, 'details').ok).toBeDefined()
    fetch.mockImplementation(id => Promise.resolve(detail(id)))
    await (state.loadVisible as (_refresh: boolean) => Promise<void>)(true)
    expect(get<Record<string, string>>(state, 'errors').bad).toBeUndefined()
    expect(get<Record<string, SystemLogDetail>>(state, 'details').bad).toBeDefined()
  })

  it('切换会话后，旧会话晚到的请求结果不能覆盖新会话', async () => {
    let resolveFirst!: (_detail: SystemLogDetail) => void
    const first = new Promise<SystemLogDetail>(resolve => { resolveFirst = resolve })
    vi.spyOn(api, 'getSystemLog').mockImplementation(id => id === 'first' ? first : Promise.resolve(detail(id)))
    const props = reactive<Props>({ groupKey: 'first-group', requests: [summary('first')] })
    const state = await mountSetup(props)
    props.groupKey = 'second-group'
    props.requests = [summary('second')]
    // SSR 渲染结束后停止 watcher，直接调用它编排的加载入口验证晚到结果。
    await (state.loadVisible as (_refresh: boolean) => Promise<void>)(false)
    resolveFirst(detail('first'))
    await nextTick()
    await nextTick()
    expect(get<Record<string, SystemLogDetail>>(state, 'details').second).toBeDefined()
    expect(get<Record<string, SystemLogDetail>>(state, 'details').first).toBeUndefined()
  })
})
