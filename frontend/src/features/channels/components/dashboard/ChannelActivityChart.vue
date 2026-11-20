<template>
  <svg v-if="bars.length" class="activity-chart-bg" preserveAspectRatio="none" viewBox="0 0 150 100" aria-hidden="true">
    <defs>
      <linearGradient
        v-for="gradient in gradients"
        :id="gradientID(gradient.key)"
        :key="gradient.key"
        x1="0%" y1="0%" x2="0%" y2="100%"
      >
        <stop offset="0%" :stop-color="gradient.color" stop-opacity="0.9" />
        <stop offset="100%" :stop-color="gradient.color" stop-opacity="0.4" />
      </linearGradient>
    </defs>
    <rect
      v-for="bar in bars"
      :key="bar.x"
      :x="bar.x" :y="bar.y" :width="bar.width" :height="bar.height"
      :fill="`url(#${gradientID(bar.gradientKey)})`"
      :rx="bar.radius" :ry="bar.radius"
      class="activity-bar"
      :class="{ 'activity-bar-latest': bar.latest }"
    />
  </svg>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { ActivityBar } from '@/features/channels/composables/useChannelActivity'
import type { ApiTab } from '@/api'

// 波形单独接收稳定的派生数组，指标、弹窗和延迟检查不再重新构建其 SVG 子树。
const props = defineProps<{ bars: ActivityBar[]; channelId: number; channelType: ApiTab }>()
const gradients = computed(() => {
  const colors = new Map(props.bars.map(bar => [bar.gradientKey, bar.color]))
  return Array.from(colors, ([key, color]) => ({ key, color }))
})
const gradientID = (key: string) => `gradient-${props.channelType}-${props.channelId}-${key}`
</script>

<style scoped>
@reference "@/styles/index.css";

.activity-chart-bg {
  @apply absolute top-0 left-0 w-full h-full pointer-events-none [z-index:0] opacity-16 overflow-hidden [border-radius:inherit] [transition:opacity_0.2s_ease];
}
:global(.channel-row:hover) .activity-chart-bg { @apply opacity-28; }
.activity-bar { @apply [transition:none]; }
.activity-bar-latest { @apply [filter:brightness(1.08)]; }
@media (prefers-reduced-motion: reduce) {
  .activity-chart-bg, .activity-bar { @apply [transition:none]! [animation:none]!; }
}
</style>
