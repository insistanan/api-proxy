<template>
  <div role="group" class="ui-toggle-group inline-flex max-w-full flex-wrap gap-1 rounded-control border border-border bg-muted/40 p-1">
    <slot></slot>
  </div>
</template>
<script setup lang="ts" generic="T">
import { computed, provide } from 'vue'
import { toggleKey } from './context'
const props = defineProps<{
  modelValue?: T
  disabled?: boolean
  mandatory?: boolean
  density?: string
  variant?: string
  color?: string
  divided?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: T] }>()
provide(toggleKey, {
  value: computed(() => props.modelValue),
  disabled: computed(() => !!props.disabled),
  select: value => {
    if (!props.disabled) emit('update:modelValue', value as T)
  }
})
</script>
