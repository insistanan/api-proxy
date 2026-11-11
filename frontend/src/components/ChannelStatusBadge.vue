<template>
  <div class="status-badge" :class="statusClass">
    <v-tooltip location="top" content-class="status-tooltip" :open-on-focus="false">
      <template #activator="{ props: tooltipProps }">
        <div class="badge-content" v-bind="tooltipProps" :aria-label="statusLabel">
          <v-icon :size="iconSize" class="status-icon">{{ statusIcon }}</v-icon>
          <span v-if="showLabel" class="status-label">{{ statusLabel }}</span>
        </div>
      </template>
      <div class="tooltip-content">
        <div class="font-weight-bold mb-1">{{ statusLabel }}</div>
        <template v-if="metrics">
          <div class="text-caption" style="line-height: 1.6;">
            <div>请求数: {{ metrics.requestCount }}</div>
            <div>成功率: {{ metrics.successRate === undefined ? '—' : metrics.successRate.toFixed(1) + '%' }}</div>
            <div>连续失败: {{ metrics.consecutiveFailures }}</div>
            <div v-if="metrics.circuitState && metrics.circuitState !== 'closed'">
              熔断状态: {{ metrics.circuitState === 'open' ? '熔断中' : '半开探测' }}
              <template v-if="metrics.circuitConsecutiveFailures">（连续失败 {{ metrics.circuitConsecutiveFailures }}）</template>
            </div>
            <div v-if="metrics.lastSuccessAt">最后成功: {{ formatTime(metrics.lastSuccessAt) }}</div>
            <div v-if="metrics.lastFailureAt">最后失败: {{ formatTime(metrics.lastFailureAt) }}</div>
          </div>
        </template>
        <div v-else class="text-caption text-medium-emphasis">暂无指标数据</div>
      </div>
    </v-tooltip>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { ChannelStatus, ChannelMetrics } from '../services/api'

const props = withDefaults(defineProps<{
  status: ChannelStatus | 'healthy' | 'error' | 'unknown'
  metrics?: ChannelMetrics
  showLabel?: boolean
  size?: 'small' | 'default' | 'large'
}>(), { showLabel: true, size: 'default' })

type StatusPresentation = { icon: string; label: string; class: string }
const STATUS_CONFIG: Record<string, StatusPresentation> = {
  active:     { icon: 'mdi-check-circle', label: '活跃', class: 'status-active' },
  healthy:    { icon: 'mdi-check-circle', label: '健康', class: 'status-active' },
  suspended:  { icon: 'mdi-pause-circle', label: '熔断', class: 'status-suspended' },
  disabled:   { icon: 'mdi-close-circle', label: '禁用', class: 'status-disabled' },
  deprecated: { icon: 'mdi-archive-clock-outline', label: '弃用', class: 'status-deprecated' },
  error:      { icon: 'mdi-alert-circle', label: '错误', class: 'status-error' },
  unknown:    { icon: 'mdi-help-circle', label: '未知', class: 'status-unknown' }
}

// 渠道级熔断器运行时态（dashboard API 的 circuitState）：覆盖 active 渠道的展示。
// open=熔断（红）、half_open=探测中（黄）；closed 不覆盖原状态。
const CIRCUIT_CONFIG: Record<string, StatusPresentation> = {
  open:      { icon: 'mdi-flash-alert', label: '熔断', class: 'status-circuit-open' },
  half_open: { icon: 'mdi-check-underline-circle', label: '探测中', class: 'status-circuit-half-open' }
}

const circuitConfig = computed(() => {
  if (props.status !== 'active' && props.status !== 'healthy') return null
  const state = props.metrics?.circuitState
  if (!state || state === 'closed') return null
  return CIRCUIT_CONFIG[state] ?? null
})

const statusConfig = computed(() => circuitConfig.value ?? STATUS_CONFIG[props.status] ?? STATUS_CONFIG.unknown)
const statusIcon = computed(() => statusConfig.value.icon)
const statusLabel = computed(() => statusConfig.value.label)
const statusClass = computed(() => statusConfig.value.class)

const iconSize = computed(() => {
  switch (props.size) {
    case 'small': return 14
    case 'large': return 22
    default: return 18
  }
})

const formatTime = (dateStr: string): string => {
  const date = new Date(dateStr)
  const now = new Date()
  const diff = now.getTime() - date.getTime()
  if (diff < 60000) return '刚刚'
  if (diff < 3600000) return `${Math.floor(diff / 60000)} 分钟前`
  if (diff < 86400000) return `${Math.floor(diff / 3600000)} 小时前`
  return date.toLocaleDateString()
}
</script>

<style scoped>
.status-badge { --status-color: var(--v-theme-on-surface-variant); display: inline-flex; align-items: center; }
.status-active { --status-color: var(--v-theme-success); }
.status-suspended, .status-circuit-half-open { --status-color: var(--v-theme-warning); }
.status-disabled, .status-error, .status-circuit-open { --status-color: var(--v-theme-error); }
.badge-content { display: inline-flex; align-items: center; gap: 4px; padding: 4px 8px; border-radius: 6px; border: 1px solid rgba(var(--status-color), 0.22); background: rgba(var(--status-color), 0.08); color: rgb(var(--status-color)); font-size: 0.75rem; cursor: help; white-space: nowrap; }
.status-label { font-size: 0.75rem; font-weight: 500; }
.tooltip-content { max-width: 260px; }
</style>
