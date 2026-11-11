// 配置页状态回归：只执行真实 setup 操作，不启动服务或访问本机配置。
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp, ref, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { api, ApiError, type ApiTab, type OpenCodeConfig, type PiAgentProvider, type PiAgentStatus } from '@/services/api'
import OpenCodeView from './OpenCodeView.vue'
import PiAgentView from './PiAgentView.vue'

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))
vi.mock('@/composables/useChannelQuickPick', () => ({
  defaultQuickPickTypeOptions: [],
  useProxyProtocolPick: () => ({ selectedType: ref(null), defaultBaseUrl: ref('') })
}))

type State = Record<string, unknown>
interface Draft { id: string; localId: string; name: string; persisted: boolean; apiKeyAction: string; apiKeyPresent: boolean }
const value = <T>(state: State, key: string) => (state[key] as Ref<T>).value
const run = (state: State, key: string) => (state[key] as () => Promise<void>)()
const setupView = async (component: unknown): Promise<State> => {
  let state!: State
  const setup = (component as { setup: (props: object, context: SetupContext) => State }).setup
  await renderToString(createSSRApp({
    setup(props, context) {
      state = setup(props, context)
      return () => null
    }
  }))
  return state
}

const piProviders = (providers: PiAgentProvider[], revision = 'r1') => ({ revision, providers, rawExists: true })
const openConfig = (revision = 'r1', apiKeyPresent = false): OpenCodeConfig => ({
  revision, path: '/test/opencode.json', exists: true, jsonc: false, model: '', smallModel: '',
  providers: [{ id: 'local', name: 'local', protocol: 'chat', npm: '@ai-sdk/openai-compatible', baseUrl: '', apiKeyMasked: apiKeyPresent ? 'masked' : '', apiKeyPresent, headers: {}, options: {}, models: [] }]
})
beforeEach(() => {
  vi.spyOn(api, 'getPiAgentStatus').mockResolvedValue({ configDir: '/test', exists: true, writable: true, files: {} as PiAgentStatus['files'] })
  vi.spyOn(api, 'getPiAgentModelSettings').mockResolvedValue({ revision: 's1', settings: { defaultProvider: '', defaultModel: '', defaultThinkingLevel: '', enabledModels: [] } })
})
afterEach(() => { vi.restoreAllMocks() })

describe('pi 配置草稿与提交阶段', () => {
  it('保存一项只刷新该项，其他供应商草稿不丢失', async () => {
    const list = vi.spyOn(api, 'getPiAgentProviders').mockResolvedValue(piProviders([{ id: 'a', name: 'A' }, { id: 'b', name: 'B' }]))
    const update = vi.spyOn(api, 'updatePiAgentProvider').mockResolvedValue({ success: true, revision: 'r2' })
    const state = await setupView(PiAgentView)
    await run(state, 'loadAll')
    const providers = value<Draft[]>(state, 'providers')
    providers[0].name = 'A 修改'
    providers[1].name = 'B 草稿'
    const draftId = providers[1].localId
    list.mockResolvedValue(piProviders([{ id: 'a', name: 'A 已保存' }, { id: 'b', name: 'B' }], 'r2'))
    await run(state, 'saveSelectedProvider')
    expect(update).toHaveBeenCalledWith('a', expect.objectContaining({ revision: 'r1' }))
    expect(value<Draft[]>(state, 'providers').map(p => p.name)).toEqual(['A 已保存', 'B 草稿'])
    expect(value<Draft[]>(state, 'providers')[1].localId).toBe(draftId)
  })

  it('版本冲突保留当前编辑，不自动刷新覆盖草稿', async () => {
    const list = vi.spyOn(api, 'getPiAgentProviders').mockResolvedValue(piProviders([{ id: 'a', name: 'A' }]))
    vi.spyOn(api, 'updatePiAgentProvider').mockRejectedValue(new ApiError('冲突', 409, undefined, 'revision_conflict'))
    const state = await setupView(PiAgentView)
    await run(state, 'loadAll')
    value<Draft[]>(state, 'providers')[0].name = '未保存修改'
    await run(state, 'saveSelectedProvider')
    expect(list).toHaveBeenCalledTimes(1)
    expect(value<Draft[]>(state, 'providers')[0].name).toBe('未保存修改')
    expect(value<{ message: string }>(state, 'notice').message).toContain('当前编辑已保留')
  })

  it('创建成功后刷新失败，再次保存使用更新入口', async () => {
    const list = vi.spyOn(api, 'getPiAgentProviders').mockResolvedValue(piProviders([]))
    const create = vi.spyOn(api, 'createPiAgentProvider').mockResolvedValue({ success: true, revision: 'r2' })
    const update = vi.spyOn(api, 'updatePiAgentProvider').mockResolvedValue({ success: true, revision: 'r3' })
    const state = await setupView(PiAgentView)
    await run(state, 'loadAll')
    await run(state, 'addProvider')
    value<Draft[]>(state, 'providers')[0].id = 'new'
    list.mockRejectedValue(new Error('刷新失败'))
    await run(state, 'saveSelectedProvider')
    expect(value<Draft[]>(state, 'providers')[0].persisted).toBe(true)
    expect(value<{ type: string }>(state, 'notice').type).toBe('warning')
    list.mockResolvedValue(piProviders([{ id: 'new' }], 'r3'))
    await run(state, 'saveSelectedProvider')
    expect(create).toHaveBeenCalledTimes(1)
    expect(update).toHaveBeenCalledWith('new', expect.objectContaining({ revision: 'r2' }))
  })

  it('一项加载失败不发布另一项的半套配置', async () => {
    const list = vi.spyOn(api, 'getPiAgentProviders').mockResolvedValue(piProviders([{ id: 'a' }]))
    const state = await setupView(PiAgentView)
    await run(state, 'loadAll')
    value<Draft[]>(state, 'providers')[0].name = '草稿'
    list.mockResolvedValue(piProviders([{ id: 'b' }], 'external'))
    vi.mocked(api.getPiAgentModelSettings).mockRejectedValue(new Error('设置读取失败'))
    await run(state, 'loadAll')
    expect(value<Draft[]>(state, 'providers')[0].name).toBe('草稿')
    expect(value<string>(state, 'providersRevision')).toBe('r1')
    expect(value<string>(state, 'loadError')).toContain('设置读取失败')
  })
})

