<template>
  <header
    ref="headerElement"
    class="header-shell sticky top-0 z-40"
    @pointermove.passive="feedback.pointerMove"
    @pointerover.passive="feedback.pointerOver"
    @pointerout.passive="feedback.pointerOut"
    @pointerleave.passive="feedback.pointerLeave"
    @pointerdown.passive="feedback.prepareSound"
    @keydown="feedback.keyDown"
    @click="feedback.activate"
  >
    <div class="flex min-h-14 w-full items-center gap-2 px-3 sm:px-5"><slot></slot></div>
    <div v-if="extended" class="flex min-h-12 w-full items-center border-t border-border/60 px-2 sm:px-4">
      <slot name="extension"></slot>
    </div>
  </header>
</template>
<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useHeaderFeedback } from '@/app/layout/useHeaderFeedback'

defineProps<{ extended?: boolean }>()
const emit = defineEmits<{ 'height-change': [height: number] }>()
const headerElement = ref<HTMLElement>()
const feedback = useHeaderFeedback(headerElement)
let sizeObserver: ResizeObserver | undefined
let previousHeight = 0
onMounted(() => {
  sizeObserver = new ResizeObserver(entries => {
    const entry = entries[0]
    if (!entry) return
    const height = Math.ceil(entry.borderBoxSize?.[0]?.blockSize ?? entry.target.getBoundingClientRect().height)
    if (height !== previousHeight) { previousHeight = height; emit('height-change', height) }
  })
  if (headerElement.value) sizeObserver.observe(headerElement.value)
})
onUnmounted(() => sizeObserver?.disconnect())
</script>

<style scoped>
@reference "@/styles/index.css";

.header-shell {
  --header-tilt: 4deg;
  --header-twist: .6deg;
  background: color-mix(in srgb, var(--popover) 94%, transparent);
  border-bottom: 1px solid color-mix(in srgb, var(--primary) 16%, var(--border));
  box-shadow: 0 3px 16px color-mix(in srgb, var(--foreground) 4%, transparent);
  backdrop-filter: blur(10px);
}
[data-palette='pop'] .header-shell { --header-tilt: 8deg; --header-twist: 1.8deg; }
[data-palette='breeze'] .header-shell { --header-tilt: 2deg; --header-twist: .25deg; }
[data-palette='forest'] .header-shell { --header-tilt: 3deg; --header-twist: .4deg; }
[data-palette='sultan'][data-surface='antique'] .header-shell {
  background: var(--surface);
  border-bottom-color: var(--border);
  box-shadow: inset 0 -2px color-mix(in srgb, var(--antique-band) 75%, transparent), 0 3px 8px #100c0933;
  backdrop-filter: none;
}
[data-radius='square'] .header-shell { --header-tilt: 1deg; --header-twist: 0deg; }
.header-shell :deep(.header-tools) { @apply flex shrink-0 items-center gap-2; }
.header-shell :deep(.header-utility-group) {
  @apply flex items-center gap-0.5 rounded-card border p-0.5;
  border-color: color-mix(in srgb, var(--primary) 12%, var(--border));
  background: color-mix(in srgb, var(--primary) 5%, transparent);
  box-shadow: inset 0 1px 0 color-mix(in srgb, var(--surface-bright) 40%, transparent);
}
.header-shell :deep(.header-tool) { @apply relative rounded-control border border-transparent text-muted-foreground; }
.header-shell :deep(.header-tool:hover), .header-shell :deep(.header-tool:focus-visible) {
  color: var(--primary);
  background: color-mix(in srgb, var(--primary) 12%, transparent);
  border-color: color-mix(in srgb, var(--primary) 20%, transparent);
}
.header-shell :deep(.header-tool.is-current), .header-shell :deep(.header-tool[aria-pressed='true']) {
  color: var(--primary-foreground);
  background: var(--primary);
  border-color: var(--primary);
  box-shadow: 0 2px 6px color-mix(in srgb, var(--primary) 18%, transparent);
}
.header-shell :deep(.header-logout) { margin-left: 2px; }
.header-shell :deep(.header-logout:hover) { color: var(--destructive); background: color-mix(in srgb, var(--destructive) 12%, transparent); }
.header-shell :deep([data-header-hit]) {
  transform-origin: center;
  transition: transform 180ms var(--ease-out), background-color 150ms ease, border-color 150ms ease, color 150ms ease, box-shadow 150ms ease;
  translate: none;
}
@media (prefers-reduced-motion: no-preference) and (hover: hover) {
  .header-shell :deep([data-header-hit][data-pointer-active='true']) {
    transform: perspective(540px) rotateX(calc(var(--header-tilt) * var(--hover-y) * (.4 + var(--hover-speed) * .3))) rotateY(calc(var(--header-tilt) * var(--hover-x) * (.4 + var(--hover-speed) * .3))) rotateZ(calc(var(--header-twist) * var(--hover-speed) * var(--hover-x))) translateY(-1px);
  }
  .header-shell :deep([data-hover-speed='swift'] .header-hit-label) { animation: header-label-flick 240ms var(--ease-out) 1; }
}
@keyframes header-label-flick { 40% { transform: translateY(-1px) skewX(-5deg); } }
@media (max-width: 420px) { .header-shell :deep(.header-version) { display: none; } }
@media (max-width: 640px), (prefers-reduced-transparency: reduce) {
  .header-shell { background: var(--popover); backdrop-filter: none; }
}
@supports not (backdrop-filter: blur(1px)) { .header-shell { background: var(--popover); } }
</style>
