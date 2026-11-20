<template>
  <div :class="cn('ui-field-root min-w-0', $attrs.class as string)" :style="$attrs.style as StyleValue">
    <label v-if="label" :for="id" class="mb-1.5 block text-xs font-medium text-muted-foreground"
      >{{ label }}<span v-if="required" class="ml-1 text-destructive">*</span></label
    >
    <div
      class="ui-field field-surface flex min-h-10 items-center gap-2 px-3"
      :data-disabled="disabled || undefined"
      :class="message || error ? 'border-destructive' : 'border-input'"
    >
      <Icon v-if="prependInnerIcon" :icon="prependInnerIcon" :size="16" class="text-muted-foreground" />
      <input
        :id="id"
        v-bind="nativeAttrs"
        :type="type ?? 'text'"
        :value="modelValue ?? ''"
        :disabled="disabled"
        :required="required"
        :aria-invalid="!!message || !!error"
        :aria-describedby="message || hint ? `${id}-message` : undefined"
        class="min-w-0 flex-1 bg-transparent py-2 text-sm outline-none! placeholder:text-muted-foreground/70 disabled:cursor-not-allowed"
        @input="input"
        @blur="validate"
      />
      <button
        v-if="clearable && modelValue !== '' && modelValue != null"
        type="button"
        aria-label="清空"
        :disabled="disabled"
        class="text-muted-foreground"
        @click="emit('update:modelValue', null as T)"
      >
        <Icon icon="mdi-close" :size="14" />
      </button>
      <slot name="append-inner"></slot>
    </div>
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
<script setup lang="ts" generic="T extends string | number | null | undefined = string">
import { computed, useAttrs, useId, type StyleValue } from 'vue'
import Icon from '@/shared/ui/icon/Icon.vue'
import { cn } from '@/shared/utils/classes'
import { useField, type ValidationRule } from '@/shared/ui/form/context'
defineOptions({ inheritAttrs: false })
const props = defineProps<{
  modelValue?: T
  modelModifiers?: { number?: boolean; trim?: boolean }
  label?: string
  type?: string
  disabled?: boolean
  required?: boolean
  rules?: ValidationRule<T>[]
  errorMessages?: string | string[]
  error?: boolean
  prependInnerIcon?: string
  clearable?: boolean
  hint?: string
  persistentHint?: boolean
  hideDetails?: boolean | string
  variant?: string
  density?: string
  loading?: boolean
  counter?: string | number | boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: T] }>()
const attrs = useAttrs()
const nativeAttrs = computed(() =>
  Object.fromEntries(Object.entries(attrs).filter(([key]) => !['class', 'style'].includes(key)))
)
const id = useId()
const { disabled, message, validate } = useField(props)
function input(event: Event) {
  let value: string | number = (event.target as HTMLInputElement).value
  if (props.modelModifiers?.trim) value = value.trim()
  if (props.modelModifiers?.number && value !== '') {
    const parsed = parseFloat(value)
    if (!Number.isNaN(parsed)) value = parsed
  }
  emit('update:modelValue', value as T)
}
defineExpose({ validate })
</script>
