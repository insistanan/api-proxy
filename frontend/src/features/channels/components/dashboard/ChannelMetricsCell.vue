<template>
  <div class="channel-metrics" :style="statusColors" @click.stop>
    <v-tooltip v-if="metrics" location="top" :open-delay="200" :open-on-focus="false">
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
        <div class="text-caption font-weight-bold mb-1">请求统计</div>
        <div v-for="item in windows" :key="item.key" class="metrics-tooltip-row">
          <span>{{ item.label }}:</span>
          <span>{{ formatStats(metrics.timeWindows?.[item.key]) }}</span>
        </div>
        <div class="text-caption font-weight-bold mt-2 mb-1">缓存统计 (Token)</div>
        <div v-for="item in windows" :key="item.key" class="metrics-tooltip-row">
          <span>{{ item.label }}:</span>
          <span>{{ formatCacheStats(metrics.timeWindows?.[item.key]) }}</span>
        </div>
      </div>
    </v-tooltip>
    <span v-else class="metrics-empty">--</span>
  </div>
</template>

<script setup lang="ts">
// 渠道行的成功率 / 缓存命中率指标条。只接收本渠道的指标快照，其他渠道刷新时不重新渲染。
import { computed } from 'vue'
import type { ChannelMetrics } from '@/api'
import { CHART_STATUS_COLORS } from '@/shared/utils/chartPalette'
import {
  formatCacheStats,
  formatStats,
  getCacheHitRate,
  getRateLevel,
  shouldShowCacheHitRate
} from '@/features/channels/utils/channelMetricsFormat'

const props = defineProps<{ metrics?: ChannelMetrics }>()
const statusColors = {
  '--metric-success': CHART_STATUS_COLORS.success,
  '--metric-warning': CHART_STATUS_COLORS.warning,
  '--metric-failure': CHART_STATUS_COLORS.failure,
  '--metric-info': CHART_STATUS_COLORS.info
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
.channel-metrics { min-width: 0; }
.metrics-visual { width: 100%; display: grid; gap: 6px; }
.metric-row { display: grid; grid-template-columns: 28px minmax(80px, 1fr) 38px; align-items: center; gap: 9px; }
.metric-label { color: rgb(var(--v-theme-on-surface-variant)); }
.metric-track { display: block; height: 6px; overflow: hidden; border-radius: 3px; background: rgba(var(--v-theme-on-surface), 0.10); }
.metric-fill { display: block; height: 100%; border-radius: inherit; background: currentColor; }
.metric-row strong { text-align: end; font-variant-numeric: tabular-nums; }
.metrics-empty { color: rgb(var(--v-theme-on-surface-variant)); }
.metrics-visual strong { font-weight: 650; }
.high { color: var(--metric-success); }
.medium { color: var(--metric-warning); }
.low { color: var(--metric-failure); }
.cache-rate { color: var(--metric-info); }
.metrics-tooltip { font-size: 0.8125rem; line-height: 1.6; }
.metrics-tooltip-row { display: flex; justify-content: space-between; gap: 16px; padding: 2px 0; }
.metrics-tooltip-row span:first-child { opacity: 0.75; }
</style>
