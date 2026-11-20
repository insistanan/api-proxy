import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useJsonViewerDrawer } from '@/shared/composables/useJsonViewerDrawer'

const drawer = useJsonViewerDrawer()
beforeEach(() => { vi.useFakeTimers(); drawer.close() })
afterEach(() => { drawer.close(); vi.useRealTimers() })

// 点击打开与显式关闭取代旧悬浮计时、跨区延迟关闭和固定状态用例。
describe('全局 JSON 抽屉显式查看', () => {
  it('明确打开后持续展示，关闭时释放当前正文且不会延迟重开', () => {
    drawer.openSource({ data: '{"ok":true}', title: '请求正文' })
    vi.runAllTimers()
    expect(drawer.isOpen.value).toBe(true)
    expect(drawer.source.value?.title).toBe('请求正文')
    drawer.close()
    vi.runAllTimers()
    expect(drawer.isOpen.value).toBe(false)
    expect(drawer.source.value).toBeNull()
  })
})
