<template>
  <div :class="cn('ui-field-root min-w-0', $attrs.class as string)">
    <label v-if="label" :for="id" class="mb-1.5 block text-xs font-medium text-muted-foreground">{{ label }}</label>
    <textarea
      :id="id"
      v-bind="nativeAttrs"
      :value="modelValue ?? ''"
      :rows="rows ?? 3"
      :disabled="disabled"
      :required="required"
      :aria-invalid="!!message"
      :aria-describedby="message || hint ? `${id}-message` : undefined"
      class="ui-field field-surface w-full px-3 py-2.5 text-sm leading-6 outline-none! placeholder:text-muted-foreground/70 disabled:cursor-not-allowed"
      :data-disabled="disabled || undefined"
      :class="[message ? 'border-destructive' : 'border-input', noResize ? 'resize-none' : 'resize-y']"
      @input="input"
      @blur="validate"
    ></textarea>
    <p
      v-if="message || hint"
      :id="`${id}-message`"
      class="mt-1 text-xs leading-5"
      :class="message ? 'text-destructive' : 'text-muted-foreground'"
      :role="message ? 'alert' : undefined"
    >
      {{ message || hint }}
    </p>
  </div>
</template>
<script setup lang="ts" generic="T extends string | null | undefined = string">
import { computed, useAttrs, useId } from 'vue'
import { cn } from '@/shared/utils/classes'
import { useField, type ValidationRule } from '@/shared/ui/form/context'
defineOptions({ inheritAttrs: false })
const props = defineProps<{
  modelValue?: T
  label?: string
  disabled?: boolean
  required?: boolean
  rules?: ValidationRule<T>[]
  errorMessages?: string | string[]
  prependInnerIcon?: string
  hint?: string
  persistentHint?: boolean
  hideDetails?: boolean | string
  variant?: string
  density?: string
  loading?: boolean
  rows?: string | number
  maxRows?: string | number
  autoGrow?: boolean
  noResize?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: T] }>()
const attrs = useAttrs()
const nativeAttrs = computed(() => Object.fromEntries(Object.entries(attrs).filter(([key]) => key !== 'class')))
const id = useId()
const { disabled, message, validate } = useField(props)
function input(event: Event) {
  const element = event.target as HTMLTextAreaElement
  emit('update:modelValue', element.value as T)
  if (props.autoGrow) {
    element.style.height = 'auto'
    element.style.height = `${Math.min(element.scrollHeight, Number(props.maxRows ?? 20) * 24)}px`
  }
}
defineExpose({ validate })
</script>
