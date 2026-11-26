<template>
  <component
    :is="menu ? DropdownMenuItem : 'button'"
    :data-active="isActive || undefined"
    :type="menu ? undefined : 'button'"
    :class="[
      'ui-list-item flex min-h-9 w-full items-center gap-2.5 rounded-control px-3 py-2 text-left text-sm outline-none transition-colors hover:bg-muted/70 data-highlighted:bg-muted/70',
      isActive ? 'bg-primary/10 font-medium text-primary hover:bg-primary/12' : colorClass(color)
    ]"
    @click="click"
    ><slot name="prepend"><Icon v-if="prependIcon" :icon="prependIcon" :size="18" /></slot
    ><span class="min-w-0 flex-1 [overflow-wrap:anywhere]"
      ><slot>{{ title }}</slot
      ><span v-if="subtitle" class="block text-xs text-muted-foreground">{{ subtitle }}</span></span
    ><slot name="append"></slot
  ></component>
</template>
<script setup lang="ts">
import { computed, inject } from 'vue'
import { DropdownMenuItem } from 'reka-ui'
import { listKey } from '@/shared/ui/navigation/context'
import { menuKey } from '@/shared/ui/overlay/context'
import Icon from '@/shared/ui/icon/Icon.vue'
import { colorClass } from '@/shared/ui/variants'
const props = defineProps<{
  value?: unknown
  title?: string
  subtitle?: string
  prependIcon?: string
  active?: boolean
  color?: string
  rounded?: string
  variant?: string
}>()
const emit = defineEmits<{ click: [event: MouseEvent] }>()
const list = inject(listKey, null)
const menu = inject(menuKey, false)
const isActive = computed(() => props.active || list?.selected.value.includes(props.value))
function click(event: MouseEvent) {
  if (props.value !== undefined) list?.select(props.value)
  emit('click', event)
}
</script>
