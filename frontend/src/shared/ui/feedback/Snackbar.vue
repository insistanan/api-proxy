<template>
  <Teleport to="body"
    ><div
      v-if="modelValue"
      role="status"
      class="fixed right-4 z-[200030] max-w-[calc(100vw-2rem)] rounded-control border bg-popover px-4 py-3 text-sm shadow-popover"
      :class="toneClass(color ?? 'info')"
      :style="{ top: `${16 + (position ?? 0) * 60}px` }"
    >
      <slot></slot></div
  ></Teleport>
</template>
<script setup lang="ts">
import { toneClass } from '@/shared/ui/variants'
defineProps<{
  modelValue?: boolean
  color?: string
  timeout?: number
  position?: number
  location?: string
  variant?: string
}>()
</script>
