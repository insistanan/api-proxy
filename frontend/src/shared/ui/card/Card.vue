<template>
  <div
    :class="cn('ui-card panel-surface min-w-0', $attrs.class as string)"
    :style="
      maxWidth
        ? {
            maxWidth:
              typeof maxWidth === 'number' ? `${maxWidth}px` : /^\d+$/.test(maxWidth) ? `${maxWidth}px` : maxWidth
          }
        : undefined
    "
  >
    <slot></slot>
  </div>
</template>
<script setup lang="ts">
import { cn } from '@/shared/utils/classes'
defineProps<{
  maxWidth?: string | number
  color?: string
  variant?: string
  rounded?: string
  elevation?: string | number
}>()
</script>
