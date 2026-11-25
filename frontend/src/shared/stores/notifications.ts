import { computed, onScopeDispose, ref } from 'vue'
import { defineStore } from 'pinia'
import type { ToastType } from '@/shared/composables/useToast'

export type NotificationLevel = 'notice' | 'warning'
export type DesktopNotificationMode = 'off' | 'gate' | 'warning' | 'all'

export interface AppNotification {
  id: string
  message: string
  type: ToastType
  level?: NotificationLevel
  source: string
  createdAt: number
  readAt: number | null
  key?: string
  link?: string
  route?: string
}

const STORAGE_KEY = 'api-proxy-notifications'
const DESKTOP_STORAGE_KEY = 'api-proxy-desktop-notifications'
const READ_RETENTION_MS = 7 * 24 * 60 * 60 * 1000

export function notificationLevel(item: AppNotification): NotificationLevel {
  return item.level ?? (item.type === 'warning' || item.type === 'error' ? 'warning' : 'notice')
}

function showInCenter(item: AppNotification) {
  if (item.level) return true
  // 旧版自动归档的提醒仍保存在本地，但不再占用列表和未读数。
  return item.source !== '操作消息' && item.source !== '页面提示'
    && item.key !== 'version-check-error' && !item.key?.startsWith('gate-error:')
}

