import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import LocalEmbeddingSettings from '@/features/channels/components/routing/LocalEmbeddingSettings.vue'
import type { LocalPresets, LocalStatus } from '@/features/channels/services/intent'

const api = vi.hoisted(() => ({ local: vi.fn(), presets: vi.fn(), installPreset: vi.fn(), localAction: vi.fn(), importResource: vi.fn(), setPresetsUrl: vi.fn(), cancelPreparation: vi.fn() }))
vi.mock('@/features/channels/services/intent', () => ({ intentApi: api }))
type State = Record<string, unknown>
const value = <T>(state: State, key: string) => (state[key] as Ref<T>).value
const status = (): LocalStatus => ({ installed: false, running: false, ready: false, busy: false, error: '', platform: 'windows/amd64', installPath: 'D:\\gateway\\.config\\intent-embedding', presetsUrl: 'https://example.invalid/presets.json', phase: 'idle', manifest: { runtimeUrl: '', runtimeSha256: '', executable: '', modelUrl: '', modelSha256: '', modelName: '', pooling: 'mean' } })
const catalog = (): LocalPresets => ({ version: 1, presets: [], installPath: status().installPath, presetsUrl: status().presetsUrl, defaultPresetsUrl: 'https://example.invalid/default-presets.json', platform: 'windows/amd64', error: '', stale: false })
async function setup() {
  let state!: State
  const component = LocalEmbeddingSettings as unknown as { setup: (props: object, context: SetupContext) => State }
  await renderToString(createSSRApp({ setup(_props, context) { state = component.setup({}, { ...context, expose: vi.fn() }); return () => null } }))
  await (state.refresh as () => Promise<void>)()
  await (state.loadPresets as () => Promise<void>)()
  return state
}
beforeEach(() => { vi.resetAllMocks(); api.local.mockResolvedValue(status()); api.presets.mockResolvedValue(catalog()) })

