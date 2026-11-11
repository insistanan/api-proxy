<template>
  <div class="availability-panel">
    <div class="availability-toolbar">
      <v-text-field v-model="keyword" label="搜索模型或渠道" hide-details density="compact" clearable />
      <v-select v-if="!fixedChannelId" v-model="channelFilter" :items="channelOptions" label="渠道" hide-details density="compact" />
      <v-select v-model="stateFilter" :items="stateOptions" label="观测状态" hide-details density="compact" />
      <v-btn variant="tonal" prepend-icon="mdi-refresh" :loading="loading" @click="refresh">刷新观测</v-btn>
    </div>

    <v-alert v-if="error" type="error" variant="tonal" class="mb-3" role="alert">{{ error }}</v-alert>
    <p class="availability-caption">
      仅观察真实请求，不主动测试模型。每次上游尝试独立统计，参数错误、内容拒绝和客户端取消不计入成功率。
      状态参考最近 1 小时，正常需至少 {{ snapshot?.minSamples ?? 5 }} 个有效样本且成功率 ≥ 95%；灰色表示未知。
      这些状态暂不参与自动选渠。
    </p>
    <p v-if="snapshot" class="availability-caption">
      更新于 {{ formatTime(snapshot.generatedAt) }} · 趋势为最近 24 个小时桶（含当前小时） ·
      {{ filteredRows.length }} 个渠道／模型组合
      <span v-if="error"> · 下方保留的是上次快照</span>
    </p>

    <v-progress-linear v-if="loading" indeterminate color="primary" aria-label="加载可用性观测" />
    <v-table v-if="visibleRows.length" class="availability-table" density="comfortable">
      <thead>
        <tr><th>渠道 / 实际模型</th><th>观测状态</th><th>窗口成功率</th><th>每小时趋势</th><th>最近请求</th><th>归因</th></tr>
      </thead>
      <tbody>
        <template v-for="row in visibleRows" :key="rowKey(row)">
          <tr>
            <td><strong>{{ row.channelName || '未命名渠道' }}</strong><div class="model-name">{{ row.model }}</div></td>
            <td><v-chip :color="stateMeta[row.state].color" size="small" variant="tonal">{{ stateMeta[row.state].label }}</v-chip></td>
            <td>
              <strong>{{ successRate(row) }}</strong>
              <div class="text-caption text-medium-emphasis">{{ eligible(row) }} 个有效样本 · {{ row.ignored }} 个排除</div>
              <div class="text-caption text-medium-emphasis">近 1 小时 {{ successRate(row.recent) }} · {{ eligible(row.recent) }} 个样本</div>
            </td>
            <td>
              <div class="availability-trend" aria-label="每小时请求成功率，灰色表示没有有效样本">
                <v-tooltip v-for="bucket in row.buckets" :key="bucket.from" :text="bucketText(bucket)" location="top">
                  <template #activator="{ props: tooltipProps }">
                    <span v-bind="tooltipProps" tabindex="0" :aria-label="bucketText(bucket)" :class="bucketTone(bucket)"></span>
                  </template>
                </v-tooltip>
              </div>
            </td>
            <td class="text-caption">{{ formatTime(row.lastSeen) }}</td>
            <td><v-btn size="small" variant="text" :aria-expanded="expanded === rowKey(row)" @click="toggle(row)">明细</v-btn></td>
          </tr>
          <tr v-if="expanded === rowKey(row)">
            <td colspan="6" class="availability-detail">
              <p class="text-body-2 mb-3">
                成功 {{ row.success }} · 上游故障 {{ row.failure }} · 不支持 {{ row.unsupported }} · 凭据／额度 {{ row.credential }} · 限流 {{ row.rateLimited }} · 排除 {{ row.ignored }}
              </p>
              <p class="text-caption text-medium-emphasis mb-3">
                未调用、最近 1 小时无有效结果或样本不足时显示未知。部分组合失败会标记降级，其他组合仍可能可用。
                冷却优先参考 Retry-After，缺失时按 30 秒估计。仅统计当前配置中的 Key、URL 和协议；旧日志不追溯判定。
              </p>
              <div v-for="candidate in row.candidates" :key="candidate.id" class="candidate-row">
                <span>{{ candidate.label }}<small>{{ candidate.endpoint }} · {{ candidate.protocol }}</small></span>
                <v-chip :color="stateMeta[candidate.state].color" size="small">{{ stateMeta[candidate.state].label }}</v-chip>
                <span>{{ successRate(candidate) }} · {{ eligible(candidate) }} 个有效样本</span>
                <span>{{ reasonLabel(candidate.lastReason) }}<small>{{ formatTime(candidate.lastSeen) }}</small></span>
                <span v-if="candidate.state === 'cooling'">冷却至 {{ formatTime(candidate.cooldownUntil) }}</span>
              </div>
              <p v-if="!row.candidates.length" class="text-body-2">当前没有配置 Key／URL 组合，无法判断可用性。</p>
            </td>
          </tr>
        </template>
      </tbody>
    </v-table>
    <p v-else-if="!loading && !error" class="text-body-2 py-4">
      {{ snapshot?.rows.length ? '没有符合筛选条件的模型。' : '暂无模型观测。配置中的映射目标和默认模型，以及真实请求使用的模型，会出现在这里。' }}
    </p>
    <v-pagination v-if="pageCount > 1" v-model="page" :length="pageCount" :total-visible="5" density="comfortable" />
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import type { ApiTab } from '@/services/channel-api'
import type { Channel } from '@/types/channel'
import { getModelAvailability } from '@/services/model-availability'
import { useModelAvailabilityStore } from '@/stores/modelAvailability'
import type { AvailabilityCounts, AvailabilityState, ModelAvailabilityRow } from '@/types/model-availability'

