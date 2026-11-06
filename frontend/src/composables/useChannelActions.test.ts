// 渠道写入与刷新回归：固定协议目标，区分已提交状态，并避免重复写入。
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { api, ApiError, type Channel, type ChannelDashboardResponse } from '@/services/api'
import { useChannelStore } from '@/stores/channel'
import { useDialogStore } from '@/stores/dialog'
import { useChannelActions } from './useChannelActions'

vi.mock('vue-router', () => ({ useRouter: () => ({ currentRoute: { value: { params: { type: 'messages' } } } }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))

const channel: Channel = { id: 'stable', index: 0, name: 'test', serviceType: 'openai', baseUrl: 'https://example.com', apiKeys: ['test'] }
const dashboard = (channels: Channel[] = []): ChannelDashboardResponse => ({
  channels, loadBalance: 'failover', metrics: [],
  stats: { multiChannelMode: false, activeChannelCount: 0, traceAffinityCount: 0, traceAffinityTTL: '', failureThreshold: 0, windowSize: 0, circuitRecoveryTime: '' }
})
const deferred = <T>() => {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

beforeEach(() => { setActivePinia(createPinia()) })
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

const arrange = () => {
  const store = useChannelStore()
  const dialog = useDialogStore()
  dialog.openEditChannelModal(channel, 'messages')
  const update = vi.fn().mockResolvedValue(undefined)
  const remove = vi.fn().mockResolvedValue(undefined)
  const add = vi.fn().mockResolvedValue({ channel })
  const promotion = vi.fn().mockResolvedValue(undefined)
  const originalApi = api.channelApi.bind(api)
  const factory = vi.spyOn(api, 'channelApi').mockImplementation(type => ({
    ...originalApi(type), updateChannel: update, deleteChannel: remove, addChannel: add, setPromotion: promotion
  }))
  const refresh = vi.spyOn(api, 'getChannelDashboard').mockResolvedValue(dashboard())
  const notify = vi.fn()
  return { store, dialog, update, remove, add, promotion, factory, refresh, notify, actions: useChannelActions(notify) }
}

describe('渠道操作提交阶段', () => {
  it('编辑协议来自打开表单时的目标，并在写入和刷新期间拒绝重复提交', async () => {
    const env = arrange()
    const write = deferred<void>()
    const read = deferred<ChannelDashboardResponse>()
    env.update.mockReturnValue(write.promise)
    env.refresh.mockReturnValue(read.promise)
    env.store.activeTab = 'responses'
    const pending = env.actions.saveChannel(channel)
    await env.actions.saveChannel(channel)
    expect(env.update).toHaveBeenCalledTimes(1)
    expect(env.factory).toHaveBeenCalledWith('messages')
    expect(env.store.isSavingChannel).toBe(true)
    write.resolve(undefined)
    await vi.waitFor(() => expect(env.refresh).toHaveBeenCalledWith('messages'))
    await env.actions.saveChannel(channel)
    expect(env.update).toHaveBeenCalledTimes(1)
    read.resolve(dashboard())
    await pending
    expect(env.store.isSavingChannel).toBe(false)
    expect(env.dialog.showAddChannelModal).toBe(false)
  })

  it('写入后刷新失败提示已写入，不重复更新', async () => {
    const env = arrange()
    env.refresh.mockRejectedValue(new Error('刷新断连'))
    await env.actions.saveChannel(channel)
    expect(env.update).toHaveBeenCalledTimes(1)
    expect(env.notify).toHaveBeenCalledWith(expect.stringContaining('变更已写入，但刷新渠道失败'), 'warning')
    expect(env.dialog.showAddChannelModal).toBe(false)
    expect(env.store.isSavingChannel).toBe(false)
  })

  it('committed 错误关闭表单并刷新实际状态', async () => {
    const env = arrange()
    env.update.mockRejectedValue(new ApiError('目录同步失败', 500, undefined, 'config_committed', true))
    await env.actions.saveChannel(channel)
    expect(env.update).toHaveBeenCalledTimes(1)
    expect(env.refresh).toHaveBeenCalledWith('messages')
    expect(env.notify).toHaveBeenCalledWith(expect.stringContaining('请勿重复提交'), 'warning')
    expect(env.dialog.showAddChannelModal).toBe(false)
  })

  it('普通写入失败保留表单并显式显示错误', async () => {
    const env = arrange()
    env.update.mockRejectedValue(new Error('备份失败'))
    await env.actions.saveChannel(channel)
    expect(env.dialog.showAddChannelModal).toBe(true)
    expect(env.refresh).not.toHaveBeenCalled()
    expect(env.notify).toHaveBeenCalledWith('备份失败', 'error')
  })

  it('快速创建后的促销失败明确渠道已创建', async () => {
    const env = arrange()
    env.dialog.openAddChannelModal('messages')
    env.promotion.mockRejectedValue(new Error('促销保存失败'))
    await env.actions.saveChannel(channel, { isQuickAdd: true })
    expect(env.add).toHaveBeenCalledTimes(1)
    expect(env.promotion).toHaveBeenCalledTimes(1)
    expect(env.notify).toHaveBeenCalledWith(expect.stringContaining('渠道已创建，请勿重复添加'), 'warning')
    expect(env.dialog.showAddChannelModal).toBe(false)
  })

  it('删除请求期间切换协议，仍刷新发起删除的协议', async () => {
    const env = arrange()
    vi.stubGlobal('confirm', vi.fn(() => true))
    const write = deferred<void>()
    env.remove.mockReturnValue(write.promise)
    const pending = env.actions.deleteChannel(0)
    env.store.activeTab = 'chat'
    await env.actions.deleteChannel(0)
    expect(env.remove).toHaveBeenCalledTimes(1)
    write.resolve(undefined)
    await pending
    expect(env.refresh).toHaveBeenCalledWith('messages')
    expect(env.store.isDeletingChannel).toBe(false)
  })
})

describe('渠道刷新与稳定身份', () => {
  it('同协议在刷新进行中再次请求，会补一次刷新并合并排队请求', async () => {
    const store = useChannelStore()
    const first = deferred<ChannelDashboardResponse>()
    const refresh = vi.spyOn(api, 'getChannelDashboard').mockReturnValueOnce(first.promise).mockResolvedValue(dashboard([channel]))
    const a = store.refreshChannels('messages')
    await vi.waitFor(() => expect(refresh).toHaveBeenCalledTimes(1))
    const b = store.refreshChannels('messages')
    const c = store.refreshChannels('messages')
    first.resolve(dashboard())
    await Promise.all([a, b, c])
    expect(refresh).toHaveBeenCalledTimes(2)
    expect(store.channelsDataMap.messages.channels).toEqual([channel])
  })

  it('某协议刷新失败不阻断另一协议，也不把错误传播给另一协议', async () => {
    const store = useChannelStore()
    const first = deferred<ChannelDashboardResponse>()
    const refresh = vi.spyOn(api, 'getChannelDashboard').mockImplementation(type => type === 'messages' ? first.promise : Promise.resolve(dashboard([channel])))
    const a = store.refreshChannels('messages')
    const rejected = expect(a).rejects.toThrow('messages 失败')
    const b = store.refreshChannels('responses')
    first.reject(new Error('messages 失败'))
    await rejected
    await b
    expect(refresh.mock.calls.map(call => call[0])).toEqual(['messages', 'responses'])
    expect(store.channelsDataMap.responses.channels).toEqual([channel])
  })

  it('渠道移动索引后延迟仍绑定稳定 ID，其他渠道不继承该延迟', async () => {
    const store = useChannelStore()
    store.channelsDataMap.messages.channels = [{ ...channel, latency: 123, latencyTestTime: Date.now() }]
    vi.spyOn(api, 'getChannelDashboard').mockResolvedValue(dashboard([
      { ...channel, id: 'other', index: 0 }, { ...channel, index: 1 }
    ]))
    await store.refreshChannels('messages')
    expect(store.channelsDataMap.messages.channels[0].latency).toBeUndefined()
    expect(store.channelsDataMap.messages.channels[1].latency).toBe(123)
  })

  it('延迟请求晚到时不会写入复用旧索引的另一渠道', async () => {
    const store = useChannelStore()
    store.channelsDataMap.messages.channels = [{ ...channel }]
    const pending = deferred<{ success: boolean; status: string; latency: number }>()
    const originalApi = api.channelApi.bind(api)
    vi.spyOn(api, 'channelApi').mockImplementation(type => ({ ...originalApi(type), pingChannel: () => pending.promise }))
    const ping = store.pingChannel(0)
    store.channelsDataMap.messages.channels = [{ ...channel, id: 'other' }, { ...channel, index: 1 }]
    pending.resolve({ success: true, status: 'ok', latency: 456 })
    await ping
    expect(store.channelsDataMap.messages.channels[0].latency).toBeUndefined()
    expect(store.channelsDataMap.messages.channels[1].latency).toBe(456)
  })
})
