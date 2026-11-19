import { createPinia, setActivePinia } from 'pinia'
import { createSSRApp, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { describe, expect, it, vi } from 'vitest'
import { api } from '@/api'
import { useLogsStore } from '@/features/observability'
import GatewaySettingsSection from '@/features/settings/components/GatewaySettingsSection.vue'

vi.mock('@/shared/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))

describe('请求与日志设置', () => {
  it('取消不删除日志；确认才清空并刷新存储状态', async () => {
    setActivePinia(createPinia())
    const purge = vi.spyOn(api, 'purgeLogs').mockResolvedValue({
      success: true, purged: { deletedAppLogs: 2, deletedTrafficLogs: 3, deletedRequestLogs: 4 }
    })
    const refresh = vi.spyOn(api, 'getLogStorageStatus').mockResolvedValue({
      policy: { runtimeDays: 2, callDays: 2, bodyDays: 2, budgetMB: 0 },
      usedBytes: 0, freeBytes: 0, databaseBytes: 0, walBytes: 0, bodyCapturePaused: false,
      maintenance: { deletedRows: 0, expiredBodies: 0 }, budgetEvents: []
    })
    try {
      const logs = useLogsStore()
      logs.selectedKey = 'old-conversation'
      let state!: { confirmClearLogs: () => void; cancelConfirm: () => void; acceptConfirm: () => Promise<void>; purgeNotice: Ref<{ message: string } | null> }
      const section = GatewaySettingsSection as unknown as { setup: (props: object, context: SetupContext) => typeof state }
      await renderToString(createSSRApp({
        setup(_props, context) {
          state = section.setup({ editor: {} }, context)
          return () => null
        }
      }))
      state.confirmClearLogs()
      state.cancelConfirm()
      expect(purge).not.toHaveBeenCalled()
      expect(logs.selectedKey).toBe('old-conversation')

      state.confirmClearLogs()
      await state.acceptConfirm()
      expect(purge).toHaveBeenCalledExactlyOnceWith({ scope: 'all' })
      expect(refresh).toHaveBeenCalledOnce()
      expect(logs.selectedKey).toBe('')
      expect(state.purgeNotice.value?.message).toContain('9 条日志记录')
    } finally {
      vi.restoreAllMocks()
    }
  })
})