const props = defineProps<{ channelType: ApiTab; channels: Channel[]; fixedChannelId?: string }>()
const store = useModelAvailabilityStore()
const snapshot = computed(() => store.snapshots[props.channelType])
const loading = ref(false)
const error = ref('')
const keyword = ref<string | null>('')
const stateFilter = ref<AvailabilityState | 'all'>('all')
const channelFilter = ref('')
const expanded = ref('')
const page = ref(1)
let controller: AbortController | undefined

const stateMeta: Record<AvailabilityState, { label: string; color: string }> = {
  unknown: { label: '未知', color: 'secondary' },
  healthy: { label: '正常', color: 'success' },
  degraded: { label: '降级', color: 'warning' },
  cooling: { label: '冷却', color: 'info' },
  unsupported: { label: '不支持', color: 'error' }
}
const stateOptions = [
  { title: '全部状态', value: 'all' },
  ...Object.entries(stateMeta).map(([value, meta]) => ({ title: meta.label, value }))
]
const channelOptions = computed(() => [
  { title: '全部渠道', value: '' },
  ...props.channels.filter(channel => channel.id).map(channel => ({ title: channel.name || '未命名渠道', value: channel.id! }))
])
const filteredRows = computed(() => {
  const query = (keyword.value ?? '').trim().toLowerCase()
  return (snapshot.value?.rows ?? []).filter(row =>
    (!(props.fixedChannelId || channelFilter.value) || row.channelId === (props.fixedChannelId || channelFilter.value)) &&
    (stateFilter.value === 'all' || row.state === stateFilter.value) &&
    (!query || `${row.model} ${row.channelName}`.toLowerCase().includes(query))
  )
})
const pageCount = computed(() => Math.ceil(filteredRows.value.length / 20))
const visibleRows = computed(() => filteredRows.value.slice((page.value - 1) * 20, page.value * 20))

