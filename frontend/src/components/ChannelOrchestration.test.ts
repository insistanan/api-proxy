// 执行真实组件 setup，验证暂停渠道的促销恢复顺序与部分提交错误。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { api, ApiError, type Channel, type ChannelMetrics } from '@/services/api'
import ChannelOrchestration from './ChannelOrchestration.vue'

const { refreshChannels } = vi.hoisted(() => ({ refreshChannels: vi.fn() }))
vi.mock('vue3-apexcharts', () => ({ default: {} }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))
vi.mock('@/stores/channel', () => ({ useChannelStore: () => ({ refreshChannels }) }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))

type State = Record<string, unknown>
const value = <T>(state: State, key: string) => (state[key] as Ref<T>).value
const put = <T>(state: State, key: string, next: T) => { (state[key] as Ref<T>).value = next }
const run = (state: State) => (state.confirmPromotion as () => Promise<void>)()
const suspended: Channel = { name: '暂停渠道', serviceType: 'claude', baseUrl: 'https://example.com', apiKeys: [], index: 2, status: 'suspended' }
const setupPromotion = async (dashboardMetrics: ChannelMetrics[] = []) => {
  let state!: State
  const emit = vi.fn()
  const setup = (ChannelOrchestration as unknown as { setup: (props: object, context: SetupContext) => State }).setup
  await renderToString(createSSRApp({
    setup(_props, context) {
      state = setup({ channels: [], currentChannelIndex: 0, channelType: 'messages', dashboardMetrics }, { ...context, emit, expose: vi.fn() })
      return () => null
    }
  }))
  ;(state.openPromotionDialog as (_channel: Channel) => void)(suspended)
  return { state, emit }
}

const resume = vi.fn()
const status = vi.fn()
const promote = vi.fn()
beforeEach(() => {
  refreshChannels.mockReset().mockResolvedValue(undefined)
  resume.mockReset().mockResolvedValue(undefined)
  status.mockReset().mockResolvedValue(undefined)
  promote.mockReset().mockResolvedValue(undefined)
  const factory = api.channelApi.bind(api)
  vi.spyOn(api, 'channelApi').mockImplementation(type => ({ ...factory(type), resumeChannel: resume, setStatus: status, setPromotion: promote }))
})
afterEach(() => { vi.restoreAllMocks() })

describe('手动恢复渠道', () => {
  it.each(['open', 'half_open'] as const)('配置为 active、运行时为 %s 时点击徽章直接恢复', async circuitState => {
    const { state, emit } = await setupPromotion([{
      channelIndex: 2, requestCount: 4, successCount: 0, failureCount: 4,
      successRate: 0, errorRate: 100, consecutiveFailures: 4, latency: 0, circuitState
    }])
    const channel = { ...suspended, status: 'active' as const }
    await (state.toggleChannelPrimaryStatus as (_channel: Channel) => Promise<void>)(channel)
    expect(status).toHaveBeenCalledExactlyOnceWith(2, 'active')
    expect(resume).not.toHaveBeenCalled()
    expect(emit).toHaveBeenCalledWith('refresh')
    expect(channel.status).toBe('active')
  })

  it.each(['active', 'suspended'] as const)('配置为 %s 且没有运行时熔断时保留状态切换', async currentStatus => {
    const { state } = await setupPromotion()
    await (state.toggleChannelPrimaryStatus as (_channel: Channel) => Promise<void>)({ ...suspended, status: currentStatus })
    expect(status).toHaveBeenCalledExactlyOnceWith(2, currentStatus === 'active' ? 'suspended' : 'active')
  })

  it('恢复失败时显示错误', async () => {
    status.mockRejectedValue(new Error('状态写入失败'))
    const { state, emit } = await setupPromotion()
    await (state.resumeChannel as (_id: number) => Promise<void>)(2)
    expect(emit).toHaveBeenCalledWith('error', expect.stringContaining('状态写入失败'))
    expect(emit).not.toHaveBeenCalledWith('refresh')
  })

  it('激活已提交但收尾失败时刷新并提示实际状态', async () => {
    status.mockRejectedValue(new ApiError('目录同步失败', 500, undefined, undefined, true))
    const { state, emit } = await setupPromotion()
    await (state.resumeChannel as (_id: number) => Promise<void>)(2)
    expect(emit).toHaveBeenCalledWith('refresh')
    expect(emit).toHaveBeenCalledWith('error', expect.stringContaining('状态已写入'))
  })
})

