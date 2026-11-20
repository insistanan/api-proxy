<template>
  <div role="tablist" class="flex flex-wrap gap-1 border-b border-border pb-2"><slot></slot></div>
</template>
<script setup lang="ts" generic="T">
import { computed, provide } from 'vue'
import { toggleKey } from '@/shared/ui/button/context'
const props = defineProps<{ modelValue?: T; color?: string; density?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: T] }>()
provide(toggleKey, {
  value: computed(() => props.modelValue),
  disabled: computed(() => false),
  select: value => emit('update:modelValue', value as T)
})
</script>
