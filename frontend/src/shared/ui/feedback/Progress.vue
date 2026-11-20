<template>
  <div
    v-if="active !== false"
    role="progressbar"
    :aria-label="($attrs['aria-label'] as string) || '进度'"
    :aria-valuenow="indeterminate ? undefined : (modelValue ?? 0)"
    :aria-valuemin="0"
    :aria-valuemax="100"
    class="overflow-hidden rounded-full bg-muted"
    :style="{ height: `${height ?? 4}px` }"
  >
    <div
      class="h-full transition-[width]"
      :class="[progressColor(color), indeterminate ? 'w-1/2 animate-pulse' : '']"
      :style="indeterminate ? undefined : { width: `${Math.min(100, Math.max(0, modelValue ?? 0))}%` }"
    ></div>
  </div>
</template>
<script setup lang="ts">
const progressColor = (color?: string) =>
  ({
    success: 'bg-success',
    warning: 'bg-warning',
    error: 'bg-destructive',
    info: 'bg-info',
    secondary: 'bg-secondary'
  })[color ?? ''] ?? 'bg-primary'
withDefaults(
  defineProps<{
    modelValue?: number
    indeterminate?: boolean
    active?: boolean
    color?: string
    height?: string | number
    rounded?: boolean | string
  }>(),
  { active: true }
)
</script>
