<template>
  <div :role="nav ? 'list' : undefined" class="ui-list space-y-1"><slot></slot></div>
</template>
<script setup lang="ts" generic="T">
import { computed, provide } from 'vue'
import { listKey } from './context'
const props = defineProps<{ selected?: T[]; nav?: boolean; mandatory?: boolean; density?: string; bgColor?: string }>()
const emit = defineEmits<{ 'update:selected': [value: T[]] }>()
provide(listKey, {
  selected: computed(() => props.selected ?? []),
  select: value => emit('update:selected', [value as T])
})
</script>
