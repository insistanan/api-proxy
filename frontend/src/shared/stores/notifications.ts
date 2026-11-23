import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import type { ToastType } from '@/shared/composables/useToast'

export type NotificationLevel = 'notice' | 'warning'

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
}

const STORAGE_KEY = 'api-proxy-notifications'
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
  const storageError = computed(() => [readError.value, writeError.value].filter(Boolean).join('；'))
  const unreadCount = computed(() => messages.value.filter(item => item.readAt === null).length)
  const describeError = (error: unknown) => error instanceof Error ? error.message : String(error)

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
    const retained = storedMessages.value.filter(item => item.readAt === null || now - item.readAt < READ_RETENTION_MS)
    if (retained.length === storedMessages.value.length) return
    storedMessages.value = retained
    persist()
  }

  function addMessage(message: string, type: ToastType, options: { source?: string; key?: string; link?: string; level: NotificationLevel }) {
    pruneRead()
    if (options.key) {
      const existing = messages.value.find(item => item.key === options.key && item.message === message && item.type === type)
      if (existing) return existing.id
    }
    const item: AppNotification = {
      id: `${Date.now()}-${Math.random().toString(36).slice(2)}`, message, type,
      createdAt: Date.now(), readAt: null, ...options, source: options.source || '操作消息'
    }
    storedMessages.value.unshift(item)
    persist()
    return item.id
  }

  function setRead(id: string, read: boolean) {
    const item = messages.value.find(message => message.id === id)
    if (!item) return
    item.readAt = read ? (item.readAt ?? Date.now()) : null
    persist()
  }

  function markAllRead() {
    const now = Date.now()
    messages.value.forEach(item => { item.readAt ??= now })
    persist()
  }

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
      })) throw new Error('本地消息记录格式无效')
      storedMessages.value = parsed as AppNotification[]
      pruneRead()
    }
  } catch (error) {
    readError.value = `消息记录读取失败：${describeError(error)}`
  }

  return { messages, storageError, unreadCount, addMessage, setRead, markAllRead, pruneRead }
})
