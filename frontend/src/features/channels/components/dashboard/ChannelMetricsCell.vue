<template>
  <div class="channel-metrics" :style="statusColors" @click.stop>
    <UiTooltip v-if="metrics" location="top" :open-delay="200" :open-on-focus="false">
      <template #activator="{ props: tooltipProps }">
        <div v-bind="tooltipProps" class="metrics-visual">
          <div class="metric-row" :class="successLevel">
            <span class="metric-label">成功</span>
            <span class="metric-track" aria-hidden="true"><span class="metric-fill" :style="{ width: `${Math.min(100, Math.max(0, recent?.successRate ?? 0))}%` }"></span></span>
            <strong>{{ recent?.successRate === undefined ? '—' : `${recent.successRate.toFixed(0)}%` }}</strong>
          </div>
          <div class="metric-row cache-rate">
            <span class="metric-label">缓存</span>
            <span class="metric-track" aria-hidden="true"><span v-if="cacheRate !== undefined" class="metric-fill" :style="{ width: `${Math.min(100, Math.max(0, cacheRate))}%` }"></span></span>
            <strong>{{ cacheRate === undefined ? '—' : `${cacheRate.toFixed(0)}%` }}</strong>
          </div>
        </div>
      </template>
      <div class="metrics-tooltip">
        <div class="text-xs font-bold mb-1">请求统计</div>
        <div v-for="item in windows" :key="item.key" class="metrics-tooltip-row">
          <span>{{ item.label }}:</span>
          <span>{{ formatStats(metrics.timeWindows?.[item.key]) }}</span>
        </div>
        <div class="text-xs font-bold mt-2 mb-1">缓存统计 (Token)</div>
        <div v-for="item in windows" :key="item.key" class="metrics-tooltip-row">
          <span>{{ item.label }}:</span>
          <span>{{ formatCacheStats(metrics.timeWindows?.[item.key]) }}</span>
        </div>
      </div>
    </UiTooltip>
    <span v-else class="metrics-empty">--</span>
  </div>
</template>

<script setup lang="ts">
import UiTooltip from '@/shared/ui/overlay/Tooltip.vue'
// 渠道行的成功率 / 缓存命中率指标条。只接收本渠道的指标快照，其他渠道刷新时不重新渲染。
import { computed } from 'vue'
import type { ChannelMetrics } from '@/api'
import { chartColor } from '@/shared/theme'
import {
  formatCacheStats,
  formatStats,
  getCacheHitRate,
  getRateLevel,
  shouldShowCacheHitRate
} from '@/features/channels/utils/channelMetricsFormat'

const props = defineProps<{ metrics?: ChannelMetrics }>()
const statusColors = {
  '--metric-success': chartColor('success'),
  '--metric-warning': chartColor('warning'),
  '--metric-failure': chartColor('failure'),
  '--metric-info': chartColor('info')
}

const windows = [
  { key: '15m', label: '15分钟' },
  { key: '1h', label: '1小时' },
  { key: '6h', label: '6小时' },
  { key: '24h', label: '24小时' }
] as const

const recent = computed(() => props.metrics?.timeWindows?.['15m'])
const successLevel = computed(() => getRateLevel(recent.value?.successRate))
const cacheRate = computed(() => shouldShowCacheHitRate(recent.value) ? getCacheHitRate(recent.value) : undefined)
</script>

<style scoped>
@reference "@/styles/index.css";

.channel-metrics { @apply min-w-0; }
.metrics-visual { @apply w-full grid gap-1.5; }
.metric-row { @apply grid [grid-template-columns:28px_minmax(80px,_1fr)_38px] items-center [gap:9px]; }
.metric-label { @apply text-muted-foreground; }
.metric-track { @apply block h-1.5 overflow-hidden rounded-badge [background:color-mix(in_srgb,_var(--foreground)_10%,_transparent)]; }
.metric-fill { @apply relative block h-full overflow-hidden [border-radius:inherit] [background:currentColor]; }
.metric-row strong { @apply text-end tabular-nums; }
.metrics-empty { @apply text-muted-foreground; }
.metrics-visual strong { @apply [font-weight:650]; }
.high { @apply [color:var(--metric-success)]; }
.medium { @apply [color:var(--metric-warning)]; }
.low { @apply [color:var(--metric-failure)]; }
.cache-rate { @apply [color:var(--metric-info)]; }
.metrics-tooltip { @apply [font-size:0.8125rem] [line-height:1.6]; }
.metrics-tooltip-row { @apply flex justify-between gap-4 [padding:2px_0]; }
.metrics-tooltip-row span:first-child { @apply opacity-75; }

/* 纹理仅改变表现，百分比仍由真实宽度表达。 */
[data-palette='pop'] .metric-track { height: 8px; }
[data-palette='pop'] .metric-fill {
  background-image: repeating-linear-gradient(-55deg, transparent 0 6px, #fff5 6px 9px, transparent 9px 14px);
  background-size: 28px 100%;
}
[data-palette='forest'] .metric-fill {
  background-image: repeating-linear-gradient(90deg, transparent 0 10px, var(--card) 10px 12px);
}
[data-palette='ocean'] .metric-fill::after,
[data-palette='breeze'] .metric-fill::after,
[data-palette='sunset'] .metric-fill::after,
[data-surface='glass'] .metric-fill::after {
  content: '';
  position: absolute;
  inset: 0;
  background: linear-gradient(100deg, transparent 15%, #fff5 50%, transparent 85%);
  transform: translateX(-100%);
}
[data-palette='breeze'] .metric-track { height: 5px; }
[data-radius='square'] .metric-fill {
  background-image: repeating-linear-gradient(90deg, transparent 0 7px, var(--card) 7px 9px);
}
@media (prefers-reduced-motion: no-preference) {
  .metric-fill { transition: width 320ms var(--ease-out); }
  [data-palette='pop'] .channel-row.has-live-traffic .metric-fill { animation: metric-stripes 2s linear 3; }
  .channel-row.has-live-traffic .metric-fill::after { animation: metric-light 3s ease-in-out 2; }
}
@media (prefers-reduced-motion: no-preference) and (hover: hover) {
  [data-palette='pop'] .channel-row:is(:hover, :focus-within) .metric-fill { animation: metric-stripes 2s linear 2; }
  .channel-row:is(:hover, :focus-within) .metric-fill::after { animation: metric-light 2.4s ease-in-out 1; }
}
@keyframes metric-stripes { to { background-position-x: 28px; } }
@keyframes metric-light { to { transform: translateX(100%); } }
</style>
