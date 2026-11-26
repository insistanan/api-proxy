import { effectScope } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import { useConfirm } from '@/shared/composables/useConfirm'

describe('确认请求结算', () => {
  it('替换与销毁均取消等待者，确认仅执行当前操作', async () => {
    const scope = effectScope()
    const confirmation = scope.run(useConfirm)!
    const previous = vi.fn()
    const action = vi.fn()
    confirmation.askConfirm({ message: '第一项', action: vi.fn(), onCancel: previous })
    confirmation.askConfirm({ message: '第二项', action })
    expect(previous).toHaveBeenCalledTimes(1)
    await confirmation.acceptConfirm()
    expect(action).toHaveBeenCalledTimes(1)
    const cancelled = vi.fn()
    confirmation.askConfirm({ message: '卸载中的导航', action: vi.fn(), onCancel: cancelled })
    scope.stop()
    expect(cancelled).toHaveBeenCalledTimes(1)
    expect(confirmation.pending.value).toBeNull()
  })
})