function eligible(counts: AvailabilityCounts) {
  return counts.success + counts.failure + counts.unsupported + counts.credential + counts.rateLimited
}
function successRate(counts: AvailabilityCounts) {
  const total = eligible(counts)
  return total ? `${(100 * counts.success / total).toFixed(1)}%` : '—'
}
function formatTime(timestamp: number) {
  return timestamp ? new Date(timestamp).toLocaleString('zh-CN', { hour12: false }) : '尚未观测'
}
function rowKey(row: ModelAvailabilityRow) {
  return JSON.stringify([row.channelId, row.model])
}
function toggle(row: ModelAvailabilityRow) {
  expanded.value = expanded.value === rowKey(row) ? '' : rowKey(row)
}
function bucketTone(counts: AvailabilityCounts) {
  if (!eligible(counts)) return 'is-empty'
  return counts.success / eligible(counts) >= 0.95 ? 'is-healthy' : 'is-degraded'
}
function bucketText(bucket: AvailabilityCounts & { from: number }) {
  return `${formatTime(bucket.from)} 起 · 成功率 ${successRate(bucket)} · ${eligible(bucket)} 个有效样本 · ${bucket.ignored} 个排除`
}
function reasonLabel(reason: string) {
  const labels: Record<string, string> = {
    success: '最近有效结果成功', model_not_found: '模型不存在或不支持', credential_or_quota: '凭据或额度异常',
    rate_limited: '限流／容量不足', network: '连接失败或超时', read_body: '响应读取中断',
    read_response: '响应读取中断', response_processing: '响应或流处理失败', empty_response: '空响应',
    error_envelope: '成功状态中含错误', upstream_error: '上游服务异常'
  }
  return labels[reason] ?? '尚无有效结果'
}
async function refresh() {
  controller?.abort()
  const current = new AbortController()
  controller = current
  const type = props.channelType
  loading.value = true
  error.value = ''
  try {
    const response = await getModelAvailability(type, current.signal)
    if (!current.signal.aborted && type === props.channelType) store.replace(type, response)
  } catch (cause) {
    if (!current.signal.aborted) error.value = cause instanceof Error ? cause.message : '加载可用性观测失败'
  } finally {
    if (controller === current) loading.value = false
  }
}

watch([keyword, stateFilter, channelFilter], () => { page.value = 1 })
watch(() => props.fixedChannelId, () => { page.value = 1; expanded.value = '' })
watch(pageCount, count => { page.value = Math.max(1, Math.min(page.value, count)) })
watch(() => props.channelType, () => {
  page.value = 1
  expanded.value = ''
  channelFilter.value = ''
  void refresh()
}, { immediate: true })
onUnmounted(() => controller?.abort())
</script>

<style scoped>
.availability-toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; margin-bottom: 12px; }
.availability-toolbar .v-text-field { flex: 2; min-width: 200px; }
.availability-toolbar .v-select { flex: 1; min-width: 140px; }
.availability-caption { color: rgb(var(--v-theme-on-surface-variant)); font-size: 0.8125rem; line-height: 1.7; margin-bottom: 10px; }
.availability-table th { white-space: nowrap; }
.model-name { overflow-wrap: anywhere; min-width: 140px; max-width: 280px; font-size: 0.8125rem; }
.availability-trend { display: flex; gap: 3px; min-width: 200px; }
.availability-trend span { flex: 1; height: 24px; border-radius: 2px; }
.availability-trend span:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: 2px; }
.is-empty { background: rgba(var(--v-theme-on-surface), 0.15); }
.is-healthy { background: rgb(var(--v-theme-success)); }
.is-degraded { background: rgb(var(--v-theme-warning)); }
.availability-detail { padding: 16px !important; background: rgba(var(--v-theme-on-surface), 0.025); }
.candidate-row { display: flex; flex-wrap: wrap; align-items: center; gap: 12px 24px; padding: 10px 0; border-top: 1px solid rgba(var(--v-theme-outline), 0.18); font-size: 0.8125rem; }
.candidate-row small { display: block; margin-top: 4px; color: rgb(var(--v-theme-on-surface-variant)); }
</style>
