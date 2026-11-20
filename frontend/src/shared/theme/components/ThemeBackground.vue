<template>
  <div
    v-if="theme.settings.decoration !== 'none'"
    aria-hidden="true"
    class="pointer-events-none fixed inset-0 z-0 overflow-hidden"
  >
    <div
      v-if="theme.settings.decoration === 'glow'"
      class="absolute -top-40 -right-32 h-[36rem] w-[48rem] rounded-full bg-primary opacity-(--glow-opacity) blur-[100px]"
    ></div>
    <div
      v-else
      class="absolute inset-x-0 bottom-0 h-[65vh] bg-[radial-gradient(ellipse_85%_65%_at_30%_100%,var(--primary)_56%,transparent_57%),radial-gradient(ellipse_80%_64%_at_95%_100%,var(--primary)_56%,transparent_57%)] opacity-(--wave-opacity)"
    ></div>
  </div>
</template>
<script setup lang="ts">
import { useTheme } from '@/shared/theme'
const theme = useTheme()
</script>
