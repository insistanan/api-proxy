<template>
  <div class="channel-metrics" @click.stop>
    <v-tooltip v-if="metrics" location="top" :open-delay="200" :open-on-focus="false">
      <template #activator="{ props: tooltipProps }">
        <div v-bind="tooltipProps" class="metrics-visual">
          <!-- 近 15 分钟有请求时显示指标条，否则留空占位 -->
          <template v-if="recent?.requestCount">
            <div class="mini-metric-bar">
              <span class="metric-bar-label">成功</span>
              <div class="mmb-track">
                <div class="mmb-fill" :class="successLevel" :style="{ width: `${recent.successRate ?? 0}%` }"></div>
              </div>
              <span class="mmb-value" :class="successLevel">{{ recent.successRate?.toFixed(0) }}%</span>
            </div>
            <!-- 缓存命中率与成功率同规格；低命中只是省得少，不按故障报警 -->
            <div class="mini-metric-bar" :class="{ 'is-unavailable': cacheRate === undefined }">
              <span class="metric-bar-label">缓存</span>
              <div class="mmb-track">
                <div v-if="cacheRate !== undefined" class="mmb-fill is-cache" :class="cacheLevel" :style="{ width: `${cacheRate}%` }"></div>
              </div>
              <span class="mmb-value is-cache" :class="cacheLevel">{{ cacheRate === undefined ? '—' : `${cacheRate.toFixed(0)}%` }}</span>
            </div>
            <div class="mini-metric-secondary">{{ recent.requestCount }} 请求</div>
          </template>
          <span v-else class="metrics-empty">--</span>
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
import type { ChannelMetrics } from '@/services/api'
import {
  formatCacheStats,
  formatStats,
  getCacheHitRate,
  getCacheRateLevel,
  getRateLevel,
  shouldShowCacheHitRate
} from '@/utils/channelMetricsFormat'

const props = defineProps<{ metrics?: ChannelMetrics }>()

const windows = [
  { key: '15m', label: '15分钟' },
  { key: '1h', label: '1小时' },
  { key: '6h', label: '6小时' },
  { key: '24h', label: '24小时' }
] as const

const recent = computed(() => props.metrics?.timeWindows?.['15m'])
const successLevel = computed(() => getRateLevel(recent.value?.successRate))
const cacheRate = computed(() => shouldShowCacheHitRate(recent.value) ? getCacheHitRate(recent.value) : undefined)
const cacheLevel = computed(() => getCacheRateLevel(cacheRate.value))
</script>

<style scoped>
.channel-metrics { display: flex; align-items: center; min-width: 186px; }
.metrics-visual { min-width: 176px; }
.metrics-empty { color: rgba(var(--v-theme-on-surface-variant), 0.6); font-size: 0.75rem; }
.mini-metric-bar { display: flex; align-items: center; gap: 7px; margin-bottom: 4px; }
.mini-metric-bar.is-unavailable { opacity: 0.7; }
.metric-bar-label { width: 28px; flex: 0 0 28px; color: rgb(var(--v-theme-on-surface-variant)); font-size: 0.6875rem; }
.mmb-track { flex: 1; height: 5px; min-width: 60px; overflow: hidden; border-radius: 3px; background: rgba(var(--v-theme-outline), 0.18); }
.mmb-fill { height: 100%; border-radius: inherit; }
.mmb-value { min-width: 34px; text-align: right; font-size: 0.75rem; font-variant-numeric: tabular-nums; font-weight: 600; }
.mmb-fill.high { background: rgb(var(--v-theme-success)); }
.mmb-fill.medium { background: rgb(var(--v-theme-warning)); }
.mmb-fill.low { background: rgb(var(--v-theme-error)); }
.mmb-fill.is-cache.medium { background: rgb(var(--v-theme-info)); }
.mmb-fill.is-cache.low { background: rgb(var(--v-theme-warning)); }
.mmb-value.high { color: rgb(var(--v-theme-success)); }
.mmb-value.medium { color: rgb(var(--v-theme-warning)); }
.mmb-value.low { color: rgb(var(--v-theme-error)); }
.mmb-value.is-cache.medium { color: rgb(var(--v-theme-info)); }
.mmb-value.is-cache.low { color: rgb(var(--v-theme-warning)); }
.mmb-value.is-cache.unknown { color: rgb(var(--v-theme-on-surface-variant)); }
.mini-metric-secondary { font-size: 0.6875rem; color: rgb(var(--v-theme-on-surface-variant)); }
.metrics-tooltip { font-size: 0.8125rem; line-height: 1.6; }
.metrics-tooltip-row { display: flex; justify-content: space-between; gap: 16px; padding: 2px 0; }
.metrics-tooltip-row span:first-child { opacity: 0.75; }
</style>
