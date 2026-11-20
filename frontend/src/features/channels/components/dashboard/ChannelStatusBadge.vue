<template>
  <div class="status-badge" :class="statusClass">
    <UiTooltip location="top" content-class="status-tooltip" :open-on-focus="false">
      <template #activator="{ props: tooltipProps }">
        <div class="badge-content" v-bind="tooltipProps" :aria-label="statusLabel">
          <UiIcon :size="iconSize" class="status-icon">{{ statusIcon }}</UiIcon>
          <span v-if="showLabel" class="status-label">{{ statusLabel }}</span>
        </div>
      </template>
      <div class="tooltip-content">
        <div class="font-bold mb-1">{{ statusLabel }}</div>
        <template v-if="metrics">
          <div class="text-xs" style="line-height: 1.6;">
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
        <div v-else class="text-xs text-muted-foreground">暂无指标数据</div>
      </div>
    </UiTooltip>
  </div>
</template>

<script setup lang="ts">
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiTooltip from '@/shared/ui/overlay/Tooltip.vue'
import { computed } from 'vue'
import type { ChannelStatus, ChannelMetrics } from '@/api'
import { formatDateTime } from '@/shared/utils/datetime'

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
  return formatDateTime(date)
}
</script>

<style scoped>
@reference "@/styles/index.css";

.status-badge { --status-color: var(--muted-foreground); @apply inline-flex items-center; }
.status-active { --status-color: var(--success); }
.status-suspended, .status-circuit-half-open { --status-color: var(--warning); }
.status-disabled, .status-error, .status-circuit-open { --status-color: var(--destructive); }
.badge-content { @apply inline-flex items-center gap-1 [padding:4px_8px] rounded-badge [border:1px_solid_color-mix(in_srgb,_var(--status-color)_22%,_transparent)] [background:color-mix(in_srgb,_var(--status-color)_8%,_transparent)] [color:var(--status-color)] [font-size:0.75rem] cursor-help whitespace-nowrap; }
.status-label { @apply [font-size:0.75rem] font-medium; }
.tooltip-content { @apply max-w-65; }
</style>
