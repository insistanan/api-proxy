import { afterEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { api, ApiError } from '@/api'
import ContextTrend from '@/features/observability/components/context/ContextTrend.vue'
import type { ContextOverview } from '@/types/logs'

vi.mock('@/shared/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))
type State = Record<string, unknown>
async function setupTrend() {
  let state!: State
  const setup = (ContextTrend as unknown as { setup: (props: object, context: SetupContext) => State }).setup
  await renderToString(createSSRApp({ setup(_props, context) {
    state = setup({ groupKey: 'responses:missing', version: '' }, { ...context, expose: vi.fn() })
    return () => null
  } }))
  await (state.load as () => Promise<void>)()
  return state
}
afterEach(() => vi.restoreAllMocks())
describe('无会话日志的上下文趋势', () => {
  it('缺失或清理后的 404 显示空状态', async () => {
    vi.spyOn(api, 'getLogContextOverview').mockRejectedValue(new ApiError('日志会话不存在', 404))
    const state = await setupTrend()
    expect((state.overview as Ref<ContextOverview>).value.calls).toBe(0)
    expect((state.error as Ref<string>).value).toBe('')
  })
  it('实际存储故障显示错误，不能降级成无日志', async () => {
    vi.spyOn(api, 'getLogContextOverview').mockRejectedValue(new ApiError('数据库不可读', 500))
    const state = await setupTrend()
    expect((state.overview as Ref<ContextOverview | null>).value).toBeNull()
    expect((state.error as Ref<string>).value).toContain('数据库不可读')
  })
})
