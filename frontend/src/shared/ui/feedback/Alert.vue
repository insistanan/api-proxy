<template>
  <div
    :role="role ?? (type === 'error' ? 'alert' : 'status')"
    class="flex items-start gap-3 rounded-control border px-4 py-3 text-sm leading-6"
    :class="toneClass(type ?? 'info')"
  >
    <Icon
      v-if="icon !== false"
      :icon="typeof icon === 'string' ? icon : icons[type ?? 'info']"
      :size="18"
      class="mt-0.5"
    />
    <div class="ui-alert-content min-w-0 flex-1">
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
withDefaults(
  defineProps<{
    type?: string
    variant?: string
    density?: string
    role?: 'alert' | 'status'
    closable?: boolean
    icon?: string | boolean
    title?: string
  }>(),
  { icon: undefined }
)
const emit = defineEmits<{ 'click:close': [] }>()
const icons: Record<string, string> = {
  success: 'mdi-check-circle',
  error: 'mdi-alert-circle',
  warning: 'mdi-alert',
  info: 'mdi-information'
}
</script>
