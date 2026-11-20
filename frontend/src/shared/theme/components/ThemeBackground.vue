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
      v-else-if="theme.settings.decoration === 'confetti'"
      class="theme-confetti absolute inset-0"
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

<style scoped>
.theme-confetti {
  background-image:
    radial-gradient(circle at 8% 14%, color-mix(in srgb, var(--accent) 28%, transparent) 0 46px, transparent 47px),
    radial-gradient(circle at 94% 34%, transparent 0 58px, color-mix(in srgb, var(--primary) 12%, transparent) 59px 68px, transparent 69px),
    radial-gradient(circle at 82% 88%, color-mix(in srgb, var(--info) 12%, transparent) 0 76px, transparent 77px),
    radial-gradient(circle, color-mix(in srgb, var(--primary) 9%, transparent) 1px, transparent 1.5px);
  background-size: auto, auto, auto, 24px 24px;
}
</style>
