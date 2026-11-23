// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useNotificationStore } from '@/shared/stores/notifications'
import { useToast } from '@/shared/composables/useToast'

describe('消息中心', () => {
  afterEach(() => vi.restoreAllMocks())
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('重载后保留消息与已读状态，相同通知不重复入库', () => {
    const store = useNotificationStore()
    const id = store.addMessage('任务完成', 'success', { level: 'notice', key: 'done' })
    expect(store.unreadCount).toBe(1)
    store.setRead(id, true)
    expect(store.addMessage('任务完成', 'success', { level: 'notice', key: 'done' })).toBe(id)
    setActivePinia(createPinia())
    const restored = useNotificationStore()
    expect(restored.messages).toHaveLength(1)
    expect(restored.unreadCount).toBe(0)
    expect(restored.messages[0].source).toBe('操作消息')
    restored.setRead(id, false)
    expect(restored.unreadCount).toBe(1)
  })

  it('普通气泡和旧页面提示不占未读数，显式警告可以归档', () => {
    localStorage.setItem('api-proxy-notifications', JSON.stringify([
      { id: 'old', message: '保存完成', type: 'success', source: '页面提示', createdAt: Date.now(), readAt: null }
    ]))
    const store = useNotificationStore()
    const { showToast } = useToast()
    vi.useFakeTimers()
    try {
      showToast('复制成功', 'success')
      showToast('请求失败', 'error')
      showToast('任务有失败项', 'warning', { level: 'warning' })
      expect(store.unreadCount).toBe(1)
      expect(store.messages.map(item => item.message)).toEqual(['任务有失败项'])
      vi.runAllTimers()
    } finally {
      vi.useRealTimers()
    }
  })

  it('从标记已读起计时满 7 天移除，未读消息保留', () => {
    const store = useNotificationStore()
    const first = store.addMessage('很早的未读消息', 'info', { level: 'notice' })
    const second = store.addMessage('稍后阅读的消息', 'info', { level: 'notice' })
    const now = Date.now()
    store.messages.forEach(item => { item.createdAt = now - 30 * 24 * 60 * 60 * 1000 })
    store.setRead(second, true)
    const readAt = store.messages.find(item => item.id === second)!.readAt!
    store.pruneRead(readAt + 7 * 24 * 60 * 60 * 1000 - 1)
    expect(store.messages).toHaveLength(2)
    store.pruneRead(readAt + 7 * 24 * 60 * 60 * 1000)
    expect(store.messages.map(item => item.id)).toEqual([first])
  })

  it('损坏的本地数据明确报错', () => {
    localStorage.setItem('api-proxy-notifications', '{invalid')
    const store = useNotificationStore()
    expect(store.storageError).toContain('消息记录读取失败')
    store.addMessage('新消息', 'info', { level: 'notice' })
    expect(localStorage.getItem('api-proxy-notifications')).toBe('{invalid')
    expect(store.storageError).toContain('未覆盖原记录')
  })

  it('持久化失败明确报告，当前消息仍可阅读', () => {
    const store = useNotificationStore()
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('存储空间已满') })
    const id = store.addMessage('需要保留的提示', 'warning', { level: 'warning' })
    expect(store.messages[0].id).toBe(id)
    expect(store.unreadCount).toBe(1)
    expect(store.storageError).toContain('消息记录保存失败：存储空间已满')
  })
})
