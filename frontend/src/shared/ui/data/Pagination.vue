<template>
  <nav class="flex max-w-full flex-wrap items-center justify-center gap-2 py-1" :aria-label="($attrs['aria-label'] as string) || '分页'">
    <button
      type="button"
      :disabled="disabled || current <= 1"
      aria-label="上一页"
      class="min-h-8 rounded-control border border-border bg-popover px-3 py-1.5 text-xs text-foreground transition-colors hover:bg-muted disabled:pointer-events-none disabled:opacity-40"
      @click="emit('update:modelValue', current - 1)"
    >
      上一页</button
    ><span class="min-w-12 text-center text-xs tabular-nums text-muted-foreground">{{ current }} / {{ Math.max(1, Number(length)) }}</span
    ><button
      type="button"
      :disabled="disabled || current >= Number(length)"
      aria-label="下一页"
      class="min-h-8 rounded-control border border-border bg-popover px-3 py-1.5 text-xs text-foreground transition-colors hover:bg-muted disabled:pointer-events-none disabled:opacity-40"
      @click="emit('update:modelValue', current + 1)"
    >
      下一页
    </button>
  </nav>
</template>
<script setup lang="ts">
import { computed } from 'vue'
const props = defineProps<{
  modelValue?: number
  length: number | string
  disabled?: boolean
  totalVisible?: number | string
  density?: string
  size?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [value: number] }>()
const current = computed(() => props.modelValue ?? 1)
</script>
