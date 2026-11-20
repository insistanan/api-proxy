<template>
  <svg
    class="ui-icon inline-block shrink-0 fill-current align-middle"
    :class="colorClass(color)"
    :style="{ width: dimension, height: dimension }"
    viewBox="0 0 24 24"
    :aria-hidden="$attrs['aria-label'] ? undefined : true"
    :role="$attrs['aria-label'] ? 'img' : undefined"
  >
    <path :d="path" />
  </svg>
</template>
<script setup lang="ts">
import { computed, useSlots } from 'vue'
import { iconMap } from './icons'
import { colorClass } from '@/shared/ui/variants'
const props = defineProps<{ icon?: string; size?: string | number; color?: string; start?: boolean }>()
const slots = useSlots()
const dimension = computed(
  () =>
    ({ 'x-small': '12px', small: '16px', large: '28px', 'x-large': '32px' })[String(props.size)] ??
    (props.size ? `${Number(props.size)}px` : '20px')
)
const path = computed(() => {
  const name = (props.icon ?? String(slots.default?.()[0]?.children ?? '')).trim().replace(/^mdi-/, '')
  if (!iconMap[name]) throw new Error(`未注册图标：${name}`)
  return iconMap[name]
})
</script>
