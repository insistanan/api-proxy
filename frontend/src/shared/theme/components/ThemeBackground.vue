<template>
  <div
    v-if="theme.settings.decoration !== 'plain'"
    aria-hidden="true"
    class="pointer-events-none fixed inset-0 z-0 overflow-hidden"
  >
    <div
      v-if="theme.settings.decoration !== 'arabesque'"
      :data-texture="theme.settings.surface === 'glass' ? 'glass' : theme.settings.palette"
      class="theme-texture absolute inset-0"
    ></div>
    <div
      v-if="theme.settings.decoration === 'arabesque'"
      class="theme-candlelight absolute inset-0"
    ><ArabesquePattern class="absolute inset-0 size-full" /></div>
    <div
      v-else-if="theme.settings.decoration === 'glow'"
      class="theme-glow absolute inset-0 opacity-(--glow-opacity)"
    ></div>
    <div
      v-else-if="theme.settings.decoration === 'confetti'"
      class="theme-confetti absolute inset-0"
    ></div>
    <div
      v-else-if="theme.settings.decoration === 'waves'"
      class="theme-waves absolute inset-x-0 -bottom-8 h-[65vh] bg-[radial-gradient(ellipse_85%_65%_at_30%_100%,var(--primary)_56%,transparent_57%),radial-gradient(ellipse_80%_64%_at_95%_100%,var(--primary)_56%,transparent_57%)] opacity-(--wave-opacity)"
    ></div>
  </div>
</template>
<script setup lang="ts">
import { useTheme } from '@/shared/theme'
import ArabesquePattern from '@/shared/theme/components/ArabesquePattern.vue'
const theme = useTheme()
</script>

<style scoped>
.theme-candlelight {
  background-image:
    radial-gradient(ellipse at 26% 10%, var(--antique-desk-light, #a583471a), transparent 69%),
    radial-gradient(ellipse at 44% 28%, transparent 38%, var(--antique-desk-shade, #0002)),
    repeating-linear-gradient(135deg, #0000 0 3px, #15100c08 3px 4px);
}
.theme-glow {
  background-image:
    radial-gradient(ellipse at 81% 10%, var(--primary) 0, transparent 34%),
    radial-gradient(ellipse at 7% 84%, var(--info) 0, transparent 27%);
}
.theme-confetti {
  background-image:
    radial-gradient(circle at 8% 14%, color-mix(in srgb, var(--accent) 28%, transparent) 0 46px, transparent 47px),
    radial-gradient(circle at 94% 34%, transparent 0 58px, color-mix(in srgb, var(--primary) 12%, transparent) 59px 68px, transparent 69px),
    radial-gradient(circle at 82% 88%, color-mix(in srgb, var(--info) 12%, transparent) 0 76px, transparent 77px);
}
.theme-confetti::before,
.theme-confetti::after {
  content: '';
  position: absolute;
  width: 72px;
  height: 72px;
  opacity: .18;
}
.theme-confetti::before {
  top: 18%;
  right: 5%;
  background: var(--accent);
  clip-path: polygon(50% 0, 100% 100%, 0 100%);
  transform: rotate(18deg);
}
.theme-confetti::after {
  left: 3%;
  bottom: 15%;
  border: 12px solid var(--primary);
  border-radius: 24%;
  transform: rotate(-16deg);
}
/* 只在进入页面时播放有限轮次，停下后保留静态装饰。 */
@media (prefers-reduced-motion: no-preference) {
  .theme-confetti::before { animation: confetti-triangle 12s ease-in-out 2; }
  .theme-confetti::after { animation: confetti-square 15s ease-in-out 2; }
  .theme-waves { animation: theme-tide 20s ease-in-out 2; }
  .theme-glow { animation: theme-glow-drift 18s ease-in-out 2; }
}
@keyframes confetti-triangle { 50% { transform: translate(-12px, 16px) rotate(34deg); } }
@keyframes confetti-square { 50% { transform: translate(10px, -14px) rotate(8deg); } }
@keyframes theme-tide { 50% { transform: translateY(20px) scaleX(1.04); } }
@keyframes theme-glow-drift { 50% { transform: translate(-24px, 18px); opacity: calc(var(--glow-opacity) * .7); } }
</style>
