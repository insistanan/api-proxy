<template>
  <span class="relative inline-flex">
    <UiButton class="header-tool" data-header-hit icon="mdi-bell-ring" variant="text" size="small" :aria-label="`消息中心，${store.unreadCount} 条未读`" :aria-pressed="open" @click="open = true" />
    <span v-if="store.unreadCount" class="pointer-events-none absolute -top-1 -right-1 min-w-4 rounded-full px-1 text-center text-[10px] leading-4" :class="hasUnreadWarning ? 'bg-destructive text-destructive-foreground' : 'bg-primary text-primary-foreground'">{{ store.unreadCount > 99 ? '99+' : store.unreadCount }}</span>
  </span>
  <UiDialog v-model="open" drawer title="消息中心" class="notification-drawer">
    <aside class="flex h-full min-h-0 flex-col">
      <header class="flex shrink-0 items-center justify-between gap-3 border-b border-border p-4">
        <div><h2 class="font-semibold">消息中心</h2><p class="mt-1 text-xs text-muted-foreground">{{ store.unreadCount }} 条未读 · 已读消息在 7 天后自动移除</p></div>
        <UiButton icon="mdi-close" variant="text" size="small" aria-label="关闭消息中心" @click="open = false" />
      </header>
      <div class="shrink-0 border-b border-border px-4 py-3">
        <div class="flex flex-wrap items-center gap-2">
          <label for="desktop-notification-mode" class="text-sm">系统通知</label>
          <select id="desktop-notification-mode" :value="store.desktopMode" class="field-surface min-h-8 px-2 text-xs" @change="changeDesktopMode">
            <option value="off">关闭</option>
            <option value="gate">仅命令审核</option>
            <option value="warning">仅警告</option>
            <option value="all">全部消息</option>
          </select>
          <UiButton variant="text" size="small" :disabled="store.desktopMode === 'off'" @click="store.testDesktopNotification">测试通知</UiButton>
        </div>
        <p class="mt-2 text-xs text-muted-foreground">仅当前浏览器生效，需保持管理台打开并允许通知。系统弹窗不显示正文；未出现时检查系统通知权限与勿扰模式。</p>
        <UiAlert v-if="store.desktopError" type="error" class="mt-2">{{ store.desktopError }}</UiAlert>
      </div>
      <div class="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-2">
        <select v-model="levelFilter" aria-label="消息等级" class="field-surface min-h-8 px-2 text-xs">
          <option value="">所有等级</option>
          <option value="notice">通知</option>
          <option value="warning">警告</option>
        </select>
        <UiButton variant="text" size="small" :aria-pressed="unreadOnly" @click="unreadOnly = !unreadOnly">{{ unreadOnly ? '查看全部' : '只看未读' }}</UiButton>
        <UiButton variant="text" size="small" prepend-icon="mdi-check" :disabled="!store.unreadCount" @click="store.markAllRead">全部已读</UiButton>
      </div>
      <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain p-4">
        <UiAlert v-if="store.storageError" type="error" class="mb-3">{{ store.storageError }}</UiAlert>
        <p v-if="!visibleMessages.length" class="py-10 text-center text-sm text-muted-foreground">{{ unreadOnly ? '暂无未读消息' : '暂无消息' }}</p>
        <article v-for="item in visibleMessages" :key="item.id" class="mb-3 rounded-control border p-3" :class="item.readAt === null ? 'border-primary/40 bg-primary/5' : 'border-border'">
          <div class="flex items-center gap-2 text-xs text-muted-foreground">
            <UiIcon :icon="toastIcons[item.type]" :color="item.type" size="16" />
            <span :class="notificationLevel(item) === 'warning' ? 'text-warning' : undefined">{{ levelLabels[notificationLevel(item)] }}</span>
            <span>{{ item.source }}</span><time class="ml-auto">{{ formatShortDateTime(item.createdAt) }}</time>
          </div>
          <p class="mt-2 line-clamp-3 whitespace-pre-wrap text-sm leading-6 [overflow-wrap:anywhere]">{{ item.message }}</p>
          <div class="mt-2 flex flex-wrap justify-end gap-2">
            <UiButton variant="text" size="small" @click="showDetails(item)">查看详情</UiButton>
            <UiButton variant="text" size="small" @click="store.setRead(item.id, item.readAt === null)">{{ item.readAt === null ? '标为已读' : '标为未读' }}</UiButton>
          </div>
        </article>
        <UiButton v-if="filteredMessages.length > visibleLimit" variant="tonal" block @click="visibleLimit += 50">继续显示消息</UiButton>
      </div>
    </aside>
    <AppDialog v-model="detailsOpen" title="消息详情" size="lg">
      <template v-if="selectedMessage">
        <div class="mb-4 flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
          <UiIcon :icon="toastIcons[selectedMessage.type]" :color="selectedMessage.type" size="18" />
          <span>{{ levelLabels[notificationLevel(selectedMessage)] }} · {{ selectedMessage.source }}</span>
          <span>{{ selectedMessage.readAt === null ? '未读' : '已读' }}</span>
          <time class="ml-auto">{{ formatDateTime(selectedMessage.createdAt) }}</time>
        </div>
        <p class="select-text whitespace-pre-wrap text-sm leading-6 [overflow-wrap:anywhere]">{{ selectedMessage.message }}</p>
      </template>
      <template #actions>
        <template v-if="selectedMessage">
          <UiButton variant="text" prepend-icon="mdi-content-copy" @click="copyText(selectedMessage.message, '消息已复制')">复制全文</UiButton>
          <UiButton v-if="selectedMessage.link" :href="selectedMessage.link" target="_blank" rel="noopener noreferrer" variant="text" prepend-icon="mdi-open-in-new">打开链接</UiButton>
          <UiButton variant="tonal" @click="store.setRead(selectedMessage.id, selectedMessage.readAt === null)">{{ selectedMessage.readAt === null ? '标为已读' : '标为未读' }}</UiButton>
        </template>
        <UiButton color="primary" @click="detailsOpen = false">关闭</UiButton>
      </template>
    </AppDialog>
  </UiDialog>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import UiButton from '@/shared/ui/button/Button.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiDialog from '@/shared/ui/overlay/Dialog.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import { notificationLevel, useNotificationStore, type AppNotification, type DesktopNotificationMode, type NotificationLevel } from '@/shared/stores/notifications'
