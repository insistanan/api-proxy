<template>
  <label class="ui-switch inline-flex min-h-10 items-center gap-2 text-sm" :class="disabled ? 'opacity-50' : ''"
    ><SwitchRoot
      :model-value="on"
      :aria-label="($attrs['aria-label'] as string) || label"
      :disabled="disabled || loading"
      class="relative inline-flex h-6 w-11 shrink-0 rounded-full border-2 border-transparent bg-muted transition-colors data-[state=checked]:bg-primary"
      @update:model-value="update"
      ><SwitchThumb
        class="pointer-events-none block size-5 translate-x-0 rounded-full bg-popover shadow-sm transition-transform data-[state=checked]:translate-x-5" /></SwitchRoot
    ><span v-if="label">{{ label }}</span
    ><span v-if="persistentHint && hint" class="text-xs text-muted-foreground">{{ hint }}</span></label
  >
</template>
<script setup lang="ts" generic="T = boolean">
import { computed } from 'vue'
import { SwitchRoot, SwitchThumb } from 'reka-ui'
import { useField } from '@/shared/ui/form/context'
const props = defineProps<{
  modelValue?: T
  trueValue?: T
  falseValue?: T
  label?: string
  disabled?: boolean
  loading?: boolean
  color?: string
  hideDetails?: boolean
  density?: string
  inset?: boolean
  hint?: string
  persistentHint?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: T] }>()
const { disabled } = useField(props)
const on = computed(() => (props.trueValue !== undefined ? props.modelValue === props.trueValue : !!props.modelValue))
function update(value: boolean) {
  emit('update:modelValue', (value ? (props.trueValue ?? true) : (props.falseValue ?? false)) as T)
}
</script>
