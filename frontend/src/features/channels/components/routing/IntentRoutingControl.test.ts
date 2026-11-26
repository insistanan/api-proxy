import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import IntentRoutingControl from '@/features/channels/components/routing/IntentRoutingControl.vue'
import type { IntentConfig, IntentOptions, IntentPreparation, LocalStatus } from '@/features/channels/services/intent'
import type { EmbeddingProfile } from '@/types'

const mocks = vi.hoisted(() => ({
  load: vi.fn(),
  save: vi.fn(),
  options: vi.fn(),
  local: vi.fn(),
  preview: vi.fn(),
  feedback: vi.fn(),
  buildIndex: vi.fn(),
  indexStatus: vi.fn(),
  status: vi.fn(),
  preparation: vi.fn(),
  prepare: vi.fn(),
  cancelPreparation: vi.fn()
}))
vi.mock('@/shared/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))
vi.mock('@/features/channels/stores/channel', () => ({
  useChannelStore: () => ({
    channelPoolsMap: {
      responses: [
        { id: 'language', name: '语言组' },
        { id: 'code', name: '代码组' },
        { id: 'builtin-free', name: '免费分组' }
      ]
    }
  })
}))
vi.mock('@/features/channels/stores/intent', () => ({
  useIntentStore: () => ({ configs: {}, load: mocks.load, save: mocks.save })
}))
vi.mock('@/features/channels/services/intent', () => ({ intentApi: mocks, intentPreviewFailure: () => undefined }))
vi.mock('@/shared/composables/useToast', () => ({ useToast: () => ({ showToast: vi.fn() }) }))

const config = (): IntentConfig => ({
  enabled: false,
  strategy: 'regex',
  classifier: 'heuristic',
  classifierChannel: '',
  classifierModel: '',
  embeddingChannel: '',
  embeddingModel: '',
  embeddingSource: 'remote',
  queryPrefix: '',
  examplePrefix: '',
  threshold: 0.75,
  timeoutMs: 2000,
  minResidenceSeconds: 300,
  minTurns: 4,
  switchMargin: 0.15,
  unmatched: 'original',
  fallbackPoolId: '',
  failurePolicy: 'error',
  observeOnly: true,
  routes: []
})
const localStatus = (): LocalStatus => ({
  installed: false,
  running: false,
  ready: false,
  busy: false,
  error: '',
  platform: 'windows/amd64',
  installPath: 'D:\\gateway\\.config\\intent-embedding',
  presetsUrl: 'https://example.invalid/embedding-presets.json',
  phase: 'idle',
  manifest: {
    runtimeUrl: '',
    runtimeSha256: '',
    executable: '',
    modelUrl: '',
    modelSha256: '',
    modelName: '',
    pooling: 'mean'
  }
})
type State = Record<string, unknown>
const value = <T>(state: State, name: string) => (state[name] as Ref<T>).value
const setup = async () => {
  let state!: State
  const component = IntentRoutingControl as unknown as { setup: (props: object, context: SetupContext) => State }
  await renderToString(
    createSSRApp({
      setup(_props, context) {
        state = component.setup({ protocol: 'responses' }, { ...context, expose: vi.fn() })
        return () => null
      }
    })
  )
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
  mocks.status.mockResolvedValue({ state: 'disabled', reason: '未启用' })
  mocks.preparation.mockResolvedValue({ id: '', protocol: '', running: false, phase: '', error: '' })
  mocks.save.mockResolvedValue(undefined)
})