import { useClipboard } from '@/shared/composables/useClipboard'
import { toastIcons } from '@/shared/composables/useToast'
import { formatDateTime, formatShortDateTime } from '@/shared/utils/datetime'

const store = useNotificationStore()
const { copyText } = useClipboard()
const open = ref(false)
async function changeDesktopMode(event: Event) {
  const select = event.target as HTMLSelectElement
  await store.setDesktopMode(select.value as DesktopNotificationMode)
  select.value = store.desktopMode
}
function openFromDesktop(event: Event) {
  open.value = true
  const id: unknown = (event as CustomEvent).detail
  const item = store.messages.find(message => message.id === id)
  if (item) showDetails(item)
}
const hasUnreadWarning = computed(() => store.messages.some(item => item.readAt === null && notificationLevel(item) === 'warning'))
const detailsOpen = ref(false)
const selectedSnapshot = ref<AppNotification | null>(null)
// 保留消息快照，避免已读筛选或自动清理影响正在查看的正文。
const selectedMessage = computed(() => store.messages.find(item => item.id === selectedSnapshot.value?.id) ?? selectedSnapshot.value)
const levelLabels: Record<NotificationLevel, string> = { notice: '通知', warning: '警告' }
const levelFilter = ref<NotificationLevel | ''>('')
function showDetails(item: AppNotification) {
  store.setRead(item.id, true)
  selectedSnapshot.value = { ...item }
  detailsOpen.value = true
}
const unreadOnly = ref(false)
const visibleLimit = ref(50)
const filteredMessages = computed(() => store.messages.filter(item => (!unreadOnly.value || item.readAt === null) && (!levelFilter.value || notificationLevel(item) === levelFilter.value)))
const visibleMessages = computed(() => filteredMessages.value.slice(0, visibleLimit.value))
watch([open, unreadOnly, levelFilter], () => { visibleLimit.value = 50; store.pruneRead() })
watch(open, value => { if (!value) detailsOpen.value = false })
let pruneTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  pruneTimer = setInterval(() => store.pruneRead(), 60 * 1000)
  window.addEventListener('api-proxy-open-notifications', openFromDesktop)
})
onUnmounted(() => {
  clearInterval(pruneTimer)
  window.removeEventListener('api-proxy-open-notifications', openFromDesktop)
})
</script>

<style>
.ui-dialog.notification-drawer { width: min(480px, 100vw); overflow: hidden; }
</style>
