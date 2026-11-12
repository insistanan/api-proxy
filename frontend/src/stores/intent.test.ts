import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { ApiError } from '@/services/http'
import { intentApi, type IntentConfig } from '@/services/intent'
import { useIntentStore } from './intent'

vi.mock('@/services/intent', () => ({ intentApi: { get: vi.fn(), save: vi.fn() } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))

const config = (enabled: boolean): IntentConfig => ({
  enabled, strategy: 'regex', classifier: 'heuristic', classifierChannel: '', classifierModel: '',
  embeddingChannel: '', embeddingModel: '', embeddingSource: 'remote', queryPrefix: '', examplePrefix: '',
  threshold: 0.75, timeoutMs: 2000, minResidenceSeconds: 300, minTurns: 4, switchMargin: 0.15,
  unmatched: 'original', fallbackPoolId: '', failurePolicy: 'error', observeOnly: true, routes: []
})

describe('意图配置状态', () => {
  beforeEach(() => { setActivePinia(createPinia()); vi.resetAllMocks() })

  it('迟到的读取不能覆盖刚保存的协议状态', async () => {
    let finishRead!: (value: IntentConfig) => void
    vi.mocked(intentApi.get).mockImplementation(() => new Promise(resolve => { finishRead = resolve }))
    vi.mocked(intentApi.save).mockResolvedValue(config(true))
    const store = useIntentStore()
    const pending = store.load('messages')
    await store.save('messages', config(true))
    finishRead(config(false))
    await pending
    expect(store.configs.messages?.enabled).toBe(true)
    expect(store.configs.responses).toBeUndefined()
  })

  it('未提交的失败保持旧状态，已提交错误标记为未知且不自动重试', async () => {
    vi.mocked(intentApi.get).mockResolvedValue(config(false))
    const store = useIntentStore()
    await store.load('messages')
    vi.mocked(intentApi.save).mockRejectedValueOnce(new Error('写入失败'))
    await expect(store.save('messages', config(true))).rejects.toThrow('写入失败')
    expect(store.configs.messages?.enabled).toBe(false)
    vi.mocked(intentApi.save).mockRejectedValueOnce(new ApiError('已提交', 500, undefined, 'CONFIG_COMMITTED', true))
    await expect(store.save('messages', config(true))).rejects.toThrow('已提交')
    expect(store.configs.messages).toBeUndefined()
    expect(intentApi.save).toHaveBeenCalledTimes(2)
  })

  it('保存期间拒绝重复写入，并忽略随后迟到的旧读取', async () => {
    let finishSave!: (value: IntentConfig) => void
    let finishRead!: (value: IntentConfig) => void
    vi.mocked(intentApi.save).mockImplementation(() => new Promise(resolve => { finishSave = resolve }))
    vi.mocked(intentApi.get).mockImplementation(() => new Promise(resolve => { finishRead = resolve }))
    const store = useIntentStore()
    const pendingSave = store.save('messages', config(true))
    const pendingRead = store.load('messages')
    await expect(store.save('messages', config(false))).rejects.toThrow('正在保存')
    finishSave(config(true))
    await pendingSave
    finishRead(config(false))
    await pendingRead
    expect(store.configs.messages?.enabled).toBe(true)
    expect(intentApi.save).toHaveBeenCalledTimes(1)
  })
})
