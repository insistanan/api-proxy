import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createSSRApp, nextTick, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { api } from '@/api'
import { useLogCallsStore } from '@/features/observability/stores/logCalls'
import type { LogCallSummary, RequestLogEntry } from '@/types/logs'
import RequestCallsPanel from '@/features/observability/components/log-panels/RequestCallsPanel.vue'

vi.mock('@/shared/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))
type State = Record<string, unknown>
const call = (phases: string[]): LogCallSummary => ({ summary: { requestId: 'one', unixMilli: 1, timestamp: '', phases, status: 'completed', stream: false, truncated: false }, attemptCount: 60 })
const attempt = (changes: Partial<RequestLogEntry> = {}): RequestLogEntry => ({ requestId: 'one', attemptId: 'a', timestamp: '', apiType: 'messages', status: 'failed', success: false, durationMs: 1, channelIndex: 0, channelName: '渠道 A', baseUrl: '', keyMask: '', retried: false, stream: false, ...changes })
async function mountSetup() {
  let state!: State
  const setup = (RequestCallsPanel as unknown as { setup: (_props: object, _context: SetupContext) => State }).setup
  await renderToString(createSSRApp({ setup(_props, context) { state = setup({ filters: {} }, { ...context, expose: vi.fn() }); return () => null } }))
  await nextTick()
  return state
}
beforeEach(() => setActivePinia(createPinia()))
afterEach(() => vi.restoreAllMocks())

describe('调用聚合展示', () => {
  it('展开时才查询尝试指标，折叠不会下载正文', async () => {
    vi.spyOn(api, 'getLogCalls').mockResolvedValue({ calls: [call(['request_end'])] })
    const fetch = vi.spyOn(api, 'getLogCallAttempts').mockResolvedValue({ attempts: [], limited: false, notice: '' })
    const body = vi.spyOn(api, 'getSystemLog')
    const state = await mountSetup()
    expect(fetch).not.toHaveBeenCalled()
    const toggle = state.toggle as (_call: LogCallSummary, _event: Event) => void
    toggle(call(['request_end']), { target: { hasAttribute: () => true } } as unknown as Event)
    await useLogCallsStore().loadAttempts(call(['request_end']))
    toggle(call(['request_end']), { target: { hasAttribute: () => false } } as unknown as Event)
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(body).not.toHaveBeenCalled()
  })

  it('仅有尝试成功的旧记录不冒充调用成功', async () => {
    vi.spyOn(api, 'getLogCalls').mockResolvedValue({ calls: [] })
    const state = await mountSetup()
    const outcome = state.outcome as (_call: LogCallSummary) => { label: string }
    expect(outcome(call([])).label).toBe('生命周期未记录')
    expect(outcome(call(['request_end'])).label).toBe('已完成')
  })

  it('重试标志与实际观测到的后续尝试分开展示', async () => {
    vi.spyOn(api, 'getLogCalls').mockResolvedValue({ calls: [] })
    const state = await mountSetup()
    const transition = state.attemptTransition as (_attempt: RequestLogEntry, _next?: RequestLogEntry) => string
    expect(transition(attempt({ retried: true }))).toContain('未观测到后续尝试')
    expect(transition(attempt(), attempt({ attemptId: 'b' }))).toContain('同一渠道')
    expect(transition(attempt(), attempt({ attemptId: 'b', channelIndex: 1, channelName: '渠道 B' }))).toContain('切换至 渠道 B')
    expect(transition(attempt({ attemptId: '' }), attempt({ attemptId: 'b' }))).toContain('无法确认')
    const status = state.attemptStatus as (_attempt: RequestLogEntry) => string
    expect(status(attempt({ status: 'blocked' }))).toBe('已拦截')
    expect(status(attempt({ statusCode: 502, upstreamStatusCode: 200 }))).toBe('失败 · 上游 HTTP 200 · 结果码 502')
  })
})
