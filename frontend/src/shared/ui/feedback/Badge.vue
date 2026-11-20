<template>
  <span
    v-if="modelValue !== false"
    class="inline-flex shrink-0 items-center gap-1 rounded-badge border border-transparent px-2 py-1 font-medium leading-none"
    :class="[toneClass(color ?? 'secondary', variant === 'flat'), size === 'x-small' ? 'text-[11px]' : 'text-xs']"
  >
    <Icon v-if="prependIcon" :icon="prependIcon" :size="12" />
    <slot></slot>
    <button
      v-if="closable"
      type="button"
      aria-label="移除标签"
      :disabled="disabled"
      @click.stop="close"
    >
      <Icon icon="mdi-close" :size="12" />
    </button>
  </span>
</template>
<script setup lang="ts">
import Icon from '@/shared/ui/icon/Icon.vue'
import { toneClass } from '@/shared/ui/variants'
withDefaults(
  defineProps<{
    modelValue?: boolean
    color?: string
    variant?: string
    size?: string
    label?: boolean
    closable?: boolean
    disabled?: boolean
    prependIcon?: string
  }>(),
  { modelValue: true }
)
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; 'click:close': [] }>()
function close() {
  emit('update:modelValue', false)
  emit('click:close')
}
</script>
