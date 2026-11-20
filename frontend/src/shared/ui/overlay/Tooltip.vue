<template>
  <TooltipProvider :delay-duration="Number(openDelay ?? 300)"
    ><TooltipRoot :disabled="disabled"
      ><TooltipTrigger as-child><slot name="activator" :props="{}"></slot></TooltipTrigger
      ><TooltipPortal
        ><TooltipContent
          :side="side"
          :side-offset="6"
          :class="
            cn(
              'z-[200020] max-w-[min(360px,calc(100vw-32px))] rounded-control border border-border bg-popover px-3 py-2 text-xs leading-6 break-words text-popover-foreground shadow-popover',
              contentClass
            )
          "
          ><slot>{{ text }}</slot></TooltipContent
        ></TooltipPortal
      ></TooltipRoot
    ></TooltipProvider
  >
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { TooltipProvider, TooltipRoot, TooltipTrigger, TooltipPortal, TooltipContent } from 'reka-ui'
import { cn } from '@/shared/utils/classes'
const props = defineProps<{
  text?: string
  disabled?: boolean
  location?: string
  openDelay?: string | number
  openOnFocus?: boolean
  contentClass?: string
  maxWidth?: string | number
  theme?: string
}>()
const side = computed(() =>
  ['top', 'bottom', 'left', 'right'].includes(props.location ?? '')
    ? (props.location as 'top' | 'bottom' | 'left' | 'right')
    : 'top'
)
</script>
