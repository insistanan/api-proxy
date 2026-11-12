import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createSSRApp, nextTick, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { api } from '@/services/api'
import { useLogCallsStore } from '@/stores/logCalls'
import type { LogCallSummary } from '@/types/logs'
import RequestCallsPanel from './RequestCallsPanel.vue'

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))
type State = Record<string, unknown>
const call = (phases: string[]): LogCallSummary => ({ summary: { requestId: 'one', unixMilli: 1, timestamp: '', phases, status: 'completed', stream: false, truncated: false }, attemptCount: 60 })
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
})