describe('本地向量安装的清单与阶段状态', () => {
  it('后台接口立即返回后继续显示忙碌，随后刷新真实完成状态', async () => {
    api.presets.mockResolvedValue({ ...catalog(), presets: [{ id: 'cpu', name: 'CPU', description: '测试条目', sizeBytes: 100 }] })
    api.installPreset.mockResolvedValue({ ...status(), busy: true, phase: 'queued' })
    const state = await setup()
    await (state.run as (action: string) => Promise<void>)('preset')
    expect(value<boolean>(state, 'busy')).toBe(true)
    api.local.mockResolvedValue({ ...status(), installed: true, running: true, ready: true, phase: 'ready' })
    await (state.refresh as () => Promise<void>)()
    expect(value<boolean>(state, 'busy')).toBe(false)
    expect(value<LocalStatus>(state, 'status').ready).toBe(true)
  })
  it('离线导入发送文件流和预设 ID，失败明确显示并清空文件选择', async () => {
    api.presets.mockResolvedValue({ ...catalog(), presets: [{ id: 'cpu', name: 'CPU', description: '测试条目', sizeBytes: 100 }] })
    api.importResource.mockRejectedValue(new Error('SHA-256 校验不匹配'))
    const state = await setup()
    const file = { size: 8, name: 'test.gguf' } as File
    const input = { files: [file], value: 'test.gguf' }
    await (state.importFile as (event: Event, resource: string) => Promise<void>)({ target: input } as unknown as Event, 'model')
    expect(api.importResource).toHaveBeenCalledWith('cpu', 'model', file)
    expect(value<string>(state, 'actionError')).toContain('SHA-256')
    expect(input.value).toBe('')
    expect(value<boolean>(state, 'busy')).toBe(false)
  })
  it('空清单没有可安装选项，刷新错误显式保留且不能执行旧预设', async () => {
    const state = await setup()
    expect(value<LocalPresets>(state, 'catalog').presets).toEqual([])
    expect(value<LocalStatus>(state, 'status').installPath).toBe(status().installPath)
    await (state.run as (action: string) => Promise<void>)('preset')
    expect(api.installPreset).not.toHaveBeenCalled()
    const stale = catalog()
    stale.presets = [{ id: 'cpu', name: 'CPU', description: '测试条目', sizeBytes: 100 }]
    stale.stale = true
    stale.error = '拉取发布预设清单失败: HTTP 502'
    api.presets.mockResolvedValue(stale)
    await (state.loadPresets as () => Promise<void>)()
    ;(state.presetId as Ref<string>).value = 'cpu'
    await (state.run as (action: string) => Promise<void>)('preset')
    expect(value<LocalPresets>(state, 'catalog').error).toContain('HTTP 502')
    expect(api.installPreset).not.toHaveBeenCalled()
  })
  it('安装只发送预设 ID，等待期间可以刷新下载与预热阶段，完成后以真实 ready 状态结束', async () => {
    const presets = catalog()
    presets.presets = [{ id: 'cpu', name: 'CPU', description: '测试条目', sizeBytes: 100 }]
    api.presets.mockResolvedValue(presets)
    const state = await setup()
    ;(state.presetId as Ref<string>).value = 'cpu'
    let finish!: (value: LocalStatus) => void
    api.installPreset.mockImplementation(() => new Promise<LocalStatus>(resolve => { finish = resolve }))
    const pending = (state.run as (action: string) => Promise<void>)('preset')
    expect(api.installPreset).toHaveBeenCalledWith('cpu')
    expect(value<boolean>(state, 'busy')).toBe(true)
    const downloading = status()
    downloading.busy = true
    downloading.phase = 'downloading_model'
    api.local.mockResolvedValue(downloading)
    await (state.refresh as () => Promise<void>)()
    expect(value<string>(state, 'phaseText')).toContain('下载向量模型')
    api.local.mockResolvedValue({ ...downloading, phase: 'warming' })
    await (state.refresh as () => Promise<void>)()
    expect(value<string>(state, 'phaseText')).toContain('预热')
    const ready = status()
    ready.installed = ready.running = ready.ready = true
    ready.phase = 'ready'
    finish(ready)
    await pending
    expect(value<boolean>(state, 'busy')).toBe(false)
    expect(value<LocalStatus>(state, 'status').ready).toBe(true)
    expect(value<string>(state, 'phaseText')).toContain('已就绪')
  })
  it('启动失败后保留错误，后续状态刷新不能掩盖失败', async () => {
    const state = await setup()
    api.localAction.mockRejectedValue(new Error('启动本地助手失败: 运行时不可执行'))
    await (state.run as (action: string) => Promise<void>)('start')
    await (state.refresh as () => Promise<void>)()
    expect(value<string>(state, 'actionError')).toContain('运行时不可执行')
    expect(value<LocalStatus>(state, 'status').ready).toBe(false)
  })
  it('清单刷新失败保留错误，重试成功后可安装推荐模型', async () => {
    const state = await setup()
    api.presets.mockRejectedValueOnce(new Error('读取推荐清单失败'))
    await (state.loadPresets as () => Promise<void>)()
    expect(value<string>(state, 'catalogError')).toBe('读取推荐清单失败')
    expect(value<boolean>(state, 'catalogLoading')).toBe(false)
    const presets = catalog()
    presets.presets = [{ id: 'cpu', name: 'CPU', description: '测试条目', sizeBytes: 100 }]
    api.presets.mockResolvedValue(presets)
    api.installPreset.mockResolvedValue({ ...status(), installed: true, running: true, ready: true, phase: 'ready' })
    await (state.loadPresets as () => Promise<void>)()
    expect(value<string>(state, 'catalogError')).toBe('')
    await (state.run as (action: string) => Promise<void>)('preset')
    expect(api.installPreset).toHaveBeenCalledWith('cpu')
  })
})
