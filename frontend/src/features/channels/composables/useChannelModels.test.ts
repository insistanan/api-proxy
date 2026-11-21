import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope, reactive, ref } from 'vue'
import { api, type ModelsResponse } from '@/api'
import { defaultChannelForm } from '@/features/channels/composables/useChannelEditor'
import { useChannelModels } from '@/features/channels/composables/useChannelModels'
import type { ChannelModalProps } from '@/features/channels/composables/useChannelModal'

vi.mock('@/shared/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))

const cleanups: Array<() => void> = []
afterEach(() => {
  cleanups.splice(0).forEach(cleanup => cleanup())
  vi.restoreAllMocks()
})
const models = (id: string): ModelsResponse => ({
  object: 'list', data: [{ id, object: 'model', created: 0, owned_by: 'fixture' }]
})
const setupModels = () => {
  const scope = effectScope()
  cleanups.push(() => scope.stop())
  const props = reactive<ChannelModalProps>({ show: true, channelType: 'responses' })
  const form = reactive({ ...defaultChannelForm('responses'), baseUrl: 'https://api.example.com/v1', apiKeys: ['key-a'] })
  const state = scope.run(() => useChannelModels(props, form, ref(false), {
    detectedBaseUrl: ref(''), detectedBaseUrls: ref<string[]>([]), detectedApiKeys: ref<string[]>([])
  }))!
  return { props, form, state }
}

describe('渠道模型候选加载', () => {
  it('重新获取只重试失败密钥，保留成功模型与缓存', async () => {
    const { form, state } = setupModels()
    form.apiKeys = ['key-a', 'key-b']
    let failed = false
    const discover = vi.spyOn(api, 'discoverUpstreamModels').mockImplementation(async request => {
      if (request.apiKey === 'key-b' && !failed) { failed = true; throw new Error('上游暂不可用') }
      return models(request.apiKey!)
    })
    await state.fetchTargetModels()
    expect(state.targetModelOptions.value.map(option => option.value)).toEqual(['key-a'])
    await state.fetchTargetModels()
    expect(discover.mock.calls.map(([request]) => request.apiKey)).toEqual(['key-a', 'key-b', 'key-b'])
    expect(state.targetModelOptions.value.map(option => option.value)).toEqual(['key-a', 'key-b'])
    await state.fetchTargetModels()
    expect(discover).toHaveBeenCalledTimes(3)
  })

  it('全部失败时展示原因，下一次打开允许重试', async () => {
    const { state } = setupModels()
    const discover = vi.spyOn(api, 'discoverUpstreamModels').mockRejectedValueOnce(new Error('密钥无效')).mockResolvedValue(models('recovered'))
    await state.fetchTargetModels()
    expect(state.fetchModelsError.value).toContain('密钥无效')
    await state.fetchTargetModels()
    expect(discover).toHaveBeenCalledTimes(2)
    expect(state.fetchModelsError.value).toBe('')
    expect(state.targetModelOptions.value[0].value).toBe('recovered')
  })

  it('探测最多四路并发，不重复发起；变更连接后停止旧队列并忽略迟到结果', async () => {
    const { form, state } = setupModels()
    form.apiKeys = ['key-1', 'key-2', 'key-3', 'key-4', 'key-5']
    let finishOld!: (value: ModelsResponse) => void
    const old = new Promise<ModelsResponse>(resolve => { finishOld = resolve })
    const discover = vi.spyOn(api, 'discoverUpstreamModels').mockReturnValue(old)
    const first = state.fetchTargetModels()
    await state.fetchTargetModels()
    expect(discover).toHaveBeenCalledTimes(4)
    form.baseUrl = 'https://new.example.com/v1'
    form.apiKeys = ['new-key']
    discover.mockResolvedValue(models('new-model'))
    await state.fetchTargetModels()
    finishOld(models('old-model'))
    await first
    expect(discover).toHaveBeenCalledTimes(5)
    expect(discover).toHaveBeenLastCalledWith(expect.objectContaining({ baseUrl: form.baseUrl, apiKey: 'new-key' }))
    expect(state.targetModelOptions.value.map(option => option.value)).toEqual(['new-model'])
    expect(state.fetchingModels.value).toBe(false)
    expect([...state.keyModelsStatus.value.keys()]).toEqual(['new-key'])
  })

  it('关闭编辑器后迟到的模型结果不回填候选', async () => {
    const { props, state } = setupModels()
    let finish!: (value: ModelsResponse) => void
    vi.spyOn(api, 'discoverUpstreamModels').mockReturnValue(new Promise(resolve => { finish = resolve }))
    const pending = state.fetchTargetModels()
    props.show = false
    finish(models('old-model'))
    await pending
    expect(state.targetModelOptions.value).toEqual([])
    expect(state.fetchingModels.value).toBe(false)
  })
})
