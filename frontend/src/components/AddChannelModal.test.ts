// 表单提交入口回归：校验失败不保存，Images 参数先编译，再交给编辑器组装。
import { describe, expect, it, vi } from 'vitest'
import { createSSRApp, nextTick, ref, type Ref, type SetupContext } from 'vue'
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

describe('渠道表单提交入口', () => {
  it('往返快速模式不会覆盖详细表单中修改的名称和密钥', async () => {
    const { state } = await setupForm()
    const quickInput = state.quickInput as Ref<string>
    const parse = state.parseQuickInput as () => void
    const toggle = state.toggleMode as () => void
    quickInput.value = 'https://images.example\nsk-original-key\nname: 快速名称'
    parse()
    toggle()
    await nextTick()
    const form = state.form as ChannelFormState
    form.name = '详细配置名称'
    form.apiKeys = ['edited-key']
    toggle()
    toggle()
    expect(form.name).toBe('详细配置名称')
    expect(form.apiKeys).toEqual(['edited-key'])
  })

  it('快速模式只修改名称时，返回详细表单保留已编辑的密钥和地址', async () => {
    const { state } = await setupForm()
    const quickInput = state.quickInput as Ref<string>
    const parse = state.parseQuickInput as () => void
    const toggle = state.toggleMode as () => void
    quickInput.value = 'https://images.example\nsk-original-key\nname: 原名称'
    parse()
    toggle()
    await nextTick()
    const form = state.form as ChannelFormState
    form.apiKeys = ['edited-key']
    form.baseUrl = 'https://edited.example'
    toggle()
    quickInput.value = 'https://images.example\nsk-original-key\nname: 新名称'
    parse()
    toggle()
    await nextTick()
    expect(form.name).toBe('新名称')
    expect(form.apiKeys).toEqual(['edited-key'])
    expect(form.baseUrl).toBe('https://edited.example')
  })

  it('关闭自动补全后详细表单提交保留完整请求地址', async () => {
    const { state, emit } = await setupForm()
    const url = 'https://images.example/custom/render/?mode=fast'
    ;(state.autoCompleteUrl as Ref<boolean>).value = false
    ;(state.form as ChannelFormState).baseUrl = url
    expect((state.form as ChannelFormState).useFullUrl).toBe(true)
    await submit(state)
    expect(emit).toHaveBeenCalledWith('save', expect.objectContaining({ useFullUrl: true, baseUrl: url }))
  })

  it('快速添加保留完整地址，重新开启后提交自动补全策略', async () => {
    const { state, emit } = await setupForm()
    const url = 'https://images.example/images/generations/?mode=fast&size=1024%20px'
    ;(state.autoCompleteUrl as Ref<boolean>).value = false
    ;(state.quickInput as Ref<string>).value = `${url}\nsk-test-key`
    ;(state.parseQuickInput as () => void)()
    expect((state.detectedBaseUrl as Ref<string>).value).toBe(url)
    ;(state.handleQuickSubmit as () => void)()
    expect(emit).toHaveBeenCalledWith('save', expect.objectContaining({ useFullUrl: true, baseUrl: url }), { isQuickAdd: true })
    ;(state.autoCompleteUrl as Ref<boolean>).value = true
    ;(state.form as ChannelFormState).serviceType = 'openai'
    ;(state.handleQuickSubmit as () => void)()
    expect(emit).toHaveBeenLastCalledWith('save', expect.objectContaining({ useFullUrl: false }), { isQuickAdd: true })
  })

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
