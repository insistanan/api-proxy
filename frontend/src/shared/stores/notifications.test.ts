// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, disposePinia, setActivePinia } from 'pinia'
import { useNotificationStore } from '@/shared/stores/notifications'
import { useToast } from '@/shared/composables/useToast'

describe('消息中心', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })
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
    const createdAt = Date.now() - 30 * 24 * 60 * 60 * 1000
    const first = store.addMessage('很早的未读消息', 'info', { level: 'notice', createdAt })
    const second = store.addMessage('稍后阅读的消息', 'info', { level: 'notice', createdAt })
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

  it('全部消息可以通知系统，正文不会离开管理台，重复消息不再次通知', async () => {
    const shown: Array<{ title: string; options: NotificationOptions }> = []
    class BrowserNotification {
      static permission = 'granted'
      constructor(title: string, options: NotificationOptions) { shown.push({ title, options }) }
    }
    vi.stubGlobal('Notification', BrowserNotification)
    vi.stubGlobal('isSecureContext', true)
    const store = useNotificationStore()
    await store.setDesktopMode('all')
    const options = { level: 'notice' as const, source: '批量翻译', key: 'translation' }
    store.addMessage('secret-password', 'success', options)
    store.addMessage('secret-password', 'success', options)
    expect(shown).toHaveLength(1)
    expect(JSON.stringify(shown)).not.toContain('secret-password')
    expect(store.messages[0].message).toBe('secret-password')
    setActivePinia(createPinia())
    expect(useNotificationStore().desktopMode).toBe('all')
    expect(shown).toHaveLength(1)
  })

  it('通知拒绝或浏览器发送失败时明确报错，消息仍可阅读', async () => {
    class BrowserNotification {
      static permission = 'denied'
      constructor() { throw new Error('通知服务不可用') }
    }
    vi.stubGlobal('Notification', BrowserNotification)
    vi.stubGlobal('isSecureContext', true)
    const store = useNotificationStore()
    await store.setDesktopMode('all')
    expect(store.desktopMode).toBe('off')
    expect(store.desktopError).toContain('未获得通知权限')
    BrowserNotification.permission = 'granted'
    await store.setDesktopMode('warning')
    store.addMessage('任务失败', 'error', { level: 'warning' })
    expect(store.desktopError).toContain('通知服务不可用')
    expect(store.messages).toHaveLength(1)
  })

  it('旧命令通知偏好仅通知新的待审核事件', () => {
    const notify = vi.fn(function () { return {} })
    Object.defineProperty(notify, 'permission', { value: 'granted' })
    vi.stubGlobal('Notification', notify)
    vi.stubGlobal('isSecureContext', true)
    localStorage.setItem('gate-desktop-notify', 'enabled')
    const store = useNotificationStore()
    store.addMessage('任务完成', 'success', { level: 'notice' })
    store.addMessage('待确认', 'warning', { level: 'warning', source: '命令审核', key: 'gate:1' })
    store.addMessage('已决定', 'success', { level: 'notice', source: '命令审核', key: 'gate-decision:1:block' })
    expect(notify).toHaveBeenCalledTimes(1)
  })

  it('来自另一个标签页的消息不会被当前页的已读操作覆盖', () => {
    const firstPinia = createPinia(), secondPinia = createPinia()
    try {
      const first = useNotificationStore(firstPinia)
      const id = first.addMessage('先到的来信', 'info', { level: 'notice', key: 'first' })
      const second = useNotificationStore(secondPinia)
      second.addMessage('新树长大了', 'success', { level: 'notice', key: 'tree:1', route: '/usage-journal?tab=forest&event=island%3A1', createdAt: 1234567890 })
      first.setRead(id, true)
      const saved = JSON.parse(localStorage.getItem('api-proxy-notifications')!)
      expect(saved).toHaveLength(2)
      expect(saved.find((item: { key: string }) => item.key === 'tree:1')).toMatchObject({ route: '/usage-journal?tab=forest&event=island%3A1', createdAt: 1234567890, readAt: null })
      window.dispatchEvent(new StorageEvent('storage', { key: 'api-proxy-notifications' }))
      expect(second.unreadCount).toBe(1)
      expect(second.messages.find(item => item.id === id)?.readAt).not.toBeNull()
    } finally {
      disposePinia(firstPinia)
      disposePinia(secondPinia)
    }
  })

  it('写入失败后重复投递会补存原消息，不重复创建', () => {
    const store = useNotificationStore()
    const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('暂时不可写') })
    const options = { level: 'notice' as const, key: 'forest:achievement:first', route: '/usage-journal?tab=achievements&event=achievement%3Afirst' }
    const id = store.addMessage('一粒种子的远行', 'success', options)
    expect(store.storageError).not.toBe('')
    write.mockRestore()
    expect(store.addMessage('一粒种子的远行', 'success', options)).toBe(id)
    expect(store.storageError).toBe('')
    expect(JSON.parse(localStorage.getItem('api-proxy-notifications')!)).toHaveLength(1)
  })
})
