import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { createPinia, setActivePinia } from 'pinia'
import { api } from '@/api'
import RequestLogsView from '@/features/observability/views/RequestLogsView.vue'
import type { RequestLogEntry } from '@/types/logs'

const replace = vi.hoisted(() => vi.fn())
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ replace }) }))
vi.mock('@/shared/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))
type State = Record<string, unknown>
const attempt: RequestLogEntry = { requestId: 'request', attemptId: 'try-1', timestamp: '', status: 'completed', apiType: 'responses', channelIndex: 0, success: true, durationMs: 1, baseUrl: '', keyMask: '', retried: false, stream: false }
async function setupView() {
  let state!: State
  const setup = (RequestLogsView as unknown as { setup: (props: object, context: SetupContext) => State }).setup
  await renderToString(createSSRApp({ setup(props, context) { state = setup(props, { ...context, expose: vi.fn() }); return () => null } }))
  return state
}
beforeEach(() => { setActivePinia(createPinia()); replace.mockClear() })
afterEach(() => vi.restoreAllMocks())

describe('模型调用逐次尝试', () => {
  it('直接按逐次尝试加载，流量详情在当前页打开并保留当前调用的版本', async () => {
    const fetch = vi.spyOn(api, 'getRequestLogs').mockResolvedValue({ logs: [attempt], limit: 50 })
    const state = await setupView()
    await (state.loadLogs as () => Promise<void>)()
    expect(fetch).toHaveBeenCalledWith(expect.objectContaining({ limit: 50 }))
    expect((state.requestLogs as Ref<RequestLogEntry[]>).value).toEqual([attempt])
    ;(state.openCallDetail as (id: string) => void)('request')
    expect((state.callDetailOpen as Ref<boolean>).value).toBe(true)
    expect((state.callDetailId as Ref<string>).value).toBe('request')
    expect((state.callDetailVersion as Ref<string>).value).toContain('try-1')
    expect(replace).not.toHaveBeenCalled()
  })

  it('读取失败保留已加载尝试并明确显示错误', async () => {
    const fetch = vi.spyOn(api, 'getRequestLogs').mockResolvedValue({ logs: [attempt], limit: 50 })
    const state = await setupView()
    const load = state.loadLogs as () => Promise<void>
    await load()
    fetch.mockRejectedValue(new Error('读取日志失败'))
    await load()
    expect((state.requestLogs as Ref<RequestLogEntry[]>).value).toEqual([attempt])
    expect((state.error as Ref<string>).value).toContain('读取日志失败')
    expect((state.loading as Ref<boolean>).value).toBe(false)
  })
})
