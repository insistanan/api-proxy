// 执行真实页面 setup，验证读取删除影响后必须经组件确认才能提交。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { createPinia, setActivePinia } from 'pinia'
import { api, ApiError, type Channel } from '@/api'
import { useDialogStore } from '@/features/channels/stores/dialog'
import { useChannelStore } from '@/features/channels/stores/channel'
import ChannelsView from '@/features/channels/views/ChannelsView.vue'
import ChannelEditorDialog from '@/features/channels/components/form/ChannelEditorDialog.vue'
import type { ConfirmRequest } from '@/shared/composables/useConfirm'

vi.mock('vue3-apexcharts', () => ({ default: {} }))
const { push } = vi.hoisted(() => ({ push: vi.fn() }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push, currentRoute: { value: { params: { type: 'messages' } } } }) }))
vi.mock('@/shared/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn(), setAuthError: vi.fn() }) }))

type State = Record<string, unknown>
const setupComponent = async (component: typeof ChannelsView | typeof ChannelEditorDialog) => {
  let state!: State
  const setup = (component as unknown as { setup: (props: object, context: SetupContext) => State }).setup
  await renderToString(createSSRApp({
    setup(_props, context) {
      state = setup({}, { ...context, expose: vi.fn() })
      return () => null
    }
  }))
  return state
}

const setupView = async () => {
  const state = await setupComponent(ChannelsView)
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

const saveFromDialog = async (channel: Omit<Channel, 'id' | 'index' | 'latency'>) => {
  const state = await setupComponent(ChannelEditorDialog)
  await (state.handleSaveChannel as (payload: typeof channel) => Promise<void>)(channel)
}

beforeEach(() => { setActivePinia(createPinia()); push.mockReset().mockResolvedValue(undefined) })
afterEach(() => { vi.restoreAllMocks() })

describe('复制渠道保存', () => {
  const source: Channel = {
    id: 'source-id', index: 2, name: '供应商A', kind: 'responses', poolId: 'source-pool',
    serviceType: 'openai', baseUrl: 'https://api.example.com/v1', apiKeys: ['sk-fixture-key']
  }
  const payload = { name: '供应商A (副本)', serviceType: 'openai' as const, baseUrl: source.baseUrl, apiKeys: source.apiKeys, poolId: 'target-pool' }

  it('按渠道自身协议打开草稿，保存为目标协议的新渠道后跳转', async () => {
    const { state, store } = await setupView()
    ;(state.copyChannel as (channel: Channel) => void)(source)
    const dialog = useDialogStore()
    expect(dialog.channelType).toBe('responses')
    expect(dialog.initialPoolId).toBe('default')
    expect(dialog.editingChannel).toBeNull()
    dialog.channelType = 'chat'
    const save = vi.spyOn(store, 'saveChannel').mockResolvedValue({ message: '渠道添加成功' })
    await saveFromDialog(payload)
    expect(save).toHaveBeenCalledExactlyOnceWith(payload, null, undefined, 'chat')
    expect(push).toHaveBeenCalledExactlyOnceWith('/channels/chat')
    expect(dialog.showAddChannelModal).toBe(false)
    expect(source.poolId).toBe('source-pool')
  })

  it('保存失败保留复制弹窗与目标协议，不跳转', async () => {
    const { state, store } = await setupView()
    ;(state.copyChannel as (channel: Channel) => void)(source)
    const dialog = useDialogStore()
    dialog.channelType = 'chat'
    vi.spyOn(store, 'saveChannel').mockRejectedValue(new Error('写入失败'))
    await saveFromDialog(payload)
    expect(dialog.showAddChannelModal).toBe(true)
    expect(dialog.copyingChannel?.id).toBe(source.id)
    expect(push).not.toHaveBeenCalled()
  })

  it('已提交但收尾失败时关闭弹窗并进入目标协议，防止重复创建', async () => {
    const { state, store } = await setupView()
    ;(state.copyChannel as (channel: Channel) => void)(source)
    useDialogStore().channelType = 'chat'
    const save = vi.spyOn(store, 'saveChannel').mockRejectedValue(new ApiError('收尾失败', 500, undefined, undefined, true))
    await saveFromDialog(payload)
    expect(save).toHaveBeenCalledOnce()
    expect(useDialogStore().showAddChannelModal).toBe(false)
    expect(push).toHaveBeenCalledExactlyOnceWith('/channels/chat')
  })
})

it('全局添加保存到选定协议后留在当前页面', async () => {
  const { store } = await setupView()
  const dialog = useDialogStore()
  dialog.openAddChannelModal('messages', 'default', true)
  dialog.channelType = 'embeddings'
  const payload = { name: '向量渠道', serviceType: 'openai' as const, baseUrl: 'https://api.example.com/v1', apiKeys: ['fixture-key'] }
  const save = vi.spyOn(store, 'saveChannel').mockResolvedValue({ message: '渠道添加成功' })
  await saveFromDialog(payload)
  expect(save).toHaveBeenCalledExactlyOnceWith(payload, null, undefined, 'embeddings')
  expect(dialog.showAddChannelModal).toBe(false)
  expect(push).not.toHaveBeenCalled()
})

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
