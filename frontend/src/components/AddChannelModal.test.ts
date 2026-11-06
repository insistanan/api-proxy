// 表单提交入口回归：校验失败不保存，Images 参数先编译，再交给编辑器组装。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp, ref, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import type { ChannelFormState } from '@/composables/useChannelEditor'
import AddChannelModal from './AddChannelModal.vue'

vi.mock('vuetify', () => ({ useTheme: () => ({ current: ref({ dark: false }) }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))

type State = Record<string, unknown>
const submit = (state: State) => (state.handleSubmit as () => Promise<void>)()
const setupForm = async (saving = false) => {
  let state!: State
  const emit = vi.fn()
  const setup = (AddChannelModal as unknown as { setup: (props: object, context: SetupContext) => State }).setup
  await renderToString(createSSRApp({
    setup(_props, context) {
      state = setup({ show: false, saving, channelType: 'images', channel: null }, { ...context, emit })
      return () => null
    }
  }))
  const validate = vi.fn().mockResolvedValue({ valid: true })
  ;(state.formRef as Ref<unknown>).value = { validate }
  Object.assign(state.form as ChannelFormState, {
    name: ' 图片渠道 ', serviceType: 'responses', responsesModel: ' main-model ',
    baseUrl: 'https://images.example/', apiKeys: ['fixture-key'], imagesForceNonStream: true,
    imagesExtraParams: [{ key: ' size ', value: ' "1024x1024" ' }]
  })
  return { state, emit, validate }
}

beforeEach(() => {
  // SSR 只需满足预览 watcher 的定时器接口，不创建真实定时任务。
  vi.stubGlobal('window', { setTimeout: vi.fn(() => 1), clearTimeout: vi.fn() })
})
afterEach(() => { vi.unstubAllGlobals() })

describe('渠道表单提交入口', () => {
  it('合法表单只提交编译和清理后的 payload', async () => {
    const { state, emit, validate } = await setupForm()
    await submit(state)
    expect(validate).toHaveBeenCalledOnce()
    expect(emit).toHaveBeenCalledWith('save', expect.objectContaining({
      name: '图片渠道', baseUrl: 'https://images.example', responsesModel: 'main-model',
      imagesForceNonStream: true, imagesExtraParams: { size: '"1024x1024"' }
    }))
    expect(emit).toHaveBeenCalledOnce()
  })

  it('表单校验失败时不发出保存事件', async () => {
    const { state, emit, validate } = await setupForm()
    validate.mockResolvedValue({ valid: false })
    await submit(state)
    expect(emit).not.toHaveBeenCalled()
  })

  it('Images 参数错误保留在表单内，不发出保存事件', async () => {
    const { state, emit } = await setupForm()
    ;(state.form as ChannelFormState).imagesExtraParams = [{ key: 'model', value: '覆盖模型' }]
    await submit(state)
    expect(emit).not.toHaveBeenCalled()
    expect((state.imagesExtraParamsError as Ref<string>).value).toContain('保留字段 model')
  })

  it('保存期间不重复校验、保存或关闭草稿', async () => {
    const { state, emit, validate } = await setupForm(true)
    await submit(state)
    ;(state.handleCancel as () => void)()
    expect(validate).not.toHaveBeenCalled()
    expect(emit).not.toHaveBeenCalled()
  })

  it('渠道选择 GIF 会保存独立输出设置，无需自定义参数行', async () => {
    const { state, emit } = await setupForm()
    Object.assign(state.form as ChannelFormState, { imagesOutputFormat: 'gif', imagesFrameCount: '9', imagesFrameDurationMs: '200', imagesExtraParams: [] })
    await submit(state)
    expect(emit).toHaveBeenCalledWith('save', expect.objectContaining({ imagesOutputFormat: 'gif', imagesFrameCount: 9, imagesFrameDurationMs: 200 }))
  })

  it('GIF 帧参数非法时不保存，并显示明确错误', async () => {
    const { state, emit } = await setupForm()
    Object.assign(state.form as ChannelFormState, { imagesOutputFormat: 'gif', imagesFrameCount: 17 })
    await submit(state)
    expect(emit).not.toHaveBeenCalled()
    expect((state.imagesOutputError as Ref<string>).value).toContain('2–16')
  })

  it('快速添加同样保存 GIF 设置，非法帧参数不会提交', async () => {
    const { state, emit } = await setupForm()
    ;(state.detectedBaseUrl as Ref<string>).value = 'https://images.example'
    ;(state.detectedBaseUrls as Ref<string[]>).value = ['https://images.example']
    ;(state.detectedApiKeys as Ref<string[]>).value = ['fixture-key']
    Object.assign(state.form as ChannelFormState, { imagesOutputFormat: 'gif', imagesFrameCount: 9, imagesFrameDurationMs: 200 })
    ;(state.handleQuickSubmit as () => void)()
    expect(emit).toHaveBeenCalledWith('save', expect.objectContaining({ imagesOutputFormat: 'gif', imagesFrameCount: 9, imagesFrameDurationMs: 200 }), { isQuickAdd: true })
    emit.mockClear()
    ;(state.form as ChannelFormState).imagesFrameCount = 17
    ;(state.handleQuickSubmit as () => void)()
    expect(emit).not.toHaveBeenCalled()
  })
})
