<template>
  <DropdownMenuRoot
    ><DropdownMenuTrigger as-child><slot name="activator" :props="{}"></slot></DropdownMenuTrigger
    ><DropdownMenuPortal
      ><DropdownMenuContent
        :side-offset="5"
        align="end"
        :collision-padding="8"
        class="menu-surface z-[200015] max-h-(--reka-dropdown-menu-content-available-height) min-w-[min(10rem,var(--reka-dropdown-menu-content-available-width))] max-w-(--reka-dropdown-menu-content-available-width) overflow-auto overscroll-contain rounded-card border border-border p-1 text-popover-foreground shadow-popover"
        :style="maxWidth ? { maxWidth: `min(${maxWidth}px, var(--reka-dropdown-menu-content-available-width))` } : undefined"
        ><slot></slot></DropdownMenuContent></DropdownMenuPortal
  ></DropdownMenuRoot>
</template>
<script setup lang="ts">
import { provide } from 'vue'
import { DropdownMenuRoot, DropdownMenuTrigger, DropdownMenuPortal, DropdownMenuContent } from 'reka-ui'
import { menuKey } from '@/shared/ui/overlay/context'
defineProps<{ maxWidth?: string | number; closeOnContentClick?: boolean }>()
provide(menuKey, true)
</script>
