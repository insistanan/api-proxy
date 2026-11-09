import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createSSRApp, nextTick, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { api } from '@/services/api'
import { useLogsStore } from '@/stores/logs'
import type { SystemLogDetail, SystemLogSummary } from '@/types/logs'
import ConversationLogTimeline from './ConversationLogTimeline.vue'

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))
type State = Record<string, unknown>
type Props = { groupKey: string; requests: SystemLogSummary[] }
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

beforeEach(() => setActivePinia(createPinia()))
afterEach(() => vi.restoreAllMocks())

describe('会话时间线按需加载', () => {
  it('只加载展开轮次及前序，重复刷新复用正文', async () => {
    const requests = Array.from({ length: 6 }, (_, index) => summary(String(index + 1), index + 1))
    const fetch = vi.spyOn(api, 'getSystemLog').mockImplementation(id => Promise.resolve(detail(id, Number(id))))
    const state = await mountSetup({ groupKey: 'conversation', requests })
    await (state.loadOpened as () => Promise<void>)()
    expect(fetch.mock.calls.map(([id]) => id).sort()).toEqual(['5', '6'])
    expect(get<unknown[]>(state, 'rows')).toHaveLength(3)
    await (state.loadOpened as () => Promise<void>)()
    expect(fetch).toHaveBeenCalledTimes(2)
    const rows = get<{ delta?: { body: string } }[]>(state, 'rows')
    expect(JSON.parse(rows[2]!.delta!.body).messages).toEqual([{ role: 'user', content: '消息6' }])
  })

  it('加载更早摘要时不下载折叠轮次正文', async () => {
    const requests = Array.from({ length: 6 }, (_, index) => summary(String(index + 1), index + 1))
    const fetch = vi.spyOn(api, 'getSystemLog').mockImplementation(id => Promise.resolve(detail(id, Number(id))))
    const state = await mountSetup({ groupKey: 'conversation', requests })
    await (state.loadOpened as () => Promise<void>)()
    await (state.loadEarlier as () => Promise<void>)()
    expect(get<unknown[]>(state, 'rows')).toHaveLength(6)
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('一轮读取失败不阻断前序加载，重试后清除错误', async () => {
    const fetch = vi.spyOn(api, 'getSystemLog').mockImplementation(id => id === 'bad' ? Promise.reject(new Error('读取失败')) : Promise.resolve(detail(id)))
    const state = await mountSetup({ groupKey: 'conversation', requests: [summary('ok'), summary('bad', 2)] })
    await (state.loadOpened as () => Promise<void>)()
    const store = useLogsStore()
    expect(store.detailErrors.bad).toBe('读取失败')
    expect(store.details.ok).toBeDefined()
    fetch.mockImplementation(id => Promise.resolve(detail(id, id === 'bad' ? 2 : 1)))
    await (state.loadOpened as () => Promise<void>)()
    expect(store.detailErrors.bad).toBeUndefined()
    expect(store.details.bad).toBeDefined()
  })
})