export const useNotificationStore = defineStore('notifications', () => {
  const storedMessages = ref<AppNotification[]>([])
  const messages = computed(() => storedMessages.value.filter(showInCenter))
  const readError = ref('')
  const writeError = ref('')
  const desktopMode = ref<DesktopNotificationMode>('off')
  const desktopError = ref('')
  const storageError = computed(() => [readError.value, writeError.value].filter(Boolean).join('；'))
  const unreadCount = computed(() => messages.value.filter(item => item.readAt === null).length)
  const describeError = (error: unknown) => error instanceof Error ? error.message : String(error)

  function showDesktop(message?: AppNotification) {
    if (!window.isSecureContext) throw new Error('系统通知需要 HTTPS 或 localhost；普通 HTTP 局域网地址不支持')
    if (typeof Notification === 'undefined') throw new Error('当前浏览器不支持系统通知')
    if (Notification.permission !== 'granted') throw new Error('尚未获得系统通知权限，请在浏览器站点设置中允许')
    // 锁屏和系统通知历史可被他人看到，不发送命令、凭据或消息正文。
    const notification = new Notification(message?.source === '命令审核' ? 'api-proxy · 命令审核' : 'api-proxy · 消息提醒', {
      body: message ? '有新的消息需要关注，请打开管理台消息中心查看。' : '这是一条测试通知。',
      tag: message ? `api-proxy-${message.id}` : 'api-proxy-notification-test'
    })
    notification.onclick = () => {
      window.focus()
      window.dispatchEvent(new CustomEvent('api-proxy-open-notifications', { detail: message?.id }))
      notification.close()
    }
    notification.onerror = () => { desktopError.value = '浏览器未能显示系统通知，请检查浏览器和操作系统的通知设置' }
  }

  async function setDesktopMode(mode: DesktopNotificationMode) {
    desktopError.value = ''
    try {
      if (mode !== 'off') {
        if (!window.isSecureContext) throw new Error('系统通知需要 HTTPS 或 localhost；普通 HTTP 局域网地址不支持')
        if (typeof Notification === 'undefined') throw new Error('当前浏览器不支持系统通知')
        const permission = Notification.permission === 'default' ? await Notification.requestPermission() : Notification.permission
        if (permission !== 'granted') throw new Error('未获得通知权限，请在浏览器站点设置中允许后重试')
      }
      localStorage.setItem(DESKTOP_STORAGE_KEY, mode)
      desktopMode.value = mode
    } catch (error) {
      desktopError.value = `系统通知设置失败：${describeError(error)}`
    }
  }

  function testDesktopNotification() {
    desktopError.value = ''
    try {
      showDesktop()
    } catch (error) {
      desktopError.value = `测试通知失败：${describeError(error)}`
    }
  }

  try {
    const mode = localStorage.getItem(DESKTOP_STORAGE_KEY)
    if (mode !== null) {
      if (!['off', 'gate', 'warning', 'all'].includes(mode)) throw new Error('通知范围无效')
      desktopMode.value = mode as DesktopNotificationMode
    } else if (localStorage.getItem('gate-desktop-notify') === 'enabled') {
      desktopMode.value = 'gate'
    }
  } catch (error) {
    desktopError.value = `读取系统通知设置失败：${describeError(error)}`
  }

  function persist() {
    if (readError.value) {
      writeError.value = '原消息记录读取失败，未覆盖原记录；新消息暂存在内存'
      return
    }
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(storedMessages.value))
      writeError.value = ''
    } catch (error) {
      writeError.value = `消息记录保存失败：${describeError(error)}`
    }
  }

  function pruneRead(now = Date.now()) {
    reload()
    const retained = storedMessages.value.filter(item => item.readAt === null || now - item.readAt < READ_RETENTION_MS)
    if (retained.length === storedMessages.value.length) return
    storedMessages.value = retained
    persist()
  }

  function addMessage(message: string, type: ToastType, options: { source?: string; key?: string; link?: string; route?: string; createdAt?: number; level: NotificationLevel }) {
    pruneRead()
    if (options.key) {
      const existing = messages.value.find(item => item.key === options.key && item.message === message && item.type === type)
      if (existing) {
        if (writeError.value) persist()
        return existing.id
      }
    }
    const item: AppNotification = {
      id: `${Date.now()}-${Math.random().toString(36).slice(2)}`, message, type,
      createdAt: Date.now(), readAt: null, ...options, source: options.source || '操作消息'
    }
    storedMessages.value.unshift(item)
    persist()
    const shouldNotify = desktopMode.value === 'all' ||
      (desktopMode.value === 'warning' && notificationLevel(item) === 'warning') ||
      (desktopMode.value === 'gate' && item.key?.startsWith('gate:'))
    if (shouldNotify) {
      try {
        showDesktop(item)
      } catch (error) {
        desktopError.value = `系统通知发送失败：${describeError(error)}`
      }
    }
    return item.id
  }

  function setRead(id: string, read: boolean) {
    reload()
    const item = messages.value.find(message => message.id === id)
    if (!item) return
    item.readAt = read ? (item.readAt ?? Date.now()) : null
    persist()
  }

  function markAllRead() {
    reload()
    const now = Date.now()
    messages.value.forEach(item => { item.readAt ??= now })
    persist()
  }

  function reload() {
    // 写入失败的消息仍留在当前页，不能被其他标签页的旧快照覆盖。
    if (writeError.value) return
    try {
      const raw = localStorage.getItem(STORAGE_KEY)
      if (raw) {
        const parsed: unknown = JSON.parse(raw)
        if (!Array.isArray(parsed) || !parsed.every((item: unknown) => {
          if (!item || typeof item !== 'object') return false
          const row = item as Record<string, unknown>
          return typeof row.id === 'string' && typeof row.message === 'string' && typeof row.source === 'string'
            && typeof row.createdAt === 'number' && Number.isFinite(row.createdAt)
            && (row.readAt === null || (typeof row.readAt === 'number' && Number.isFinite(row.readAt)))
            && ['success', 'error', 'warning', 'info'].includes(String(row.type))
            && (row.level === undefined || row.level === 'notice' || row.level === 'warning')
            && (row.key === undefined || typeof row.key === 'string')
            && (row.link === undefined || typeof row.link === 'string')
            && (row.route === undefined || (typeof row.route === 'string' && row.route.startsWith('/') && !row.route.startsWith('//')))
        })) throw new Error('本地消息记录格式无效')
        storedMessages.value = parsed as AppNotification[]
      } else {
        storedMessages.value = []
      }
      readError.value = ''
    } catch (error) {
      readError.value = `消息记录读取失败：${describeError(error)}`
    }
  }

  function storageChanged(event: StorageEvent) {
    if (event.key === STORAGE_KEY || event.key === null) reload()
  }
  pruneRead()
  window.addEventListener('storage', storageChanged)
  onScopeDispose(() => window.removeEventListener('storage', storageChanged))

  return { messages, storageError, unreadCount, addMessage, setRead, markAllRead, pruneRead, desktopMode, desktopError, setDesktopMode, testDesktopNotification }
})
