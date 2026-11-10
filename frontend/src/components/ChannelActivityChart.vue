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
import type { ActivityBar } from '@/composables/useChannelActivity'
import type { ApiTab } from '@/services/api'

// 波形单独接收稳定的派生数组，指标、弹窗和延迟检查不再重新构建其 SVG 子树。
const props = defineProps<{ bars: ActivityBar[]; channelId: number; channelType: ApiTab }>()
const gradients = computed(() => {
  const colors = new Map(props.bars.map(bar => [bar.gradientKey, bar.color]))
  return Array.from(colors, ([key, color]) => ({ key, color }))
})
const gradientID = (key: string) => `gradient-${props.channelType}-${props.channelId}-${key}`
</script>

<style scoped>
.activity-chart-bg {
  position: absolute; top: 0; left: 0; width: 100%; height: 100%;
  pointer-events: none; z-index: 0; opacity: 0.2; overflow: hidden;
  border-radius: inherit; transition: opacity 0.2s ease;
}
:global(.channel-row:hover) .activity-chart-bg { opacity: 0.34; }
.activity-bar { transition: none; }
.activity-bar-latest { filter: brightness(1.45) saturate(1.2); }
:global(.channel-row:hover) .activity-bar-latest { animation: spark-blink 2.4s ease-in-out infinite; }
@media (prefers-reduced-motion: reduce) {
  .activity-chart-bg, .activity-bar { transition: none !important; animation: none !important; }
}
</style>
