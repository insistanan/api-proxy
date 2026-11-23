<template>
  <div
    ref="element"
    :role="role ?? (type === 'error' ? 'alert' : 'status')"
    class="flex items-start gap-2.5 rounded-control border px-3.5 py-3 text-[13px] leading-6"
    :class="toneClass(type ?? 'info')"
  >
    <Icon
      v-if="icon !== false"
      :icon="typeof icon === 'string' ? icon : icons[type ?? 'info']"
      :size="18"
      class="mt-0.5"
    />
    <div class="ui-alert-content min-w-0 flex-1 break-words">
      <strong v-if="title" class="block">{{ title }}</strong
      ><slot></slot>
    </div>
    <slot name="append"></slot
    ><button v-if="closable" type="button" aria-label="关闭提示" @click="emit('click:close')">
      <Icon icon="mdi-close" :size="16" />
    </button>
  </div>
</template>
<script setup lang="ts">
import Icon from '@/shared/ui/icon/Icon.vue'
import { toneClass } from '@/shared/ui/variants'
import { onMounted, onUpdated, ref } from 'vue'
import { useNotificationStore } from '@/shared/stores/notifications'
import type { ToastType } from '@/shared/composables/useToast'
const props = withDefaults(
  defineProps<{
    type?: string
    variant?: string
    density?: string
    role?: 'alert' | 'status'
    closable?: boolean
    icon?: string | boolean
    title?: string
    notify?: boolean
  }>(),
  { icon: undefined, notify: true }
)
const element = ref<HTMLElement>()
let lastMessage = ''
// 浮动提示之外，现有页面提示也归档到同一消息中心；相同提示重复挂载不重复入库。
function recordNotice() {
  if (!props.notify) return
  const message = element.value?.querySelector('.ui-alert-content')?.textContent?.trim() || ''
  const type = props.type || 'info'
  if (!message || lastMessage === `${type}:${message}`) return
  lastMessage = `${type}:${message}`
  if (!['success', 'error', 'warning', 'info'].includes(type)) return
  useNotificationStore().addMessage(message, type as ToastType, { source: '页面提示', key: `alert:${window.location.pathname}:${type}:${message}` })
}
onMounted(recordNotice)
onUpdated(recordNotice)
const emit = defineEmits<{ 'click:close': [] }>()
const icons: Record<string, string> = {
  success: 'mdi-check-circle',
  error: 'mdi-alert-circle',
  warning: 'mdi-alert',
  info: 'mdi-information'
}
</script>
