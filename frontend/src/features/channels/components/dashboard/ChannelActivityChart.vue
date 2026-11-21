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
      :style="{ color: bar.color }"
    />
    <ellipse
      v-for="bar in decoratedBars"
      :key="bar.x"
      :cx="bar.x + bar.width / 2" :cy="bar.y + Math.min(bar.width * 2, bar.height / 3)"
      :rx="bar.width / 2" :ry="Math.min(bar.width * 2, bar.height / 3)"
      class="activity-tip"
      :class="{ 'activity-tip-latest': bar.latest }"
      :style="{ color: bar.color }"
    />
  </svg>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useTheme } from '@/shared/theme'
import type { ActivityBar } from '@/features/channels/composables/useChannelActivity'
import type { ApiTab } from '@/api'

// 波形单独接收稳定的派生数组，指标、弹窗和延迟检查不再重新构建其 SVG 子树。
const props = defineProps<{ bars: ActivityBar[]; channelId: number; channelType: ApiTab }>()
const theme = useTheme()
// 只有使用端点装饰的主题才创建额外节点，其他主题仍保持每段一个矩形。
const decoratedBars = computed(() => ['pop', 'forest'].includes(theme.settings.palette)
  ? props.bars.filter(bar => bar.height >= 8)
  : [])
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
.channel-row:hover .activity-chart-bg { @apply opacity-28; }
.activity-bar { @apply [transition:none]; }
.activity-bar-latest { filter: brightness(1.08); }
.activity-tip { display: none; fill: currentColor; }
[data-palette='pop'] .activity-bar { fill: currentColor; fill-opacity: .72; }
[data-palette='pop'] .activity-tip { display: block; transform-box: fill-box; transform-origin: center; transform: rotate(-25deg); }
[data-palette='breeze'] .activity-bar { rx: 1px; fill-opacity: .75; }
[data-palette='ocean'] .activity-bar { rx: 2px; }
[data-palette='forest'] .activity-tip { display: block; transform-box: fill-box; transform-origin: center; transform: rotate(30deg); opacity: .8; }
[data-palette='sunset'] .activity-bar { rx: 1px; }
[data-surface='glass'] .activity-bar { stroke: currentColor; stroke-width: .15px; }
[data-radius='square'] .activity-bar { rx: 0; ry: 0; }
@media (prefers-reduced-motion: no-preference) and (hover: hover) {
  [data-palette='pop'] .channel-row:is(:hover, :focus-within) .activity-bar-latest { animation: activity-pop 700ms var(--ease-spring) 2; }
  [data-palette='forest'] .channel-row:is(:hover, :focus-within) .activity-tip-latest { animation: activity-leaf 1.8s ease-in-out 2; }
}
@keyframes activity-pop { 50% { transform: translateY(-2px); opacity: .6; } }
@keyframes activity-leaf { 50% { transform: rotate(-15deg); } }
@media (prefers-reduced-motion: reduce) {
  .activity-chart-bg, .activity-bar, .activity-tip { @apply [transition:none]! [animation:none]!; }
}
</style>
