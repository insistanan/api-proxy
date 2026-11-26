<template>
  <div role="radiogroup" class="flex gap-3" :class="inline ? 'flex-row flex-wrap' : 'flex-col'"><slot></slot></div>
</template>
<script setup lang="ts" generic="T">
import { computed, provide } from 'vue'
import { radioKey } from '@/shared/ui/check/context'
const props = defineProps<{ modelValue?: T; hideDetails?: boolean; inline?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: T] }>()
provide(radioKey, { value: computed(() => props.modelValue), select: value => emit('update:modelValue', value as T) })
</script>
