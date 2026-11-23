<template>
  <UiDialog v-model="open" max-width="440" :title="title" :persistent="loading">
    <UiCard class="flex min-h-0 flex-col overflow-hidden">
      <div class="ui-card-title shrink-0 px-4 pt-4 text-base font-semibold">{{ title }}</div>
      <div class="p-4 confirm-dialog__message">{{ message }}</div>
      <div class="flex shrink-0 flex-wrap items-center gap-2 border-t border-border px-4 py-3">
        <span class="flex-1" ></span>
        <UiButton variant="text" :disabled="loading" @click="open = false">{{ cancelText }}</UiButton>
        <UiButton :color="confirmColor" :loading="loading" @click="emit('confirm')">{{ confirmText }}</UiButton>
      </div>
    </UiCard>
  </UiDialog>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiDialog from '@/shared/ui/overlay/Dialog.vue'
const open = defineModel<boolean>({ default: false })

withDefaults(defineProps<{
  message: string
  title?: string
  confirmText?: string
  cancelText?: string
  confirmColor?: string
  loading?: boolean
}>(), {
  title: '请确认',
  confirmText: '确定',
  cancelText: '取消',
  confirmColor: 'error',
  loading: false
})

const emit = defineEmits<{ confirm: [] }>()
</script>

<style scoped>
@reference "@/styles/index.css";

.confirm-dialog__message { @apply min-h-0 overflow-y-auto whitespace-pre-wrap [line-height:1.7]; }
</style>
