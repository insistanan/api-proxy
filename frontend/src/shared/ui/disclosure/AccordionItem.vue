<template>
  <details
    :name="group || undefined"
    class="ui-accordion-item group rounded-card border border-border bg-card text-card-foreground shadow-card"
    @toggle="emit('group:selected', { value: ($event.target as HTMLDetailsElement).open })"
  >
    <summary
      v-if="title || $slots.title"
      class="flex cursor-pointer list-none items-center justify-between gap-3 p-4 text-sm font-semibold"
    >
      <span
        ><slot name="title">{{ title }}</slot></span
      ><Icon icon="mdi-chevron-down" class="transition-transform group-open:rotate-180" :size="16" />
    </summary>
    <slot></slot>
  </details>
</template>
<script setup lang="ts">
import { inject } from 'vue'
import { accordionKey } from '@/shared/ui/navigation/context'
import Icon from '@/shared/ui/icon/Icon.vue'
defineProps<{ title?: string }>()
const emit = defineEmits<{ 'group:selected': [value: { value: boolean }] }>()
const group = inject(accordionKey, '')
</script>