describe('暂停渠道设置促销', () => {
  it('先恢复熔断，再持久化 active，最后设置促销；不修改父级原数据', async () => {
    const { state, emit } = await setupPromotion()
    put(state, 'promotionDuration', 3)
    put(state, 'promotionCount', 2)
    await run(state)
    expect(resume).toHaveBeenCalledWith(2)
    expect(status).toHaveBeenCalledWith(2, 'active')
    expect(promote).toHaveBeenCalledWith(2, 180, 2)
    expect(resume.mock.invocationCallOrder[0]).toBeLessThan(status.mock.invocationCallOrder[0])
    expect(status.mock.invocationCallOrder[0]).toBeLessThan(promote.mock.invocationCallOrder[0])
    expect(refreshChannels).toHaveBeenCalledWith('messages')
    expect(emit).toHaveBeenCalledWith('success', expect.stringContaining('任一限制达到即结束'))
    expect(suspended.status).toBe('suspended')
  })

  it('双零清除促销时保留暂停状态', async () => {
    const { state } = await setupPromotion()
    put(state, 'promotionDuration', 0)
    put(state, 'promotionCount', 0)
    await run(state)
    expect(resume).not.toHaveBeenCalled()
    expect(status).not.toHaveBeenCalled()
    expect(promote).toHaveBeenCalledWith(2, 0, 0)
  })

  it('恢复状态失败时不设置促销，并保留错误', async () => {
    status.mockRejectedValue(new Error('状态写入失败'))
    const { state } = await setupPromotion()
    await run(state)
    expect(promote).not.toHaveBeenCalled()
    expect(value<string>(state, 'promotionError')).toContain('状态写入失败')
    expect(value<Channel>(state, 'promotionChannel').status).toBe('suspended')
  })

  it('恢复状态已提交但收尾失败时标记 active，刷新并提示促销尚未设置', async () => {
    status.mockRejectedValue(new ApiError('目录同步失败', 500, undefined, undefined, true))
    const { state } = await setupPromotion()
    await run(state)
    expect(promote).not.toHaveBeenCalled()
    expect(value<Channel>(state, 'promotionChannel').status).toBe('active')
    expect(value<string>(state, 'promotionError')).toContain('促销尚未设置')
    expect(refreshChannels).toHaveBeenCalledWith('messages')
  })

  it('恢复成功后促销失败，保留已恢复提示并刷新', async () => {
    promote.mockRejectedValue(new Error('促销写入失败'))
    const { state } = await setupPromotion()
    await run(state)
    expect(value<string>(state, 'promotionError')).toContain('渠道已恢复，但促销设置失败')
    expect(refreshChannels).toHaveBeenCalledWith('messages')
  })

  it('促销已提交但收尾失败时关闭弹窗、提示刷新，避免重复提交', async () => {
    promote.mockRejectedValue(new ApiError('目录同步失败', 500, undefined, undefined, true))
    const { state, emit } = await setupPromotion()
    await run(state)
    expect(value<boolean>(state, 'showPromotionDialog')).toBe(false)
    expect(value<Channel | null>(state, 'promotionChannel')).toBeNull()
    expect(emit).toHaveBeenCalledWith('error', expect.stringContaining('请勿重复提交'))
    expect(refreshChannels).toHaveBeenCalledWith('messages')
  })
})