describe('意图匹配编辑器', () => {
  it('合并验证旧分类迁移、分组草稿隔离、预览和排序', async () => {
    const original = config()
    original.classifier = 'systemone'
    original.groups = [
      { poolId: 'code', tasks: ['code_generation'], tiers: [1], cost: 0 },
      { poolId: 'language', tasks: ['writing'], tiers: [1], cost: 0 }
    ]
    mocks.load.mockResolvedValue(original)
    mocks.preview.mockResolvedValue({ task: 'debugging', poolId: 'code', tier: 2, reason: '匹配', durationMs: 1 })
    const state = await setup()
    expect(value<{ id: string }[]>(state, 'pools').map(pool => pool.id)).toContain('builtin-free')
    expect(value<IntentConfig>(state, 'draft').classifier).toBe('decisions')
    expect(original.classifier).toBe('systemone')
    ;(state.groups as Ref<IntentConfig['groups']>).value = [
      { poolId: 'builtin-free', tasks: ['architecture'], tiers: [2], cost: 0 }
    ]
    ;(state.embeddingProfile as Ref<EmbeddingProfile>).value = {
      source: 'remote',
      channel: 'embedding',
      model: 'vector',
      modelVersion: 'v1',
      queryPrefix: 'query: ',
      examplePrefix: 'passage: ',
      timeoutMs: 5000
    }
    expect(original.groups).toEqual([
      { poolId: 'code', tasks: ['code_generation'], tiers: [1], cost: 0 },
      { poolId: 'language', tasks: ['writing'], tiers: [1], cost: 0 }
    ])
    ;(state.previewText as Ref<string>).value = '排查并发死锁'
    await (state.preview as () => Promise<void>)()
    await (state.save as () => Promise<void>)()
    expect(mocks.preview).toHaveBeenCalledOnce()
    expect(mocks.feedback).not.toHaveBeenCalled()
    expect(mocks.save).toHaveBeenCalledWith(
      'responses',
      expect.objectContaining({
        classifier: 'decisions',
        groups: [{ poolId: 'builtin-free', tasks: ['architecture'], tiers: [2], cost: 0 }],
        embeddingModel: 'vector',
        queryPrefix: 'query: ',
        timeoutMs: 5000
      })
    )
  })

  it('校验分组能力完整性并保留当前渠道顺序', async () => {
    const original = config()
    original.strategy = 'auto'
    original.groups = [
      { poolId: 'code', tasks: ['code_generation'], tiers: [1], cost: 0 },
      { poolId: 'language', tasks: ['writing'], tiers: [1], cost: 0 }
    ]
    mocks.load.mockResolvedValue(original)
    const state = await setup()
    ;(state.groups as Ref<IntentConfig['groups']>).value = [
      { poolId: 'code', tasks: ['debugging'], tiers: [], cost: 0 }
    ]
    await (state.save as () => Promise<void>)()
    expect(mocks.save).not.toHaveBeenCalled()
    expect(value<string>(state, 'error')).toContain('代码组')
    ;(state.groups as Ref<IntentConfig['groups']>).value = original.groups
    await (state.save as () => Promise<void>)()
    expect(mocks.save.mock.calls[0]![1].groups.map((group: { poolId: string }) => group.poolId)).toEqual([
      'language',
      'code'
    ])
    expect(original.groups.map(group => group.poolId)).toEqual(['code', 'language'])
  })

  it('本地向量模型未就绪时阻断启用，就绪且索引匹配后允许保存', async () => {
    const original = config()
    Object.assign(original, {
      enabled: true,
      strategy: 'auto',
      taskMatcher: 'semantic',
      embeddingSource: 'local',
      embeddingModel: 'vector',
      datasetIds: ['intent']
    })
    original.groups = [{ poolId: 'language', tasks: ['translation'], tiers: [0, 1, 2], cost: 0 }]
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

  it('明确采用内置样本后后台准备，完成时只合并依赖字段并保留分组草稿', async () => {
    const original = config()
    Object.assign(original, {
      strategy: 'auto',
      taskMatcher: 'semantic',
      embeddingChannel: 'vector',
      embeddingModel: 'new-model'
    })
    mocks.load.mockResolvedValue(original)
    mocks.options.mockResolvedValue({
      classifiers: [],
      embeddings: [{ id: 'vector', name: '向量渠道', defaultModel: 'embed-model' }],
      tasks: []
    } satisfies IntentOptions)
    const state = await setup()
    const pending: IntentPreparation = {
      id: 'job-1',
      protocol: 'responses',
      running: true,
      phase: 'indexing',
      error: '',
      completed: [],
      committed: false
    }
    mocks.prepare.mockResolvedValue(pending)
    await (state.prepareSemanticIndex as () => Promise<void>)()
    expect(mocks.prepare).toHaveBeenCalledWith(
      'responses',
      expect.objectContaining({ embeddingModel: 'new-model', embeddingChannel: 'vector' }),
      true
    )
    expect(value<boolean>(state, 'preparing')).toBe(true)
    expect(mocks.save).not.toHaveBeenCalled()
    value<IntentConfig>(state, 'draft').groups = [{ poolId: 'language', tasks: ['writing'], tiers: [1], cost: 0 }]
    mocks.preparation.mockResolvedValue({
      ...pending,
      running: false,
      phase: 'ready',
      config: { ...original, datasetIds: ['starter-intents-v3'] }
    })
    await (state.refreshPreparation as () => Promise<void>)()
    expect(value<IntentConfig>(state, 'draft').datasetIds).toEqual(['starter-intents-v3'])
    expect(value<IntentConfig>(state, 'draft').groups).toEqual([
      { poolId: 'language', tasks: ['writing'], tiers: [1], cost: 0 }
    ])
    expect(value<IntentConfig>(state, 'draft').embeddingModel).toBe('new-model')
    expect(original.datasetIds).toBeUndefined()
    expect(mocks.save).not.toHaveBeenCalled()
    await (state.prepareSemanticIndex as () => Promise<void>)()
    expect(mocks.prepare).toHaveBeenLastCalledWith(
      'responses',
      expect.objectContaining({ datasetIds: ['starter-intents-v3'] }),
      false
    )
  })

  it('Embeddings 默认模型和 Decisions 渠道约束阻止无效配置', async () => {
    const original = config()
    Object.assign(original, { enabled: true, strategy: 'auto', taskMatcher: 'semantic' })
    mocks.load.mockResolvedValue(original)
    mocks.options.mockResolvedValue({
      classifiers: [],
      embeddings: [{ id: 'vector', name: '向量渠道', defaultModel: 'embed-model' }],
      tasks: []
    } satisfies IntentOptions)
    const state = await setup()
    expect(value<string>(state, 'vectorReason')).toContain('Embeddings')
    ;(state.useRemoteEmbedding as () => void)()
    expect(value<IntentConfig>(state, 'draft').embeddingModel).toBe('embed-model')
    expect(value<string>(state, 'vectorReason')).toBe('')
    value<IntentConfig>(state, 'draft').embeddingModel = ''
    await (state.save as () => Promise<void>)()
    expect(mocks.save).not.toHaveBeenCalled()

    vi.clearAllMocks()
    const decisionConfig = config()
    Object.assign(decisionConfig, {
      enabled: true,
      strategy: 'auto',
      classifier: 'decisions',
      classifierModel: 'decision-model'
    })
    mocks.load.mockResolvedValue(decisionConfig)
    mocks.options.mockResolvedValue({ classifiers: [], embeddings: [], tasks: [] } satisfies IntentOptions)
    mocks.local.mockResolvedValue(localStatus())
    mocks.save.mockResolvedValue(undefined)
    const missing = await setup()
    expect(value<string>(missing, 'classifierReason')).toContain('Decisions')
    await (missing.save as () => Promise<void>)()
    expect(mocks.save).not.toHaveBeenCalled()
    expect(value<IntentConfig>(missing, 'draft').classifier).toBe('decisions')
  })

  it('共享 Kilo 保留旧协议身份，渠道选择更新默认模型和实际 classifier', async () => {
    const original = config()
    Object.assign(original, {
      classifier: 'llm',
      classifierChannel: 'builtin-kilo-chat',
      classifierModel: 'saved-model'
    })
    mocks.load.mockResolvedValue(original)
    mocks.options.mockResolvedValue({
      classifiers: [
        {
          id: 'builtin-kilo-messages',
          name: 'Kilo 免费池',
          kind: 'messages',
          builtin: true,
          sharedKey: 'kilo',
          defaultModel: 'free-model',
          models: ['free-model']
        },
        {
          id: 'chat',
          name: '同名',
          kind: 'chat',
          builtin: false,
          sharedKey: '',
          defaultModel: 'chat-default',
          models: ['chat-default', 'other-model']
        },
        {
          id: 'decision',
          name: '同名',
          kind: 'decisions',
          builtin: false,
          sharedKey: '',
          defaultModel: 'decision-model',
          models: ['decision-model']
        }
      ],
      embeddings: [],
      tasks: []
    } satisfies IntentOptions)
    const state = await setup()
    expect(value<{ id: string; name: string }[]>(state, 'classifierOptions')).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ id: 'builtin-kilo-chat', kind: 'chat', name: 'Kilo 免费池（四个入口共享）' }),
        expect.objectContaining({ name: '同名', kind: 'chat' })
      ])
    )
    expect(value<{ id: string }[]>(state, 'classifierOptions')).toHaveLength(2)
    expect(value<IntentConfig>(state, 'draft').classifierModel).toBe('saved-model')
    ;(state.selectClassifierChannel as (id: string) => void)('chat')
    expect(value<IntentConfig>(state, 'draft').classifierModel).toBe('chat-default')
    expect(
      value<IntentOptions['classifiers']>(state, 'classifierOptions').find(option => option.id === 'chat')?.models
    ).toEqual(['chat-default', 'other-model'])
    ;(state.selectClassifier as (type: IntentConfig['classifier']) => void)('decisions')
    expect(value<{ id: string }[]>(state, 'classifierOptions').map(option => option.id)).toEqual(['decision'])
    await (state.save as () => Promise<void>)()
    expect(mocks.save).toHaveBeenCalledWith(
      'responses',
      expect.objectContaining({
        classifier: 'decisions',
        classifierChannel: 'decision',
        classifierModel: 'decision-model'
      })
    )
    expect(original.classifierChannel).toBe('builtin-kilo-chat')
  })
})
