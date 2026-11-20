<template>
  <div class="metric-grid" :class="{ compact }">
    <article v-for="item in items" :key="item.label" class="metric-card" :style="{ '--metric-color': item.color }">
      <div class="metric-card__top">
        <span>{{ item.label }}</span>
        <v-icon :icon="item.icon" size="18" aria-hidden="true" />
      </div>
      <div class="metric-card__value" :title="item.rawValue">{{ item.value }}</div>
      <div class="metric-card__detail">{{ item.detail }}</div>
      <div v-if="item.comparison && !compact" class="metric-card__comparison" :class="`is-${item.comparison.direction}`">
        <v-icon v-if="item.comparison.direction === 'up'" size="12" icon="mdi-arrow-up-bold" />
        <v-icon v-else-if="item.comparison.direction === 'down'" size="12" icon="mdi-arrow-down-bold" />
        <span>{{ item.comparison.text }}</span>
      </div>
    </article>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { RequestStatsSummary } from '@/types/metrics'
import { compareMetric, formatCompactNumber, formatStatMs, formatStatNumber, formatStatPercent, type MetricComparison } from '@/features/observability/utils/statsFormat'
import { CHART_PALETTE } from '@/shared/utils/chartPalette'

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
      detail: `${formatStatNumber(summary.totalCalls)} 个独立请求`, icon: 'mdi-chart-line', color: CHART_PALETTE.indigo,
      comparison: compareMetric(summary, props.previous, 'totalRequests')
    },
    {
      label: '成功率', value: formatStatPercent(summary.avgSuccessRate), rawValue: formatStatPercent(summary.avgSuccessRate),
      detail: `${formatStatNumber(summary.totalSuccess)} 成功 · ${formatStatNumber(summary.totalFailure)} 失败/中止`, icon: 'mdi-check-circle-outline', color: CHART_PALETTE.jade,
      comparison: compareMetric(summary, props.previous, 'avgSuccessRate', true)
    },
    {
      label: '总 Token', value: formatCompactNumber(summary.totalInputTokens + summary.totalOutputTokens), rawValue: formatStatNumber(summary.totalInputTokens + summary.totalOutputTokens),
      detail: `输入 ${formatCompactNumber(summary.totalInputTokens)} · 输出 ${formatCompactNumber(summary.totalOutputTokens)}`, icon: 'mdi-database', color: CHART_PALETTE.violet
    },
    {
      label: '缓存命中率', value: formatStatPercent(summary.cacheHitRate), rawValue: formatStatPercent(summary.cacheHitRate),
      detail: `读取 ${formatCompactNumber(summary.totalCacheReadTokens)} · 创建 ${formatCompactNumber(summary.totalCacheCreationTokens)}`, icon: 'mdi-lightning-bolt', color: CHART_PALETTE.copper,
      comparison: compareMetric(summary, props.previous, 'cacheHitRate', true)
    },
    {
      label: 'P95 总耗时', value: formatStatMs(summary.p95DurationMs), rawValue: formatStatMs(summary.p95DurationMs),
      detail: `平均 ${formatStatMs(summary.avgDurationMs)} · P50 ${formatStatMs(summary.p50DurationMs)}`, icon: 'mdi-timer-sand', color: CHART_PALETTE.rose,
      comparison: compareMetric(summary, props.previous, 'p95DurationMs')
    },
    {
      label: 'P95 首字', value: formatStatMs(summary.p95FirstTokenMs), rawValue: formatStatMs(summary.p95FirstTokenMs),
      detail: `平均 ${formatStatMs(summary.avgFirstTokenMs)} · ${formatStatNumber(summary.firstTokenSamples)} 个样本`, icon: 'mdi-speedometer', color: CHART_PALETTE.teal,
      comparison: compareMetric(summary, props.previous, 'p95FirstTokenMs')
    },
    {
      label: '平均输出速度', value: summary.avgTps > 0 ? `${formatStatNumber(summary.avgTps)} /s` : '--', rawValue: formatStatNumber(summary.avgTps),
      detail: '按有输出速度记录的尝试平均', icon: 'mdi-rocket-launch', color: CHART_PALETTE.blue
    },
    {
      label: '重试率', value: formatStatPercent(summary.retryRate), rawValue: formatStatPercent(summary.retryRate),
      detail: `${formatStatNumber(summary.totalRetried)} 次尝试标记为重试`, icon: 'mdi-sync', color: CHART_PALETTE.coral,
      comparison: compareMetric(summary, props.previous, 'retryRate', true)
    }
  ]
  return props.compact ? all.slice(0, 4) : all
})
</script>

<style scoped>
.metric-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; }
.metric-card { position: relative; min-width: 0; padding: 16px 18px; overflow: hidden; border: 1px solid rgba(var(--v-theme-outline), .18); border-radius: 12px; background: rgba(var(--v-theme-surface-bright), .5); }
.metric-card::before { position: absolute; inset: 0 auto 0 0; width: 3px; background: var(--metric-color); content: ''; opacity: .9; }
.metric-card__top { display: flex; align-items: center; justify-content: space-between; gap: 8px; color: rgb(var(--v-theme-on-surface-variant)); font-size: 12px; }
.metric-card__top .v-icon { color: var(--metric-color); }
.metric-card__value { margin-top: 10px; overflow: hidden; color: rgb(var(--v-theme-on-surface)); font-size: clamp(21px, 2vw, 28px); font-weight: 700; line-height: 1.15; font-variant-numeric: tabular-nums; text-overflow: ellipsis; white-space: nowrap; }
.metric-card__detail { min-height: 18px; margin-top: 8px; overflow: hidden; color: rgb(var(--v-theme-on-surface-variant)); font-size: 11px; line-height: 1.5; text-overflow: ellipsis; white-space: nowrap; }
.metric-card__comparison { display: flex; align-items: center; gap: 3px; margin-top: 6px; color: rgb(var(--v-theme-on-surface-variant)); font-size: 11px; }
.metric-card__comparison.is-up { color: rgb(var(--v-theme-primary)); }
.metric-card__comparison.is-down { color: rgb(var(--v-theme-secondary)); }
.metric-grid.compact { grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 8px; }
.compact .metric-card { padding: 10px 12px; }
.compact .metric-card__value { margin-top: 6px; font-size: 20px; }
.compact .metric-card__detail { margin-top: 4px; }
@media (max-width: 1100px) { .metric-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 700px) { .metric-grid, .metric-grid.compact { grid-template-columns: repeat(2, minmax(0, 1fr)); } .metric-card { padding: 12px; } .metric-card__value { font-size: 20px; } }
@media (max-width: 420px) { .metric-grid, .metric-grid.compact { grid-template-columns: 1fr; } }
</style>
