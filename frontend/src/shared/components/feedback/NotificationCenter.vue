<template>
  <span class="relative inline-flex">
    <UiButton class="header-tool" data-header-hit icon="mdi-bell-ring" variant="text" size="small" :aria-label="`消息中心，${store.unreadCount} 条未读`" :aria-pressed="open" @click="open = true" />
    <span v-if="store.unreadCount" class="pointer-events-none absolute -top-1 -right-1 min-w-4 rounded-full bg-destructive px-1 text-center text-[10px] leading-4 text-white">{{ store.unreadCount > 99 ? '99+' : store.unreadCount }}</span>
  </span>
  <UiDialog v-model="open" drawer title="消息中心" class="notification-drawer">
    <aside class="flex h-full min-h-0 flex-col">
      <header class="flex shrink-0 items-center justify-between gap-3 border-b border-border p-4">
        <div><h2 class="font-semibold">消息中心</h2><p class="mt-1 text-xs text-muted-foreground">{{ store.unreadCount }} 条未读 · 已读消息在 7 天后自动移除</p></div>
        <UiButton icon="mdi-close" variant="text" size="small" aria-label="关闭消息中心" @click="open = false" />
      </header>
      <div class="flex shrink-0 items-center justify-between gap-2 border-b border-border px-4 py-2">
        <UiButton variant="text" size="small" :aria-pressed="unreadOnly" @click="unreadOnly = !unreadOnly">{{ unreadOnly ? '查看全部' : '只看未读' }}</UiButton>
        <UiButton variant="text" size="small" prepend-icon="mdi-check" :disabled="!store.unreadCount" @click="store.markAllRead">全部已读</UiButton>
      </div>
      <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain p-4">
        <UiAlert v-if="store.storageError" type="error" :notify="false" class="mb-3">{{ store.storageError }}</UiAlert>
        <p v-if="!visibleMessages.length" class="py-10 text-center text-sm text-muted-foreground">{{ unreadOnly ? '暂无未读消息' : '暂无消息' }}</p>
        <article v-for="item in visibleMessages" :key="item.id" class="mb-3 rounded-control border p-3" :class="item.readAt === null ? 'border-primary/40 bg-primary/5' : 'border-border'">
          <div class="flex items-center gap-2 text-xs text-muted-foreground">
            <UiIcon :icon="toastIcons[item.type]" :color="item.type" size="16" />
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
          <span>{{ typeLabels[selectedMessage.type] }} · {{ selectedMessage.source }}</span>
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
import { useNotificationStore, type AppNotification } from '@/shared/stores/notifications'
import { useClipboard } from '@/shared/composables/useClipboard'
import { toastIcons, type ToastType } from '@/shared/composables/useToast'
import { formatDateTime, formatShortDateTime } from '@/shared/utils/datetime'

const store = useNotificationStore()
const { copyText } = useClipboard()
const open = ref(false)
const detailsOpen = ref(false)
const selectedSnapshot = ref<AppNotification | null>(null)
// 保留消息快照，避免已读筛选或自动清理影响正在查看的正文。
const selectedMessage = computed(() => store.messages.find(item => item.id === selectedSnapshot.value?.id) ?? selectedSnapshot.value)
const typeLabels: Record<ToastType, string> = { success: '成功', error: '错误', warning: '警告', info: '提醒' }
function showDetails(item: AppNotification) {
  store.setRead(item.id, true)
  selectedSnapshot.value = { ...item }
  detailsOpen.value = true
}
const unreadOnly = ref(false)
const visibleLimit = ref(50)
const filteredMessages = computed(() => store.messages.filter(item => !unreadOnly.value || item.readAt === null))
const visibleMessages = computed(() => filteredMessages.value.slice(0, visibleLimit.value))
watch([open, unreadOnly], () => { visibleLimit.value = 50; store.pruneRead() })
watch(open, value => { if (!value) detailsOpen.value = false })
let pruneTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => { pruneTimer = setInterval(() => store.pruneRead(), 60 * 1000) })
onUnmounted(() => clearInterval(pruneTimer))
</script>

<style>
.ui-dialog.notification-drawer { width: min(480px, 100vw); overflow: hidden; }
</style>
