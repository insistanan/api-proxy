<template>
  <DropdownMenuRoot
    ><DropdownMenuTrigger as-child><slot name="activator" :props="{}"></slot></DropdownMenuTrigger
    ><DropdownMenuPortal
      ><DropdownMenuContent
        :side-offset="5"
        class="z-[200015] min-w-40 rounded-card border border-border bg-popover p-1 text-popover-foreground shadow-popover"
        :style="maxWidth ? { maxWidth: `${maxWidth}px` } : undefined"
        ><slot></slot></DropdownMenuContent></DropdownMenuPortal
  ></DropdownMenuRoot>
</template>
<script setup lang="ts">
import { provide } from 'vue'
import { DropdownMenuRoot, DropdownMenuTrigger, DropdownMenuPortal, DropdownMenuContent } from 'reka-ui'
import { menuKey } from './context'
defineProps<{ maxWidth?: string | number; closeOnContentClick?: boolean }>()
provide(menuKey, true)
</script>
