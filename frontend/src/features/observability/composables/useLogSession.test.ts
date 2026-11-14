import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createSSRApp, h } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { api } from '@/api'
import { createLogSession, provideLogSession, useLogSession } from '@/features/observability/composables/useLogSession'
import { useLogsStore } from '@/features/observability/stores/logs'

vi.mock('@/shared/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))
beforeEach(() => { setActivePinia(createPinia()); vi.restoreAllMocks() })

describe('对话轨迹独立状态', () => {
  it('空记录及清理局部状态均不影响全局筛选或已选会话', async () => {
    const global = useLogsStore()
    global.searchFilter = '保留搜索'; global.typeFilter = 'chat'; global.statusFilter = 'failed'
    global.selectedKey = 'chat:global'
    const local = createLogSession()
    local.selectedKey = 'responses:local'
    const fetch = vi.spyOn(api, 'getLogConversationRequests').mockResolvedValue({ logs: [] })
    await local.loadRounds({ latest: true })
    expect(fetch).toHaveBeenCalledWith(expect.objectContaining({ key: 'responses:local' }))
    expect(local.requests).toEqual([])
    expect(local.roundsError).toBe('')
    local.reset()
    expect(global.searchFilter).toBe('保留搜索')
    expect(global.typeFilter).toBe('chat')
    expect(global.statusFilter).toBe('failed')
    expect(global.selectedKey).toBe('chat:global')
  })

  it('子级详情和时间线取得注入实例，无提供者时复用全局实例', async () => {
    let provided!: ReturnType<typeof createLogSession>
    let received!: ReturnType<typeof createLogSession>
    await renderToString(createSSRApp({ setup() {
      provided = provideLogSession()
      return () => h({ setup() { received = useLogSession(); return () => null } })
    } }))
    expect(received).toBe(provided)
    await renderToString(createSSRApp({ setup() { received = useLogSession(); return () => null } }))
    expect(received).toBe(useLogsStore())
  })
})