describe('OpenCode 配置提交阶段', () => {
  it.each([
    ['messages', 'http://localhost:9996', 'http://localhost:9996/v1', '@ai-sdk/anthropic'],
    ['responses', 'http://localhost:9996/', 'http://localhost:9996/v1', '@ai-sdk/openai'],
    ['chat', 'http://localhost:9996/v1/', 'http://localhost:9996/v1', '@ai-sdk/openai-compatible'],
    ['gemini', 'http://localhost:9996', 'http://localhost:9996/v1beta', '@ai-sdk/google']
  ])('一键连接 %s 填入 SDK 所需的版本路径', async (protocol, root, expectedUrl, npm) => {
    vi.spyOn(api, 'getOpenCodeConfig').mockResolvedValue(openConfig())
    const state = await setupView(OpenCodeView)
    await run(state, 'loadConfig')
    await (state.applyProtocol as (type: ApiTab, url: Promise<string>) => Promise<void>)(protocol as ApiTab, Promise.resolve(root))
    expect(value<OpenCodeConfig['providers']>(state, 'providers')[0]).toMatchObject({ protocol, npm, baseUrl: expectedUrl })
  })

  it('保存后刷新密钥元数据与 revision，重复点击只写一次', async () => {
    const read = vi.spyOn(api, 'getOpenCodeConfig').mockResolvedValue(openConfig())
    let resolve!: (result: { success: boolean; path: string; revision: string }) => void
    const write = vi.spyOn(api, 'saveOpenCodeConfig').mockReturnValue(new Promise(res => { resolve = res }))
    const state = await setupView(OpenCodeView)
    await run(state, 'loadConfig')
    const pending = run(state, 'saveConfig')
    await run(state, 'saveConfig')
    expect(write).toHaveBeenCalledTimes(1)
    expect(write.mock.calls[0][0].revision).toBe('r1')
    read.mockResolvedValue(openConfig('r2', true))
    resolve({ success: true, path: '/test/opencode.json', revision: 'r2' })
    await pending
    expect(value<Draft[]>(state, 'providers')[0].apiKeyPresent).toBe(true)
    expect(value<Draft[]>(state, 'providers')[0].apiKeyAction).toBe('keep')
    expect(value<OpenCodeConfig>(state, 'config').revision).toBe('r2')
  })

  it('保存成功但刷新失败显示 warning，并保留新 revision', async () => {
    const read = vi.spyOn(api, 'getOpenCodeConfig').mockResolvedValue(openConfig())
    const write = vi.spyOn(api, 'saveOpenCodeConfig').mockResolvedValue({ success: true, path: '/test/opencode.json', revision: 'r2' })
    const state = await setupView(OpenCodeView)
    await run(state, 'loadConfig')
    read.mockRejectedValue(new Error('断连'))
    await run(state, 'saveConfig')
    expect(write).toHaveBeenCalledTimes(1)
    expect(value<OpenCodeConfig>(state, 'config').revision).toBe('r2')
    expect(value<{ type: string; message: string }>(state, 'notice')).toMatchObject({ type: 'warning', message: expect.stringContaining('配置已保存，但刷新失败') })
  })
})
