<template>
  <div class="space-y-3"><slot></slot></div>
</template>
<script setup lang="ts">
import { provide, useId } from 'vue'
import { accordionKey } from '@/shared/ui/navigation/context'
const props = defineProps<{ multiple?: boolean; variant?: string }>()
provide(accordionKey, props.multiple ? '' : useId())
</script>
