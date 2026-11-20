<template>
  <DialogRoot
    :open="modelValue"
    :modal="scrim !== false"
    @update:open="
      value => {
        if (!persistent || value) emit('update:modelValue', value)
      }
    "
  >
    <DialogPortal>
      <DialogOverlay v-if="scrim !== false" class="fixed inset-0 z-[200000] bg-black/45" />
      <DialogContent
        ref="content"
        v-bind="contentProps"
        :aria-describedby="undefined"
        :class="
          cn(
            'ui-overlay fixed z-[200001] outline-none',
            scrim !== false && 'top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2',
            contentClass,
            $attrs.class as string
          )
        "
        :style="{ zIndex: zIndex ?? 200001 }"
        @escape-key-down="guard"
        @interact-outside="guard"
        @open-auto-focus="focus"
      >
        <DialogTitle class="sr-only">{{ contentProps?.['aria-label'] || '浮动面板' }}</DialogTitle>
        <slot></slot>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue'
import { DialogRoot, DialogPortal, DialogOverlay, DialogContent, DialogTitle } from 'reka-ui'
import { cn } from '@/shared/utils/classes'
defineOptions({ inheritAttrs: false })
const props = defineProps<{
  modelValue?: boolean
  persistent?: boolean
  scrim?: boolean | string
  contentClass?: string
  contentProps?: Record<string, unknown>
  zIndex?: number
  activator?: unknown
  openOnClick?: boolean
  scrollStrategy?: string
  closeOnBack?: boolean
  transition?: unknown
}>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
const content = ref<{ $el: HTMLElement }>()
const contentEl = computed(() => content.value?.$el)
function guard(event: Event) {
  if (props.persistent) event.preventDefault()
}
function focus(event: Event) {
  if (props.scrim === false && props.activator) event.preventDefault()
}
defineExpose({ contentEl })
</script>
