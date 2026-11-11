import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { reactive } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { api } from '@/services/api'
import { API_TABS, type ApiTab } from '@/services/channel-api'
import { usePreferencesStore } from '@/stores/preferences'
import { useRoutingModes } from './useRoutingModes'

const channel = reactive<{ activeTab: ApiTab }>({ activeTab: 'messages' })
vi.mock('@/stores/channel', () => ({ useChannelStore: () => channel }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))

beforeEach(() => {
  setActivePinia(createPinia())
  channel.activeTab = 'messages'
  vi.spyOn(api, 'getFuzzyMode').mockResolvedValue({
    fuzzyModeEnabled: true,
    fuzzyModeByProtocol: Object.fromEntries(API_TABS.map(protocol => [protocol, true])) as Record<ApiTab, boolean>
  })
  vi.spyOn(api, 'getClientDisguise').mockResolvedValue({
    claudeCodeDisguiseEnabled: false, codexDisguiseEnabled: false
  } as Awaited<ReturnType<typeof api.getClientDisguise>>)
})
afterEach(() => vi.restoreAllMocks())

describe('按协议管理故障转移', () => {
  it('保存期间切换协议，迟到响应只更新发起协议', async () => {
    let finish!: () => void
    const save = vi.spyOn(api, 'setFuzzyMode').mockReturnValue(new Promise<void>(resolve => { finish = resolve }))
    const modes = useRoutingModes(vi.fn())
    expect(modes.fuzzyUnknown.value).toBe(true)
    await modes.loadModes()
    const pending = modes.toggleFuzzyMode()
    expect(save).toHaveBeenCalledWith('messages', false)
    expect(modes.fuzzyLoading.value).toBe(true)
    channel.activeTab = 'responses'
    expect(modes.fuzzyLoading.value).toBe(false)
    expect(modes.fuzzyEnabled.value).toBe(true)
    finish()
    await pending
    expect(modes.fuzzyEnabled.value).toBe(true)
    channel.activeTab = 'messages'
    expect(modes.fuzzyEnabled.value).toBe(false)
    expect(usePreferencesStore().fuzzyModeByProtocol.responses).toBe(true)
  })

  it('加载失败时不使用持久化缓存冒充已知状态，也不允许切换', async () => {
    usePreferencesStore().setFuzzyMode('messages', true)
    vi.mocked(api.getFuzzyMode).mockRejectedValue(new Error('offline'))
    const save = vi.spyOn(api, 'setFuzzyMode')
    const modes = useRoutingModes(vi.fn())
    await modes.loadModes()
    expect(modes.fuzzyUnknown.value).toBe(true)
    await modes.toggleFuzzyMode()
    expect(save).not.toHaveBeenCalled()
  })
})
