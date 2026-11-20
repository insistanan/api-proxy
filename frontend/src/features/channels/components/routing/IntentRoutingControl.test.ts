import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import IntentRoutingControl from '@/features/channels/components/routing/IntentRoutingControl.vue'
import type { IntentConfig, IntentOptions, LocalStatus } from '@/features/channels/services/intent'
import type { Dataset, EmbeddingProfile } from '@/types'

const mocks = vi.hoisted(() => ({ load: vi.fn(), save: vi.fn(), options: vi.fn(), local: vi.fn(), preview: vi.fn(), feedback: vi.fn(), buildIndex: vi.fn(), indexStatus: vi.fn(), datasetList: vi.fn(), datasetBuiltin: vi.fn(), datasetSave: vi.fn() }))
vi.mock('@/shared/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))
vi.mock('@/features/channels/stores/channel', () => ({ useChannelStore: () => ({ channelPoolsMap: { responses: [{ id: 'language', name: '语言组' }, { id: 'code', name: '代码组' }, { id: 'builtin-free', name: '免费分组' }] } }) }))
vi.mock('@/features/channels/stores/intent', () => ({ useIntentStore: () => ({ configs: {}, load: mocks.load, save: mocks.save }) }))
vi.mock('@/features/channels/services/intent', () => ({ intentApi: { options: mocks.options, local: mocks.local, preview: mocks.preview, feedback: mocks.feedback, buildIndex: mocks.buildIndex, indexStatus: mocks.indexStatus }, intentErrorText: (error: Error) => error.message, intentPreviewFailure: () => undefined }))
vi.mock('@/features/extensions/datasets', () => ({ datasetsApi: { list: mocks.datasetList, builtin: mocks.datasetBuiltin, save: mocks.datasetSave } }))
vi.mock('@/shared/composables/useToast', () => ({ useToast: () => ({ showToast: vi.fn() }) }))

const config = (): IntentConfig => ({ enabled: false, strategy: 'regex', classifier: 'heuristic', classifierChannel: '', classifierModel: '', embeddingChannel: '', embeddingModel: '', embeddingSource: 'remote', queryPrefix: '', examplePrefix: '', threshold: 0.75, timeoutMs: 2000, minResidenceSeconds: 300, minTurns: 4, switchMargin: 0.15, unmatched: 'original', fallbackPoolId: '', failurePolicy: 'error', observeOnly: true, routes: [] })
const localStatus = (): LocalStatus => ({ installed: false, running: false, ready: false, busy: false, error: '', platform: 'windows/amd64', installPath: 'D:\\gateway\\.config\\intent-embedding', presetsUrl: 'https://example.invalid/embedding-presets.json', phase: 'idle', manifest: { runtimeUrl: '', runtimeSha256: '', executable: '', modelUrl: '', modelSha256: '', modelName: '', pooling: 'mean' } })
type State = Record<string, unknown>
const value = <T>(state: State, name: string) => (state[name] as Ref<T>).value
const setup = async () => {
  let state!: State
  const component = IntentRoutingControl as unknown as { setup: (props: object, context: SetupContext) => State }
  await renderToString(createSSRApp({ setup(_props, context) { state = component.setup({ protocol: 'responses' }, { ...context, expose: vi.fn() }); return () => null } }))
  await (state.open as () => Promise<void>)()
  return state
}
beforeEach(() => {
  vi.resetAllMocks()
  mocks.load.mockResolvedValue(config())
  mocks.options.mockResolvedValue({ classifiers: [], embeddings: [], tasks: [] } satisfies IntentOptions)
  mocks.local.mockResolvedValue(localStatus())
  mocks.buildIndex.mockResolvedValue(undefined)
  mocks.indexStatus.mockResolvedValue({ ready: true })
  mocks.datasetList.mockResolvedValue([])
  mocks.datasetBuiltin.mockResolvedValue([])
  mocks.datasetSave.mockImplementation(async (dataset: Dataset) => ({ ...dataset, revision: 1 }))
  mocks.save.mockResolvedValue(undefined)
})

describe('意图匹配编辑器', () => {
  it('免费组显示在能力列表中，旧 System One 草稿改用 Decisions', async () => {
    const original = config()
    original.classifier = 'systemone'
    mocks.load.mockResolvedValue(original)
    const state = await setup()
    expect(value<{ id: string }[]>(state, 'pools').map(pool => pool.id)).toContain('builtin-free')
    expect(value<IntentConfig>(state, 'draft').classifier).toBe('decisions')
    expect(original.classifier).toBe('systemone')
    ;(state.groups as Ref<IntentConfig['groups']>).value = [{ poolId: 'builtin-free', tasks: ['architecture'], tiers: [2], cost: 0 }]
    await (state.save as () => Promise<void>)()
    expect(mocks.save).toHaveBeenCalledWith('responses', expect.objectContaining({ classifier: 'decisions', groups: [{ poolId: 'builtin-free', tasks: ['architecture'], tiers: [2], cost: 0 }] }))
  })
  it('只给空配置推荐综合模式，保留已有正则规则', async () => {
    const blank = config()
    mocks.load.mockResolvedValue(blank)
    const state = await setup()
    expect(value<IntentConfig>(state, 'draft').strategy).toBe('auto')
    expect(blank.strategy).toBe('regex')
    const existing = config()
    existing.routes = [{ id: 'manual', poolId: 'code', description: '已有规则', patterns: ['测试'], examples: [], tier: 1, cost: 0 }]
    mocks.load.mockResolvedValue(existing)
    const edited = await setup()
    expect(value<IntentConfig>(edited, 'draft').strategy).toBe('regex')
    expect(value<IntentConfig>(edited, 'draft').routes).toEqual(existing.routes)
  })
  it('分组能力和向量配置进入同一草稿，保存前不会污染原配置', async () => {
    const original = config()
    mocks.load.mockResolvedValue(original)
    const state = await setup()
    ;(state.groups as Ref<IntentConfig['groups']>).value = [{ poolId: 'code', tasks: ['debugging'], tiers: [1, 2], cost: 0 }]
    ;(state.embeddingProfile as Ref<EmbeddingProfile>).value = { source: 'remote', channel: 'embedding', model: 'vector', modelVersion: 'v1', queryPrefix: 'query: ', examplePrefix: 'passage: ', timeoutMs: 5000 }
    expect(original.groups).toBeUndefined()
    expect(original.embeddingModel).toBe('')
    await (state.save as () => Promise<void>)()
    expect(mocks.save).toHaveBeenCalledWith('responses', expect.objectContaining({ groups: [{ poolId: 'code', tasks: ['debugging'], tiers: [1, 2], cost: 0 }], embeddingModel: 'vector', queryPrefix: 'query: ', timeoutMs: 5000 }))
  })
  it('预览只调用分类，不保存配置或发送满意度反馈', async () => {
    mocks.preview.mockResolvedValue({ task: 'debugging', poolId: 'code', tier: 2, reason: '匹配', durationMs: 1 })
    const state = await setup()
    ;(state.previewText as Ref<string>).value = '排查并发死锁'
    await (state.preview as () => Promise<void>)()
    expect(mocks.preview).toHaveBeenCalledOnce()
    expect(mocks.save).not.toHaveBeenCalled()
    expect(mocks.feedback).not.toHaveBeenCalled()
  })
  it('保存和预览沿用现有渠道分组顺序，不受能力填写先后影响', async () => {
    const original = config()
    original.groups = [{ poolId: 'code', tasks: ['code_generation'], tiers: [1], cost: 0 }, { poolId: 'language', tasks: ['writing'], tiers: [1], cost: 0 }]
    mocks.load.mockResolvedValue(original)
    const state = await setup()
    ;(state.previewText as Ref<string>).value = '编写一个函数'
    await (state.preview as () => Promise<void>)()
    await (state.save as () => Promise<void>)()
    expect(mocks.preview.mock.calls[0]![1].groups.map((group: { poolId: string }) => group.poolId)).toEqual(['language', 'code'])
    expect(mocks.save.mock.calls[0]![1].groups.map((group: { poolId: string }) => group.poolId)).toEqual(['language', 'code'])
    expect(original.groups.map(group => group.poolId)).toEqual(['code', 'language'])
  })
  it('能力未填写完整时定位到分组并提示名称，不发送无效配置', async () => {
    const state = await setup()
    ;(state.groups as Ref<IntentConfig['groups']>).value = [{ poolId: 'code', tasks: ['debugging'], tiers: [], cost: 0 }]
    await (state.save as () => Promise<void>)()
    expect(mocks.save).not.toHaveBeenCalled()
    expect(value<string>(state, 'error')).toContain('代码组')
  })
  it('向量模型未就绪时阻断启用，模型就绪且索引匹配后才能保存', async () => {
    const original = config()
    original.enabled = true
    original.strategy = 'auto'
    original.taskMatcher = 'semantic'
    original.embeddingSource = 'local'
    original.embeddingModel = 'vector'
    original.datasetIds = ['intent']
    mocks.load.mockResolvedValue(original)
    const state = await setup()
    expect(value<boolean>(state, 'vectorGuide')).toBe(false)
    expect(value<string>(state, 'vectorReason')).toContain('尚未就绪')
    await (state.save as () => Promise<void>)()
    expect(mocks.save).not.toHaveBeenCalled()
    const ready = localStatus()
    ready.installed = ready.running = ready.ready = true
    ready.manifest.modelName = 'vector'
    ;(state.updateLocalStatus as (status: LocalStatus) => void)(ready)
    await (state.checkIndex as () => Promise<void>)()
    expect(value<string>(state, 'vectorReason')).toBe('')
    await (state.save as () => Promise<void>)()
    expect(mocks.save).toHaveBeenCalledOnce()
    expect(original.embeddingModel).toBe('vector')
  })
  it('常用方式可映射到自动分流，保留同一份草稿字段', async () => {
    const state = await setup()
    const mode = state.simpleMode as Ref<string>
    mode.value = 'assistant'
    expect(value<IntentConfig>(state, 'draft')).toMatchObject({ strategy: 'auto', recognitionMode: 'assistant', taskMatcher: 'builtin', classifier: 'llm' })
    mode.value = 'semantic'
    expect(value<IntentConfig>(state, 'draft')).toMatchObject({ strategy: 'auto', recognitionMode: 'semantic', taskMatcher: 'semantic', classifier: 'heuristic' })
    mode.value = 'builtin'
    expect(value<IntentConfig>(state, 'draft')).toMatchObject({ strategy: 'auto', recognitionMode: 'builtin', taskMatcher: 'builtin', classifier: 'heuristic' })
  })
  it('内置样本未审核时提示直接去数据集页面，不自动导入或批准', async () => {
    const original = config()
    original.strategy = 'auto'
    original.taskMatcher = 'semantic'
    original.embeddingSource = 'remote'
    original.embeddingChannel = 'vector'
    original.embeddingModel = 'embed-model'
    mocks.load.mockResolvedValue(original)
    mocks.options.mockResolvedValue({ classifiers: [], embeddings: [{ id: 'vector', name: '向量渠道', defaultModel: 'embed-model' }], tasks: [] } satisfies IntentOptions)
    mocks.datasetList.mockResolvedValue([{ id: 'starter-intents-v2', kind: 'intent', approved: 0 }])
    const state = await setup()
    await (state.prepareSemanticIndex as () => Promise<void>)()
    expect(value<string>(state, 'error')).toContain('审核并保存')
    expect(mocks.datasetBuiltin).not.toHaveBeenCalled()
    expect(mocks.datasetSave).not.toHaveBeenCalled()
    expect(mocks.buildIndex).not.toHaveBeenCalled()
  })
  it('内置数据集已删除时不重新导入，提示选择其他数据集', async () => {
    const original = config()
    Object.assign(original, { strategy: 'auto', taskMatcher: 'semantic', embeddingChannel: 'vector', embeddingModel: 'embed-model' })
    mocks.load.mockResolvedValue(original)
    mocks.options.mockResolvedValue({ classifiers: [], embeddings: [{ id: 'vector', name: '向量渠道' }], tasks: [] } satisfies IntentOptions)
    const state = await setup()
    await (state.prepareSemanticIndex as () => Promise<void>)()
    expect(value<string>(state, 'error')).toContain('未找到内置意图数据集')
    expect(mocks.datasetBuiltin).not.toHaveBeenCalled()
    expect(mocks.datasetSave).not.toHaveBeenCalled()
    expect(mocks.buildIndex).not.toHaveBeenCalled()
  })
  it('切换已有 Embeddings 渠道带出默认模型，缺少模型时仍阻断保存', async () => {
    const original = config()
    original.enabled = true
    original.strategy = 'auto'
    original.taskMatcher = 'semantic'
    mocks.load.mockResolvedValue(original)
    mocks.options.mockResolvedValue({ classifiers: [], embeddings: [{ id: 'vector', name: '向量渠道', defaultModel: 'embed-model' }], tasks: [] } satisfies IntentOptions)
    const state = await setup()
    expect(value<string>(state, 'vectorReason')).toContain('Embeddings')
    ;(state.useRemoteEmbedding as () => void)()
    expect(value<IntentConfig>(state, 'draft').embeddingModel).toBe('embed-model')
    expect(value<string>(state, 'vectorReason')).toBe('')
    value<IntentConfig>(state, 'draft').embeddingModel = ''
    await (state.save as () => Promise<void>)()
    expect(mocks.save).not.toHaveBeenCalled()
  })
  it('共享 Kilo 保留旧 ID 所属协议，其他渠道提供分组和模型列表', async () => {
    const original = config()
    original.classifier = 'llm'
    original.classifierChannel = 'builtin-kilo-chat'
    original.classifierModel = 'saved-model'
    mocks.load.mockResolvedValue(original)
    mocks.options.mockResolvedValue({ classifiers: [
      { id: 'builtin-kilo-messages', name: 'Kilo 免费池', kind: 'messages', builtin: true, sharedKey: 'kilo', defaultModel: 'free-model', models: ['free-model'] },
      { id: 'chat', name: '同名', kind: 'chat', builtin: false, sharedKey: '', defaultModel: 'chat-default', models: ['chat-default', 'other-model'] },
      { id: 'decision', name: '同名', kind: 'decisions', builtin: false, sharedKey: '', defaultModel: 'decision-model', models: ['decision-model'] }
    ], embeddings: [], tasks: [] } satisfies IntentOptions)
    const state = await setup()
    expect(value<{ id: string; name: string }[]>(state, 'classifierOptions')).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: 'builtin-kilo-chat', kind: 'chat', name: 'Kilo 免费池（四个入口共享）' }), expect.objectContaining({ name: '同名', kind: 'chat' })
    ]))
    expect(value<{ id: string }[]>(state, 'classifierOptions')).toHaveLength(2)
    expect(value<IntentConfig>(state, 'draft').classifierModel).toBe('saved-model')
    ;(state.selectClassifierChannel as (id: string) => void)('chat')
    expect(value<IntentConfig>(state, 'draft').classifierModel).toBe('chat-default')
    expect(value<IntentOptions['classifiers']>(state, 'classifierOptions').find(option => option.id === 'chat')?.models).toEqual(['chat-default', 'other-model'])
    ;(state.selectClassifier as (type: IntentConfig['classifier']) => void)('decisions')
    expect(value<{ id: string }[]>(state, 'classifierOptions').map(option => option.id)).toEqual(['decision'])
    await (state.save as () => Promise<void>)()
    expect(mocks.save).toHaveBeenCalledWith('responses', expect.objectContaining({
      classifier: 'decisions', classifierChannel: 'decision', classifierModel: 'decision-model'
    }))
    expect(original.classifierChannel).toBe('builtin-kilo-chat')
  })
  it('没有 Decisions 渠道时保留显式选项并阻断启用保存', async () => {
    const original = config()
    original.enabled = true
    original.strategy = 'auto'
    original.classifier = 'decisions'
    original.classifierModel = 'decision-model'
    mocks.load.mockResolvedValue(original)
    const state = await setup()
    expect(value<string>(state, 'classifierReason')).toContain('Decisions')
    await (state.save as () => Promise<void>)()
    expect(mocks.save).not.toHaveBeenCalled()
    expect(value<IntentConfig>(state, 'draft').classifier).toBe('decisions')
  })
  it('混合模式只有四种组合，选择组合不会改变固定顺序', async () => {
    const state = await setup()
    ;(state.simpleMode as Ref<string>).value = 'hybrid'
    ;(state.selectHybridCombination as (value: string) => void)('builtin,semantic,assistant')
    expect(value<IntentConfig>(state, 'draft')).toMatchObject({ recognitionMode: 'hybrid', hybridMatchers: ['builtin', 'semantic', 'assistant'], classifier: 'llm' })
    ;(state.selectHybridCombination as (value: string) => void)('assistant,semantic')
    expect(value<IntentConfig>(state, 'draft').hybridMatchers).toEqual(['builtin', 'semantic', 'assistant'])
  })
  it('重建已有内置数据集时保留新模型，不切回旧索引的模型', async () => {
    const original = config()
    Object.assign(original, { strategy: 'auto', taskMatcher: 'semantic', embeddingChannel: 'vector', embeddingModel: 'new-model' })
    mocks.load.mockResolvedValue(original)
    mocks.options.mockResolvedValue({ classifiers: [], embeddings: [{ id: 'vector', name: '向量渠道' }], tasks: [] } satisfies IntentOptions)
    mocks.datasetList.mockResolvedValue([{ id: 'starter-intents-v2', kind: 'intent', approved: 144, indexes: [{ profile: { model: 'old-model', source: 'remote', channel: 'old-channel' } }] }])
    const state = await setup()
    await (state.prepareSemanticIndex as () => Promise<void>)()
    expect(mocks.buildIndex).toHaveBeenCalledWith('responses', expect.objectContaining({ embeddingModel: 'new-model', embeddingChannel: 'vector', datasetIds: ['starter-intents-v2'] }))
    expect(value<IntentConfig>(state, 'draft').embeddingModel).toBe('new-model')
  })
})
