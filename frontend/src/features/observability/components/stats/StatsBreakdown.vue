<template>
  <section class="breakdown-panel" aria-label="维度排行">
    <div class="breakdown-header">
      <div>
        <h3>维度排行</h3>
        <p>最多显示前 20 项。{{ tab === 'keys' ? '相同的密钥掩码会合并统计。' : '点击名称应用筛选。' }}</p>
      </div>
      <v-select v-model="sort" :items="sortItems" label="排序" class="breakdown-sort" variant="outlined" density="compact" hide-details />
    </div>
    <div class="breakdown-dimensions" aria-label="统计维度">
      <v-btn
        v-for="dimension in dimensions"
        :key="dimension.key"
        :variant="tab === dimension.key ? 'tonal' : 'text'"
        :color="tab === dimension.key ? 'primary' : undefined"
        :aria-pressed="tab === dimension.key"
        size="small"
        @click="tab = dimension.key"
      >{{ dimension.label }}</v-btn>
    </div>
    <EmptyState v-if="!rows.length" variant="inline" icon="mdi-chart-box-outline" title="暂无维度数据" description="试试扩大时间范围或清除筛选。" />
    <div v-else class="breakdown-table-wrap" :aria-busy="loading">
      <table class="breakdown-table">
        <thead>
          <tr><th>{{ heading }}</th><th>尝试 / 占比</th><th>未成功 / 比例</th><th>输入 Token</th><th>输出 Token</th><th>平均耗时</th><th aria-label="操作"></th></tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.name">
            <td class="dimension-cell">
              <button type="button" :title="row.name" :disabled="loading" @click="select(row.name, false)">{{ displayName(row.name) }}</button>
              <div class="dimension-bar" aria-hidden="true"><span :style="{ width: `${share(row.requestCount)}%` }"></span></div>
            </td>
            <td>{{ formatStatNumber(row.requestCount) }} <span class="metric-context">{{ formatStatPercent(share(row.requestCount)) }}</span></td>
            <td :class="{ 'has-failure': row.failureCount > 0 }">
              {{ formatStatNumber(row.failureCount) }}
              <span class="metric-context">{{ formatStatPercent(100 - row.successRate) }}</span>
            </td>
            <td :title="formatStatNumber(row.inputTokens)">{{ formatCompactNumber(row.inputTokens) }}</td>
            <td :title="formatStatNumber(row.outputTokens)">{{ formatCompactNumber(row.outputTokens) }}</td>
            <td>{{ formatStatMs(row.avgDurationMs) }}</td>
            <td><v-btn size="x-small" variant="text" color="primary" :disabled="loading" @click="select(row.name, true)">查看日志</v-btn></td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { RequestStatsDimensionSort, RequestStatsResponse } from '@/types/metrics'
import { formatCompactNumber, formatStatMs, formatStatNumber, formatStatPercent } from '@/features/observability/utils/statsFormat'
import EmptyState from '@/shared/components/common/EmptyState.vue'

const dimensions = [
  { key: 'channels', label: '渠道' },
  { key: 'models', label: '模型' },
  { key: 'errors', label: '错误类型' },
  { key: 'protocols', label: '入口协议' },
  { key: 'statuses', label: '状态' },
  { key: 'upstreams', label: '上游地址' },
  { key: 'keys', label: '密钥掩码' }
] as const

export type StatsBreakdownKind = (typeof dimensions)[number]['key']
const props = defineProps<{ stats: RequestStatsResponse; loading: boolean }>()
const emit = defineEmits<{ select: [payload: { kind: StatsBreakdownKind; name: string; drill: boolean }] }>()
const sort = defineModel<RequestStatsDimensionSort>('sort', { required: true })
const sortItems = [
  { title: '未成功次数', value: 'failures' },
  { title: '未成功比例', value: 'failureRate' },
  { title: '尝试次数', value: 'volume' },
  { title: 'Token 用量', value: 'tokens' },
  { title: '平均耗时', value: 'duration' }
]
const statusLabels: Record<string, string> = {
  completed: '已完成', failed: '失败', cancelled: '已取消', blocked: '已拦截', interrupted: '未正常收尾', unknown: '状态未知'
}
const tab = ref<StatsBreakdownKind>('channels')
const rows = computed(() => props.stats[tab.value] ?? [])
const heading = computed(() => dimensions.find(dimension => dimension.key === tab.value)?.label)
const share = (count: number) => props.stats.summary.totalRequests ? count / props.stats.summary.totalRequests * 100 : 0

function displayName(name: string) {
  if (tab.value === 'statuses') return statusLabels[name] ?? name
  if (tab.value === 'errors' && name === 'unknown_error') return '未记录错误类型'
  return name || '未记录'
}

const select = (name: string, drill: boolean) => emit('select', { kind: tab.value, name, drill })
</script>

<style scoped>
.breakdown-panel {
  min-width: 0;
  padding: 14px 16px;
  border: 1px solid rgba(var(--v-theme-outline), 0.18);
  border-radius: 12px;
  background: rgb(var(--v-theme-surface));
}
.breakdown-header { display: flex; align-items: center; justify-content: space-between; gap: 10px; flex-wrap: wrap; }
.breakdown-sort { max-width: 160px; flex: 1 0 148px; }
.breakdown-dimensions { display: flex; flex-wrap: wrap; gap: 3px; margin: 10px 0 5px; }
h3 { margin: 0; font-size: 15px; }
p { margin: 3px 0 0; color: rgb(var(--v-theme-on-surface-variant)); font-size: 12px; }
.breakdown-table-wrap { overflow-x: auto; }
.breakdown-table { width: 100%; min-width: 680px; border-collapse: collapse; font-size: 12px; }
th, td { padding: 7px 8px; border-bottom: 1px solid rgba(var(--v-theme-outline), 0.14); }
th { color: rgb(var(--v-theme-on-surface-variant)); font-weight: 500; text-align: start; }
td { font-variant-numeric: tabular-nums; white-space: nowrap; }
th:nth-child(n+2), td:nth-child(n+2) { text-align: end; }
tbody tr:last-child td { border-bottom: 0; }
tbody tr:hover { background: rgba(var(--v-theme-primary), 0.04); }
.dimension-cell { min-width: 140px; max-width: 290px; width: 30%; }
.dimension-cell button {
  display: block; max-width: 100%; overflow: hidden; font-weight: 600;
  text-align: start; text-overflow: ellipsis; white-space: nowrap; cursor: pointer;
}
.dimension-cell button:hover { color: rgb(var(--v-theme-primary)); }
.dimension-bar { height: 3px; margin-top: 4px; overflow: hidden; background: rgba(var(--v-theme-on-surface), 0.08); border-radius: 2px; }
.dimension-bar span { display: block; height: 100%; background: rgba(var(--v-theme-primary), 0.7); }
.metric-context { margin-inline-start: 6px; color: rgb(var(--v-theme-on-surface-variant)); }
.has-failure { color: rgb(var(--v-theme-error)); }
</style>
