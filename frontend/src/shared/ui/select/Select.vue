<template>
  <div class="ui-field-root min-w-0">
    <label v-if="label" :id="`${id}-label`" :for="id" class="mb-1.5 block text-xs font-medium text-muted-foreground">{{
      label
    }}</label>
    <ComboboxRoot
      :model-value="keys"
      :multiple="multiple"
      :disabled="disabled"
      :open="isOpen"
      ignore-filter
      open-on-click
      :reset-search-term-on-blur="false"
      :reset-search-term-on-select="false"
      @update:model-value="select"
      @update:open="updateOpen"
    >
      <ComboboxAnchor
        class="ui-field field-surface flex min-h-10 items-center gap-2 px-3"
        :data-disabled="disabled || undefined"
        :class="message ? 'border-destructive' : 'border-input'"
      >
        <Icon v-if="prependInnerIcon" :icon="prependInnerIcon" :size="16" class="text-muted-foreground" />
        <ComboboxInput
          :id="id"
          :model-value="inputText"
          :readonly="!searchable"
          :placeholder="placeholder"
          :aria-label="($attrs['aria-label'] as string) || label"
          :aria-required="required || undefined"
          :aria-invalid="!!message"
          :aria-describedby="message || hint ? `${id}-message` : undefined"
          class="min-w-0 flex-1 bg-transparent py-2 text-sm outline-none! placeholder:text-muted-foreground/70 disabled:cursor-not-allowed"
          @update:model-value="query = $event"
          @blur="validate"
        />
        <button
          v-if="clearable && hasValue"
          type="button"
          :disabled="disabled"
          aria-label="清空选择"
          @click.stop="clear"
        >
          <Icon icon="mdi-close" :size="14" />
        </button>
        <ComboboxTrigger aria-label="展开选项" class="text-muted-foreground"
          ><Icon :icon="loading ? 'mdi-loading' : 'mdi-chevron-down'" :class="loading ? 'animate-spin' : ''" :size="16"
        /></ComboboxTrigger>
      </ComboboxAnchor>
      <div v-if="multiple && Array.isArray(modelValue) && modelValue.length" class="mt-1 flex flex-wrap gap-1">
        <span
          v-for="(value, index) in modelValue"
          :key="index"
          class="inline-flex items-center gap-1 rounded-badge bg-primary/10 px-2 py-1 text-xs text-primary"
          >{{ titleFor(value)
          }}<button
            v-if="closableChips"
            type="button"
            :disabled="disabled"
            :aria-label="`移除 ${titleFor(value)}`"
            @click="remove(index)"
          >
            <Icon icon="mdi-close" :size="12" /></button
        ></span>
      </div>
      <ComboboxPortal>
        <ComboboxContent
          position="popper"
          :side-offset="4"
          :collision-padding="8"
          class="z-[200010] max-h-[min(20rem,var(--reka-combobox-content-available-height))] min-w-(--reka-combobox-trigger-width) max-w-(--reka-combobox-content-available-width) overflow-auto overscroll-contain rounded-card border border-border bg-popover p-1 text-popover-foreground shadow-popover"
          @escape-key-down.stop
        >
          <ComboboxViewport>
            <ComboboxItem
              v-for="item in filtered"
              :key="item.key"
              :value="item.key"
              class="flex min-h-9 cursor-pointer items-center gap-2 rounded-control px-3 py-2 text-sm break-words outline-none transition-colors data-highlighted:bg-primary/10 data-highlighted:text-primary data-[state=checked]:bg-primary/5 data-[state=checked]:font-medium"
              ><ComboboxItemIndicator><Icon icon="mdi-check" :size="14" /></ComboboxItemIndicator
              ><span class="min-w-0 flex-1 [overflow-wrap:anywhere]"><slot
                name="item"
                :item="{ raw: item.raw, title: item.title, value: item.value, subtitle: item.subtitle }"
                :props="{}"
                >{{ item.title }}</slot></span
              ></ComboboxItem
            >
            <ComboboxItem
              v-if="customValue"
              value="custom"
              class="cursor-pointer rounded-control px-3 py-2 text-sm outline-none data-highlighted:bg-primary/12"
              @select="selectCustom"
              >添加「{{ customValue }}」</ComboboxItem
            >
            <ComboboxEmpty class="p-3 text-sm text-muted-foreground"
              ><slot name="no-data">{{ noDataText || '暂无选项' }}</slot></ComboboxEmpty
            >
          </ComboboxViewport>
        </ComboboxContent>
      </ComboboxPortal>
    </ComboboxRoot>
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
<script setup lang="ts" generic="T">
import { computed, ref, useId, watch } from 'vue'
import {
  ComboboxRoot,
  ComboboxAnchor,
  ComboboxInput,
  ComboboxTrigger,
  ComboboxPortal,
  ComboboxContent,
  ComboboxViewport,
  ComboboxItem,
  ComboboxItemIndicator,
  ComboboxEmpty,
  type AcceptableValue
} from 'reka-ui'
import Icon from '@/shared/ui/icon/Icon.vue'
import { useField, type ValidationRule } from '@/shared/ui/form/context'
const props = withDefaults(
  defineProps<{
    modelValue?: T
    items?: readonly unknown[]
    itemTitle?: string | ((item: never) => string)
    itemValue?: string
    returnObject?: boolean
    searchable?: boolean
    creatable?: boolean
    multiple?: boolean
    chips?: boolean
    closableChips?: boolean
    clearable?: boolean
    label?: string
    placeholder?: string
    menu?: boolean
    loading?: boolean
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
    noDataText?: string
    maxWidth?: string | number
  }>(),
  { items: () => [], itemTitle: 'title', itemValue: 'value', menu: undefined }
)
const emit = defineEmits<{ 'update:modelValue': [value: T]; 'update:menu': [value: boolean] }>()
const id = useId()
const query = ref<string | null>(null)
const localOpen = ref(false)
const isOpen = computed(() => props.menu ?? localOpen.value)
watch(isOpen, open => {
  if (!open) query.value = null
})
const { disabled, message, validate } = useField(props)
const options = computed(() =>
  props.items.map((raw, index) => {
    const record = raw && typeof raw === 'object' ? (raw as Record<string, unknown>) : null
    const title =
      typeof props.itemTitle === 'function'
        ? props.itemTitle(raw as never)
        : record
          ? (record[props.itemTitle] ?? record.name ?? record.id ?? '')
          : raw
    // null 是部分过滤器中的有效选项，只有缺失的字段才使用原对象。
    const value = record && props.itemValue in record ? record[props.itemValue] : raw
    return {
      key: String(index),
      raw,
      subtitle: typeof record?.subtitle === 'string' ? record.subtitle : undefined,
      title: String(title),
      value
    }
  })
)
const values = computed<unknown[]>(() =>
  props.multiple ? (Array.isArray(props.modelValue) ? props.modelValue : []) : [props.modelValue]
)
const same = (a: unknown, b: unknown) =>
  a === b ||
  (a != null && b != null && typeof a === 'object' && typeof b === 'object' && JSON.stringify(a) === JSON.stringify(b))
