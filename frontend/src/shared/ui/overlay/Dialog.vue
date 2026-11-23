<template>
  <DialogRoot
    :open="modelValue"
    @update:open="
      value => {
        if (!persistent || value) emit('update:modelValue', value)
      }
    "
  >
    <DialogTrigger v-if="$slots.activator" as-child><slot name="activator" :props="{}"></slot></DialogTrigger>
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-[200000] bg-black/45" />
      <DialogContent
        :aria-describedby="undefined"
        :class="
          cn(
            'ui-dialog dialog-surface fixed z-[200001] overflow-auto border border-border text-popover-foreground shadow-dialog outline-none [&>.ui-card]:border-0 [&>.ui-card]:bg-transparent [&>.ui-card]:shadow-none',
            drawer
              ? 'ui-dialog--drawer inset-y-0 right-0 h-dvh w-[min(1360px,100vw)]'
              : 'top-1/2 left-1/2 max-h-(--dialog-max-height) w-[calc(100%_-_2rem)] -translate-x-1/2 -translate-y-1/2 rounded-dialog',
            $attrs.class as string
          )
        "
        :style="drawer ? undefined : { maxWidth: width }"
        @escape-key-down="guard"
        @interact-outside="guard"
        @close-auto-focus="restoreFocus"
      >
        <DialogTitle class="sr-only">{{ title || $attrs['aria-label'] || '对话框' }}</DialogTitle>
        <slot></slot>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
<script setup lang="ts">
import { computed, watch } from 'vue'
import { DialogRoot, DialogTrigger, DialogPortal, DialogOverlay, DialogContent, DialogTitle } from 'reka-ui'
import { cn } from '@/shared/utils/classes'
defineOptions({ inheritAttrs: false })
const props = defineProps<{
  modelValue?: boolean
  persistent?: boolean
  maxWidth?: string | number
  drawer?: boolean
  title?: string
  scrollable?: boolean
  transition?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
const width = computed(() =>
  props.maxWidth ? (/^\d+$/.test(String(props.maxWidth)) ? `${props.maxWidth}px` : String(props.maxWidth)) : '640px'
)
function guard(event: Event) {
  if (props.persistent) event.preventDefault()
}
let previousFocus: HTMLElement | null = null
watch(
  () => props.modelValue,
  open => {
    if (open && typeof document !== 'undefined')
      previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  },
  { flush: 'sync', immediate: true }
)
function restoreFocus(event: Event) {
  if (!previousFocus?.isConnected) return
  event.preventDefault()
  previousFocus.focus()
  previousFocus = null
}
</script>

<style>
.ui-dialog {
  --dialog-max-height: min(90dvh, calc(100dvh - 2rem - env(safe-area-inset-top) - env(safe-area-inset-bottom)));
}
.ui-dialog:has(> .app-dialog, > .dialog-card-shell) { overflow: hidden; }
.ui-dialog--drawer { --dialog-max-height: 100dvh; }
.ui-dialog > .ui-card { max-height: var(--dialog-max-height); }
</style>
