<template>
  <Teleport to="body" :disabled="stacked"
    ><div
      v-if="modelValue"
      role="status"
      class="menu-surface pointer-events-auto shrink-0 rounded-control border px-4 py-3 text-sm shadow-popover"
      :class="[toneClass(color ?? 'info'), stacked ? '' : 'fixed right-4 z-[200030] max-w-[calc(100vw-2rem)]']"
      :style="{ backgroundColor: 'var(--menu-surface)', ...(stacked ? {} : { top: `calc(var(--app-header-height, 56px) + 12px + ${(position ?? 0) * 60}px)` }) }"
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
  stacked?: boolean
}>()
</script>
