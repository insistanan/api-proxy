<template>
  <section class="breakdown-panel" aria-label="维度排行">
    <div class="breakdown-header">
      <div>
        <h3>维度排行</h3>
        <p>最多显示前 20 项。{{ tab === 'keys' ? '相同的密钥掩码会合并统计。' : '点击名称应用筛选。' }}</p>
      </div>
      <UiSelect v-model="sort" :items="sortItems" label="排序" class="breakdown-sort" variant="outlined" density="compact" hide-details />
    </div>
    <div class="breakdown-dimensions" aria-label="统计维度">
      <UiButton
        v-for="dimension in dimensions"
        :key="dimension.key"
        :variant="tab === dimension.key ? 'tonal' : 'text'"
        :color="tab === dimension.key ? 'primary' : undefined"
        :aria-pressed="tab === dimension.key"
        size="small"
        @click="tab = dimension.key"
      >{{ dimension.label }}</UiButton>
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
            <td><UiButton size="x-small" variant="text" color="primary" :disabled="loading" @click="select(row.name, true)">查看日志</UiButton></td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { computed, ref } from 'vue'
import type { RequestStatsDimensionSort, RequestStatsResponse } from '@/types/metrics'
import { formatCompactNumber, formatStatMs, formatStatNumber, formatStatPercent } from '@/features/observability/utils/statsFormat'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'

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
@reference "@/styles/index.css";

.breakdown-panel {
  @apply min-w-0 [padding:14px_16px] [border:1px_solid_color-mix(in_srgb,_var(--outline)_18%,_transparent)] rounded-card [background:var(--surface)];
}
.breakdown-header { @apply flex items-center justify-between gap-2.5 flex-wrap; }
.breakdown-sort { @apply max-w-40 [flex:1_0_148px]; }
.breakdown-dimensions { @apply flex flex-wrap [gap:3px] [margin:10px_0_5px]; }
h3 { @apply m-0 [font-size:15px]; }
p { @apply [margin:3px_0_0] text-muted-foreground [font-size:12px]; }
.breakdown-table-wrap { @apply overflow-x-auto; }
.breakdown-table { @apply w-full min-w-170 [border-collapse:collapse] [font-size:12px]; }
th, td { @apply [padding:7px_8px] [border-bottom:1px_solid_color-mix(in_srgb,_var(--outline)_14%,_transparent)]; }
th { @apply text-muted-foreground font-medium text-start; }
td { @apply tabular-nums whitespace-nowrap; }
th:nth-child(n+2), td:nth-child(n+2) { @apply text-end; }
tbody tr:last-child td { @apply [border-bottom:0]; }
tbody tr:hover { @apply [background:color-mix(in_srgb,_var(--primary)_4%,_transparent)]; }
.dimension-cell { @apply min-w-35 max-w-72.5 [width:30%]; }
.dimension-cell button {
  @apply block max-w-full overflow-hidden font-semibold text-start text-ellipsis whitespace-nowrap cursor-pointer;
}
.dimension-cell button:hover { @apply text-primary; }
.dimension-bar { @apply [height:3px] mt-1 overflow-hidden [background:color-mix(in_srgb,_var(--foreground)_8%,_transparent)] rounded-badge; }
.dimension-bar span { @apply block h-full [background:color-mix(in_srgb,_var(--primary)_70%,_transparent)]; }
.metric-context { @apply ms-1.5 text-muted-foreground; }
.has-failure { @apply text-destructive; }
</style>
