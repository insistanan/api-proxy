import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { api, ApiError, type Channel } from '@/api'
import ChannelConcurrency from '@/features/channels/components/dashboard/ChannelConcurrency.vue'

const { freeUpdate } = vi.hoisted(() => ({ freeUpdate: vi.fn() }))
vi.mock('@/features/channels/stores/freeChannels', () => ({ useFreeChannelsStore: () => ({ setBranchConcurrency: freeUpdate }) }))
vi.mock('@/shared/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))

type State = Record<string, unknown>
const value = <T>(state: State, key: string) => (state[key] as Ref<T>).value
const channel: Channel = { id: 'stable-id', index: 2, name: '测试渠道', serviceType: 'responses', baseUrl: 'https://example.com', apiKeys: [] }
const update = vi.fn()
beforeEach(() => {
  update.mockReset().mockResolvedValue(undefined)
  freeUpdate.mockReset().mockResolvedValue(undefined)
  const factory = api.channelApi.bind(api)
  vi.spyOn(api, 'channelApi').mockImplementation(kind => ({ ...factory(kind), updateChannel: update }))
})
afterEach(() => vi.restoreAllMocks())

async function editor(source = channel) {
  let state!: State
  const emit = vi.fn()
  const setup = (ChannelConcurrency as unknown as { setup: (props: object, context: SetupContext) => State }).setup
  await renderToString(createSSRApp({
    setup(_props, context) {
      state = setup({ channel: source, kind: 'responses', active: 2, title: '分支统计' }, { ...context, emit, expose: vi.fn() })
      return () => null
    }
  }))
  await (state.edit as () => Promise<void>)()
  return { state, emit, save: state.save as (explicit: boolean) => Promise<void>, draft: state.draft as Ref<string | number | null> }
}

describe('渠道分支并发快速设置', () => {
  it('默认上限为 3，Enter 与失焦只保存一次，成功后请求刷新', async () => {
    const { state, emit, save, draft } = await editor()
    expect(draft.value).toBe(3)
    let finish!: () => void
    update.mockImplementation(() => new Promise<void>(resolve => { finish = resolve }))
    draft.value = '6'
    const pending = save(true)
    await save(false)
    expect(update).toHaveBeenCalledExactlyOnceWith(2, { maxBranchConcurrency: 6 })
    finish()
    await pending
    await save(false)
    expect(update).toHaveBeenCalledTimes(1)
    expect(emit).toHaveBeenCalledWith('refresh')
    expect(value<boolean>(state, 'editing')).toBe(false)
    expect(channel.maxBranchConcurrency).toBeUndefined()
  })

  it.each(['', '0', '-1', '1.5', 'NaN', 'Infinity', '9007199254740992', null])('拒绝非法值 %s，保留草稿并显示提示', async invalid => {
    const { state, save, draft } = await editor()
    draft.value = invalid
    await save(false)
    expect(update).not.toHaveBeenCalled()
    expect(value<boolean>(state, 'editing')).toBe(true)
    expect(value<string>(state, 'error')).toContain('大于零的整数')
  })

  it('失焦可保存；失败后的重复失焦不重试，Enter 可明确重试', async () => {
    const { state, save, draft } = await editor()
    update.mockRejectedValueOnce(new Error('保存失败'))
    draft.value = 5
    await save(false)
    await save(false)
    expect(update).toHaveBeenCalledTimes(1)
    expect(value<boolean>(state, 'editing')).toBe(true)
    await save(true)
    expect(update).toHaveBeenCalledTimes(2)
    expect(value<boolean>(state, 'editing')).toBe(false)
  })

  it('配置已落盘但收尾失败时关闭编辑并提示刷新，防止重复提交', async () => {
    const { state, emit, save, draft } = await editor()
    update.mockRejectedValue(new ApiError('收尾失败', 500, undefined, undefined, true))
    draft.value = 5
    await save(true)
    await save(false)
    expect(update).toHaveBeenCalledTimes(1)
    expect(value<boolean>(state, 'editing')).toBe(false)
    expect(emit).toHaveBeenCalledWith('refresh')
    expect(emit).toHaveBeenCalledWith('error', expect.stringContaining('勿重复提交'))
  })

  it('免费渠道更新共享配置，Esc 取消不发请求', async () => {
    const { state, save, draft } = await editor({ ...channel, builtinProvider: 'kilo', maxBranchConcurrency: 4 })
    expect(draft.value).toBe(4)
    draft.value = 6
    await save(false)
    expect(freeUpdate).toHaveBeenCalledExactlyOnceWith(6)
    expect(update).not.toHaveBeenCalled()
    await (state.edit as () => Promise<void>)()
    draft.value = 8
    ;(state.cancel as () => void)()
    await save(false)
    expect(freeUpdate).toHaveBeenCalledTimes(1)
  })
})
