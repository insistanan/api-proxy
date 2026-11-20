<template>
  <div class="metric-grid" :class="{ compact }">
    <article v-for="item in items" :key="item.label" class="metric-card" :style="{ '--metric-color': item.color }">
      <div class="metric-card__top">
        <span>{{ item.label }}</span>
        <UiIcon :icon="item.icon" size="18" aria-hidden="true" />
      </div>
      <div class="metric-card__value" :title="item.rawValue">{{ item.value }}</div>
      <div class="metric-card__detail">{{ item.detail }}</div>
      <div v-if="item.comparison && !compact" class="metric-card__comparison" :class="`is-${item.comparison.direction}`">
        <UiIcon v-if="item.comparison.direction === 'up'" size="12" icon="mdi-arrow-up-bold" />
        <UiIcon v-else-if="item.comparison.direction === 'down'" size="12" icon="mdi-arrow-down-bold" />
        <span>{{ item.comparison.text }}</span>
      </div>
    </article>
  </div>
</template>

<script setup lang="ts">
import UiIcon from '@/shared/ui/icon/Icon.vue'
import { computed } from 'vue'
import type { RequestStatsSummary } from '@/types/metrics'
import { compareMetric, formatCompactNumber, formatStatMs, formatStatNumber, formatStatPercent, type MetricComparison } from '@/features/observability/utils/statsFormat'
import { chartColor } from '@/shared/theme'

const props = withDefaults(defineProps<{ summary?: RequestStatsSummary; previous?: RequestStatsSummary; compact?: boolean }>(), { compact: false })

interface MetricItem {
  label: string
  value: string
  rawValue: string
  detail: string
  icon: string
  color: string
  comparison?: MetricComparison
}

const items = computed<MetricItem[]>(() => {
  const summary = props.summary
  if (!summary) return []
  const all: MetricItem[] = [
    {
      label: '上游尝试', value: formatCompactNumber(summary.totalRequests), rawValue: formatStatNumber(summary.totalRequests),
      detail: `${formatStatNumber(summary.totalCalls)} 个独立请求`, icon: 'mdi-chart-line', color: chartColor('indigo'),
      comparison: compareMetric(summary, props.previous, 'totalRequests')
    },
    {
      label: '成功率', value: formatStatPercent(summary.avgSuccessRate), rawValue: formatStatPercent(summary.avgSuccessRate),
      detail: `${formatStatNumber(summary.totalSuccess)} 成功 · ${formatStatNumber(summary.totalFailure)} 失败/中止`, icon: 'mdi-check-circle-outline', color: chartColor('jade'),
      comparison: compareMetric(summary, props.previous, 'avgSuccessRate', true)
    },
    {
      label: '总 Token', value: formatCompactNumber(summary.totalInputTokens + summary.totalOutputTokens), rawValue: formatStatNumber(summary.totalInputTokens + summary.totalOutputTokens),
      detail: `输入 ${formatCompactNumber(summary.totalInputTokens)} · 输出 ${formatCompactNumber(summary.totalOutputTokens)}`, icon: 'mdi-database', color: chartColor('violet')
    },
    {
      label: '缓存命中率', value: formatStatPercent(summary.cacheHitRate), rawValue: formatStatPercent(summary.cacheHitRate),
      detail: `读取 ${formatCompactNumber(summary.totalCacheReadTokens)} · 创建 ${formatCompactNumber(summary.totalCacheCreationTokens)}`, icon: 'mdi-lightning-bolt', color: chartColor('copper'),
      comparison: compareMetric(summary, props.previous, 'cacheHitRate', true)
    },
    {
      label: 'P95 总耗时', value: formatStatMs(summary.p95DurationMs), rawValue: formatStatMs(summary.p95DurationMs),
      detail: `平均 ${formatStatMs(summary.avgDurationMs)} · P50 ${formatStatMs(summary.p50DurationMs)}`, icon: 'mdi-timer-sand', color: chartColor('rose'),
      comparison: compareMetric(summary, props.previous, 'p95DurationMs')
    },
    {
      label: 'P95 首字', value: formatStatMs(summary.p95FirstTokenMs), rawValue: formatStatMs(summary.p95FirstTokenMs),
      detail: `平均 ${formatStatMs(summary.avgFirstTokenMs)} · ${formatStatNumber(summary.firstTokenSamples)} 个样本`, icon: 'mdi-speedometer', color: chartColor('teal'),
      comparison: compareMetric(summary, props.previous, 'p95FirstTokenMs')
    },
    {
      label: '平均输出速度', value: summary.avgTps > 0 ? `${formatStatNumber(summary.avgTps)} /s` : '--', rawValue: formatStatNumber(summary.avgTps),
      detail: '按有输出速度记录的尝试平均', icon: 'mdi-rocket-launch', color: chartColor('blue')
    },
    {
      label: '重试率', value: formatStatPercent(summary.retryRate), rawValue: formatStatPercent(summary.retryRate),
      detail: `${formatStatNumber(summary.totalRetried)} 次尝试标记为重试`, icon: 'mdi-sync', color: chartColor('coral'),
      comparison: compareMetric(summary, props.previous, 'retryRate', true)
    }
  ]
  return props.compact ? all.slice(0, 4) : all
})
</script>

<style scoped>
@reference "@/styles/index.css";

.metric-grid { @apply grid [grid-template-columns:repeat(4,_minmax(0,_1fr))] gap-3; }
.metric-card { @apply relative min-w-0 [padding:16px_18px] overflow-hidden [border:1px_solid_color-mix(in_srgb,_var(--outline)_18%,_transparent)] rounded-card [background:color-mix(in_srgb,_var(--surface-bright)_50%,_transparent)]; }
.metric-card::before { @apply absolute [inset:0_auto_0_0] [width:3px] [background:var(--metric-color)] [content:''] opacity-90; }
.metric-card__top { @apply flex items-center justify-between gap-2 text-muted-foreground [font-size:12px]; }
.metric-card__top .ui-icon { @apply [color:var(--metric-color)]; }
.metric-card__value { @apply mt-2.5 overflow-hidden text-foreground [font-size:clamp(21px,_2vw,_28px)] font-bold [line-height:1.15] tabular-nums text-ellipsis whitespace-nowrap; }
.metric-card__detail { @apply min-h-4.5 mt-2 overflow-hidden text-muted-foreground [font-size:11px] [line-height:1.5] text-ellipsis whitespace-nowrap; }
.metric-card__comparison { @apply flex items-center [gap:3px] mt-1.5 text-muted-foreground [font-size:11px]; }
.metric-card__comparison.is-up { @apply text-primary; }
.metric-card__comparison.is-down { @apply text-secondary; }
.metric-grid.compact { @apply [grid-template-columns:repeat(4,_minmax(0,_1fr))] gap-2; }
.compact .metric-card { @apply [padding:10px_12px]; }
.compact .metric-card__value { @apply mt-1.5 [font-size:20px]; }
.compact .metric-card__detail { @apply mt-1; }
@media (max-width: 1100px) { .metric-grid { @apply [grid-template-columns:repeat(2,_minmax(0,_1fr))]; } }
@media (max-width: 700px) { .metric-grid, .metric-grid.compact { @apply [grid-template-columns:repeat(2,_minmax(0,_1fr))]; } .metric-card { @apply p-3; } .metric-card__value { @apply [font-size:20px]; } }
@media (max-width: 420px) { .metric-grid, .metric-grid.compact { @apply [grid-template-columns:1fr]; } }
</style>