const keys = computed(() => {
  const selected = options.value
    .filter(option => values.value.some(value => same(value, props.returnObject ? option.raw : option.value)))
    .map(option => option.key)
  return props.multiple ? selected : selected[0]
})
const filtered = computed(() =>
  options.value.filter(
    item => !props.searchable || item.title.toLocaleLowerCase().includes((query.value ?? '').toLocaleLowerCase())
  )
)
const customValue = computed(() => {
  const value = query.value?.trim()
  return props.creatable &&
    value &&
    !options.value.some(item => item.title === value || item.value === value) &&
    !values.value.includes(value)
    ? value
    : ''
})
const hasValue = computed(() =>
  props.multiple ? values.value.length > 0 : props.modelValue != null && props.modelValue !== ''
)
function titleFor(value: unknown) {
  return (
    options.value.find(option => same(value, props.returnObject ? option.raw : option.value))?.title ??
    (value == null ? '' : typeof value === 'object' ? JSON.stringify(value) : String(value))
  )
}
const inputText = computed(
  () => (isOpen.value ? query.value : null) ?? (props.multiple ? '' : titleFor(props.modelValue))
)
function select(value: AcceptableValue | AcceptableValue[]) {
  const chosen = (Array.isArray(value) ? value : [value])
    .map(key => options.value.find(item => item.key === key))
    .filter(item => item !== undefined)
  const result = chosen.map(item => (props.returnObject ? item.raw : item.value))
  const custom = values.value.filter(
    value => !options.value.some(item => same(value, props.returnObject ? item.raw : item.value))
  )
  emit('update:modelValue', (props.multiple ? [...custom, ...result] : chosen.length ? result[0] : null) as T)
  query.value = null
}
function clear() {
  emit('update:modelValue', (props.multiple ? [] : null) as T)
  query.value = null
}
function remove(index: number) {
  emit('update:modelValue', values.value.filter((_, i) => i !== index) as T)
}
function updateOpen(open: boolean) {
  localOpen.value = open
  if (!open) query.value = null
  emit('update:menu', open)
}
function selectCustom(event: Event) {
  event.preventDefault()
  const value = customValue.value
  if (!value) return
  emit('update:modelValue', (props.multiple ? [...values.value, value] : value) as T)
  updateOpen(false)
}
defineExpose({ validate })
</script>
