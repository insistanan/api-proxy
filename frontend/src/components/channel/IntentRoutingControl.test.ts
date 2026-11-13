import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import IntentRoutingControl from './IntentRoutingControl.vue'
import type { IntentConfig, IntentOptions } from '@/services/intent'
import type { EmbeddingProfile } from '@/services/datasets'

const mocks = vi.hoisted(() => ({ load: vi.fn(), save: vi.fn(), options: vi.fn(), preview: vi.fn(), feedback: vi.fn() }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))
vi.mock('@/stores/channel', () => ({ useChannelStore: () => ({ channelPoolsMap: { responses: [{ id: 'language', name: '语言组' }, { id: 'code', name: '代码组' }, { id: 'builtin-free', name: '免费分组' }] } }) }))
vi.mock('@/stores/intent', () => ({ useIntentStore: () => ({ configs: {}, load: mocks.load, save: mocks.save }) }))
vi.mock('@/services/intent', () => ({ intentApi: { options: mocks.options, preview: mocks.preview, feedback: mocks.feedback }, intentErrorText: (error: Error) => error.message, intentPreviewFailure: () => undefined }))
vi.mock('@/composables/useToast', () => ({ useToast: () => ({ showToast: vi.fn() }) }))

const config = (): IntentConfig => ({ enabled: false, strategy: 'regex', classifier: 'heuristic', classifierChannel: '', classifierModel: '', embeddingChannel: '', embeddingModel: '', embeddingSource: 'remote', queryPrefix: '', examplePrefix: '', threshold: 0.75, timeoutMs: 2000, minResidenceSeconds: 300, minTurns: 4, switchMargin: 0.15, unmatched: 'original', fallbackPoolId: '', failurePolicy: 'error', observeOnly: true, routes: [] })
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
    expect(value<string>(state, 'tab')).toBe('groups')
    expect(value<string>(state, 'error')).toContain('代码组')
  })
})
