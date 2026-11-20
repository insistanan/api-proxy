<template>
  <label class="inline-flex min-h-10 items-center gap-2 text-sm" :class="disabled ? 'opacity-50' : ''"
    ><input
      type="checkbox"
      :checked="checked"
      :disabled="disabled"
      class="size-4 shrink-0 accent-primary"
      @change="change" /><span v-if="label">{{ label }}</span
    ><slot></slot
  ></label>
</template>
<script setup lang="ts" generic="T">
import { computed } from 'vue'
import { useField } from '@/shared/ui/form/context'
const props = defineProps<{
  modelValue?: T
  value?: unknown
  label?: string
  disabled?: boolean
  density?: string
  hideDetails?: boolean
  color?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [value: T] }>()
const { disabled } = useField(props)
const checked = computed(() =>
  Array.isArray(props.modelValue) ? props.modelValue.includes(props.value) : !!props.modelValue
)
function change(event: Event) {
  const on = (event.target as HTMLInputElement).checked
  emit(
    'update:modelValue',
    (Array.isArray(props.modelValue)
      ? on
        ? [...props.modelValue, props.value]
        : props.modelValue.filter(v => v !== props.value)
      : on) as T
  )
}
</script>
