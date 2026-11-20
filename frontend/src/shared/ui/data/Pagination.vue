<template>
  <nav class="flex items-center justify-center gap-2 py-3" :aria-label="($attrs['aria-label'] as string) || '分页'">
    <button
      type="button"
      :disabled="disabled || current <= 1"
      aria-label="上一页"
      class="rounded-control border border-border px-3 py-1.5 disabled:opacity-40"
      @click="emit('update:modelValue', current - 1)"
    >
      上一页</button
    ><span class="text-sm tabular-nums">{{ current }} / {{ Math.max(1, Number(length)) }}</span
    ><button
      type="button"
      :disabled="disabled || current >= Number(length)"
      aria-label="下一页"
      class="rounded-control border border-border px-3 py-1.5 disabled:opacity-40"
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
