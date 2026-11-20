<template>
  <component
    :is="to ? RouterLink : href ? 'a' : 'button'"
    v-bind="to ? { to } : href ? { href } : { type: type ?? 'button', disabled: blocked }"
    :class="
      cn(
        buttonVariants({ size: normalizedSize }),
        icon ? 'aspect-square px-0' : '',
        block && 'w-full',
        appearance,
        selected && 'ring-2 ring-ring/30',
        $attrs.class as string
      )
    "
    :aria-disabled="blocked || undefined"
    :aria-busy="loading || undefined"
    :aria-pressed="group && $attrs.role !== 'tab' ? selected : undefined"
    :aria-selected="$attrs.role === 'tab' ? selected : undefined"
    @click="click"
  >
    <Icon v-if="loading" icon="mdi-loading" class="animate-spin" :size="16" />
    <Icon v-else-if="typeof icon === 'string'" :icon="icon" :size="18" />
    <Icon v-else-if="prependIcon" :icon="prependIcon" :size="16" />
    <slot></slot>
  </component>
</template>
<script setup lang="ts">
import { computed, inject } from 'vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'
import { cva } from 'class-variance-authority'
import Icon from '@/shared/ui/icon/Icon.vue'
import { cn } from '@/shared/utils/classes'
import { colorClass, toneClass } from '@/shared/ui/variants'
import { toggleKey } from './context'
defineOptions({ inheritAttrs: true })
const props = defineProps<{
  icon?: boolean | string
  prependIcon?: string
  color?: string
  variant?: string
  size?: string
  loading?: boolean
  disabled?: boolean
  block?: boolean
  to?: RouteLocationRaw
  href?: string
  type?: 'button' | 'submit' | 'reset'
  value?: unknown
  active?: boolean
  rounded?: string | boolean
  selectedClass?: string
  height?: string | number
}>()
const emit = defineEmits<{ click: [event: MouseEvent] }>()
const group = inject(toggleKey, null)
const selected = computed(() => props.active || (group && group.value.value === props.value))
const blocked = computed(() => props.disabled || props.loading || group?.disabled.value)
const normalizedSize = computed(() =>
  ['x-small', 'small', 'large'].includes(props.size ?? '') ? (props.size as 'small' | 'large' | 'x-small') : 'default'
)
const buttonVariants = cva(
  'ui-button inline-flex shrink-0 items-center justify-center gap-2 rounded-control border border-transparent font-medium leading-5 transition-colors focus-visible:ring-2 focus-visible:ring-ring/30 disabled:pointer-events-none disabled:opacity-50',
  {
    variants: {
      size: {
        default: 'min-h-10 px-4 py-2 text-sm',
        small: 'min-h-8 px-3 py-1.5 text-sm',
        'x-small': 'min-h-7 px-2 py-1 text-xs',
        large: 'min-h-12 px-5 py-3 text-base'
      }
    }
  }
)
const appearance = computed(() => {
  if (props.variant === 'text') return `${colorClass(props.color)} hover:bg-muted`
  if (props.variant === 'outlined') return `border-input ${colorClass(props.color)} hover:bg-muted`
  if (props.variant === 'tonal') return toneClass(props.color ?? 'secondary')
  return props.color ? toneClass(props.color, true) : 'bg-muted text-foreground hover:bg-muted/70'
})
function click(event: MouseEvent) {
  if (blocked.value) {
    event.preventDefault()
    return
  }
  if (group) group.select(props.value)
  emit('click', event)
}
</script>
