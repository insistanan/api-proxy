// 执行真实页面 setup，验证读取删除影响后必须经组件确认才能提交。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { createPinia, setActivePinia } from 'pinia'
import { api } from '@/api'
import { useChannelStore } from '@/features/channels/stores/channel'
import ChannelsView from '@/features/channels/views/ChannelsView.vue'
import type { ConfirmRequest } from '@/shared/composables/useConfirm'

vi.mock('vue3-apexcharts', () => ({ default: {} }))
vi.mock('vue-router', () => ({ useRouter: () => ({ currentRoute: { value: { params: { type: 'messages' } } } }) }))
vi.mock('@/shared/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))

type State = Record<string, unknown>
const setupView = async () => {
  let state!: State
  const setup = (ChannelsView as unknown as { setup: (props: object, context: SetupContext) => State }).setup
  await renderToString(createSSRApp({
    setup(_props, context) {
      state = setup({}, { ...context, expose: vi.fn() })
      return () => null
    }
  }))
  const store = useChannelStore()
  const remove = vi.spyOn(store, 'deleteChannel').mockResolvedValue({ success: true, message: '渠道删除成功' })
  vi.spyOn(store, 'refreshChannels').mockResolvedValue(undefined)
  vi.spyOn(store, 'refreshVisionChannels').mockResolvedValue(undefined)
  return {
    state, store, remove,
    requestDelete: state.handleDeleteChannel as (id: number, kind?: 'chat') => Promise<void>,
    pending: state.pendingConfirm as Ref<ConfirmRequest | null>,
    accept: state.acceptConfirm as () => Promise<void>,
    cancel: state.cancelConfirm as () => void
  }
}

beforeEach(() => { setActivePinia(createPinia()) })
afterEach(() => { vi.restoreAllMocks() })

describe('渠道删除组件确认', () => {
  it('列出跨协议图片理解引用，确认后才向目标协议发出一次删除', async () => {
    vi.spyOn(api, 'getChannelDeletionImpact').mockResolvedValue({ name: '待删除渠道', channelCount: 1, dependents: [{ kind: 'messages', index: 4, name: '引用方' }] })
    const env = await setupView()
    await env.requestDelete(2, 'chat')
    expect(api.getChannelDeletionImpact).toHaveBeenCalledWith('chat', 2)
    expect(env.remove).not.toHaveBeenCalled()
    expect(env.pending.value?.message).toContain('Messages：引用方')
    expect(env.pending.value?.message).toContain('关闭这些渠道的图片理解支持和图片理解层')
    await env.accept()
    await env.accept()
    expect(env.remove).toHaveBeenCalledExactlyOnceWith(2, 'chat')
    expect(env.pending.value).toBeNull()
  })

  it('取消确认不执行删除', async () => {
    vi.spyOn(api, 'getChannelDeletionImpact').mockResolvedValue({ name: '待删除渠道', channelCount: 1, dependents: [] })
    const env = await setupView()
    await env.requestDelete(0)
    env.cancel()
    await env.accept()
    expect(env.remove).not.toHaveBeenCalled()
  })

  it('读取影响失败时不显示确认框，也不执行删除', async () => {
    vi.spyOn(api, 'getChannelDeletionImpact').mockRejectedValue(new Error('读取失败'))
    const env = await setupView()
    await env.requestDelete(0)
    expect(env.pending.value).toBeNull()
    expect(env.remove).not.toHaveBeenCalled()
  })
})
