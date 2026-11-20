<template>
  <svg
    role="status"
    aria-label="加载中"
    viewBox="0 0 24 24"
    class="inline-block animate-spin"
    :class="colorClass(color)"
    :style="{ width: `${size ?? 24}px`, height: `${size ?? 24}px` }"
  >
    <circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" :stroke-width="width ?? 3" opacity=".2" />
    <path fill="none" stroke="currentColor" :stroke-width="width ?? 3" stroke-linecap="round" d="M12 3a9 9 0 0 1 9 9" />
  </svg>
</template>
<script setup lang="ts">
import { colorClass } from '@/shared/ui/variants'
defineProps<{ indeterminate?: boolean; color?: string; size?: string | number; width?: string | number }>()
</script>
