<template>
  <div class="availability-panel">
    <div class="availability-toolbar">
      <v-text-field v-model="keyword" label="搜索模型或渠道" prepend-inner-icon="mdi-magnify" hide-details density="compact" clearable class="availability-search" />
      <v-select v-model="poolFilter" :items="poolOptions" label="分组" hide-details density="compact" />
      <v-select v-model="channelFilter" :items="channelOptions" label="渠道" hide-details density="compact" />
      <v-select v-model="stateFilter" :items="stateOptions" label="观测状态" hide-details density="compact" />
    </div>

    <div class="availability-meta">
      <span class="text-caption text-medium-emphasis">
        {{ snapshot ? `更新于 ${formatTime(snapshot.generatedAt)}` : '尚未加载观测数据' }}
        <span v-if="error && snapshot"> · 保留上次快照</span>
      </span>
      <div class="availability-actions">
        <v-menu :close-on-content-click="false" max-width="360">
          <template #activator="{ props: menuProps }">
            <v-btn v-bind="menuProps" variant="text" size="small" prepend-icon="mdi-information">统计口径</v-btn>
          </template>
          <v-card class="pa-4 text-body-2 availability-help">
            <p>每行对应一个渠道下的实际模型，包含配置中的模型和真实请求中出现的模型。</p>
            <p>仅统计真实请求中的上游尝试，不主动测试。参数错误、内容拒绝和客户端取消不计入成功率。</p>
            <p>状态参考最近 1 小时；正常需至少 {{ snapshot?.minSamples ?? 5 }} 个有效样本且成功率 ≥ 95%。未调用或样本不足时显示未知。</p>
            <p>趋势展示最近 24 个小时桶（含当前小时），灰色为无有效样本。免费池参考这些观测选模型，普通渠道调度不变。</p>
            <p>列表优先展示近 1 小时有有效请求的模型，按成功率降序；其次展示仅有 24 小时历史的模型，无有效样本的排在最后。</p>
          </v-card>
        </v-menu>
        <v-btn variant="tonal" size="small" prepend-icon="mdi-refresh" :loading="loading" @click="refresh">刷新</v-btn>
      </div>
    </div>
    <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mx-6 mb-3" role="alert">{{ error }}</v-alert>

    <v-progress-linear :active="loading" indeterminate color="primary" aria-label="加载可用性观测" />
    <v-table fixed-header class="availability-table" density="comfortable">
      <thead>
        <tr><th scope="col">实际模型</th><th scope="col">渠道 / 分组</th><th scope="col">状态</th><th scope="col">近 1 小时成功率</th><th scope="col">24 小时趋势</th><th scope="col">最近请求</th><th scope="col">明细</th></tr>
      </thead>
      <tbody>
        <template v-for="row in visibleRows" :key="rowKey(row)">
          <tr>
            <td class="model-cell"><strong class="model-name">{{ row.model }}</strong></td>
            <td class="channel-cell">
              <span>{{ row.channelName || '未命名渠道' }}</span>
              <div class="text-caption text-medium-emphasis">{{ poolName(row) }}</div>
            </td>
            <td><v-chip :color="stateMeta[row.state].color" size="small" variant="tonal">{{ stateMeta[row.state].label }}</v-chip></td>
            <td>
              <strong>{{ successRate(row.recent) }}</strong>
              <div class="text-caption text-medium-emphasis">{{ eligible(row.recent) }} 次有效请求</div>
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
            <td class="text-caption last-seen">{{ formatTime(row.lastSeen) }}</td>
            <td><v-btn size="small" variant="text" :aria-label="`${row.channelName} 的 ${row.model} 观测明细`" :aria-expanded="expanded === rowKey(row)" @click="toggle(row)">{{ expanded === rowKey(row) ? '收起' : '明细' }}</v-btn></td>
          </tr>
          <tr v-if="expanded === rowKey(row)">
            <td colspan="7" class="availability-detail">
              <p class="text-body-2 mb-2">24 小时成功率 {{ successRate(row) }} · {{ eligible(row) }} 次有效请求</p>
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
        <tr v-if="!visibleRows.length">
          <td colspan="7" class="availability-empty">
            {{ loading ? '正在加载模型观测…' : error ? '模型观测加载失败，请重试。' : snapshot?.rows.length ? '没有符合筛选条件的模型。' : '暂无模型。配置中的模型和实际请求过的模型会出现在这里。' }}
          </td>
        </tr>
      </tbody>
    </v-table>
    <footer class="availability-footer">
      <span class="text-caption text-medium-emphasis">共 {{ snapshot?.rows.length ?? 0 }} 条模型记录<span v-if="filteredRows.length !== snapshot?.rows.length && snapshot"> · 筛选后 {{ filteredRows.length }} 条</span></span>
      <v-pagination v-if="pageCount > 1" v-model="page" :length="pageCount" :total-visible="3" density="comfortable" />
    </footer>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import type { ApiTab } from '@/services/channel-api'
import type { Channel } from '@/types/channel'
import { getModelAvailability } from '@/services/model-availability'
import { useModelAvailabilityStore } from '@/stores/modelAvailability'
import { useChannelStore } from '@/stores/channel'
import type { AvailabilityCounts, AvailabilityState, ModelAvailabilityRow } from '@/types/model-availability'

const props = defineProps<{ channelType: ApiTab; channels: Channel[] }>()
const store = useModelAvailabilityStore()
const channelStore = useChannelStore()
const snapshot = computed(() => store.snapshots[props.channelType])
const channelsById = computed(() => new Map(props.channels.map(channel => [channel.id, channel])))
const pools = computed(() => channelStore.channelPoolsMap[props.channelType])
const poolsById = computed(() => new Map(pools.value.map(pool => [pool.id, pool])))
const loading = ref(false)
const error = ref('')
const keyword = ref<string | null>('')
const stateFilter = ref<AvailabilityState | 'all'>('all')
const channelFilter = ref('')
const poolFilter = ref('')
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
const poolOptions = computed(() => [
  { title: '全部分组', value: '' },
  ...pools.value.map(pool => ({ title: pool.name, value: pool.id }))
])
function poolId(row: ModelAvailabilityRow) {
  const channel = channelsById.value.get(row.channelId)
  return channel ? channel.poolId || 'default' : undefined
}
function poolName(row: ModelAvailabilityRow) {
  const id = poolId(row)
  return id ? poolsById.value.get(id)?.name ?? '未找到分组' : '未找到渠道配置'
}
const filteredRows = computed(() => {
  const query = (keyword.value ?? '').trim().toLowerCase()
  return (snapshot.value?.rows ?? []).filter(row =>
    (!channelFilter.value || row.channelId === channelFilter.value) &&
    (!poolFilter.value || poolId(row) === poolFilter.value) &&
    (stateFilter.value === 'all' || row.state === stateFilter.value) &&
    (!query || `${row.model} ${row.channelName}`.toLowerCase().includes(query))
  ).sort((left, right) => {
    const period = (row: ModelAvailabilityRow) => eligible(row.recent) ? 0 : eligible(row) ? 1 : 2
    const leftPeriod = period(left)
    const rightPeriod = period(right)
    if (leftPeriod !== rightPeriod) return leftPeriod - rightPeriod
    const leftCounts = leftPeriod === 0 ? left.recent : left
    const rightCounts = rightPeriod === 0 ? right.recent : right
    const leftTotal = eligible(leftCounts)
    const rightTotal = eligible(rightCounts)
    if (leftTotal && rightTotal) {
      const rateOrder = rightCounts.success / rightTotal - leftCounts.success / leftTotal
      if (rateOrder) return rateOrder
      if (leftTotal !== rightTotal) return rightTotal - leftTotal
    }
    return right.lastSeen - left.lastSeen
  })
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
  const results = await Promise.allSettled([
    getModelAvailability(type, current.signal),
    channelStore.refreshChannelPools(type)
  ])
  if (current.signal.aborted || type !== props.channelType) return
  const [models] = results
  if (models.status === 'fulfilled') store.replace(type, models.value)
  error.value = results.flatMap((result, index) => {
    if (result.status === 'fulfilled') return []
    const message = result.reason instanceof Error ? result.reason.message : String(result.reason)
    return [`${index === 0 ? '模型观测' : '分组'}加载失败：${message}`]
  }).join('；')
  if (controller === current) loading.value = false
}

watch([keyword, stateFilter, channelFilter, poolFilter], () => { page.value = 1; expanded.value = '' })
watch(page, () => { expanded.value = '' })
watch(pageCount, count => { page.value = Math.max(1, Math.min(page.value, count)) })
watch(() => props.channelType, () => {
  page.value = 1
  expanded.value = ''
  channelFilter.value = ''
  poolFilter.value = ''
  void refresh()
}, { immediate: true })
onUnmounted(() => controller?.abort())
</script>

<style scoped>
.availability-panel { display: flex; flex-direction: column; flex: 1; min-width: 0; min-height: 0; }
.availability-toolbar { display: grid; grid-template-columns: minmax(220px, 2fr) repeat(3, minmax(140px, 1fr)); align-items: center; gap: 12px; padding: 0 24px 12px; flex-shrink: 0; }
.availability-meta, .availability-actions { display: flex; align-items: center; gap: 8px; }
.availability-meta { justify-content: space-between; flex-wrap: wrap; padding: 0 24px 12px; flex-shrink: 0; }
.availability-help { line-height: 1.7; }
.availability-help p + p { margin-top: 12px; }
.availability-table { flex: 1; min-width: 0; min-height: 0; border-radius: 0; }
.availability-table :deep(.v-table__wrapper > table) { min-width: 1080px; }
.availability-table th { white-space: nowrap; }
.availability-table td { padding-top: 12px !important; padding-bottom: 12px !important; font-variant-numeric: tabular-nums; }
.model-cell { min-width: 180px; max-width: 280px; overflow-wrap: anywhere; }
.model-name { font-size: 0.8125rem; }
.channel-cell { min-width: 140px; max-width: 200px; overflow-wrap: anywhere; }
.last-seen { width: 140px; }
.availability-trend { display: flex; gap: 3px; width: 220px; }
.availability-trend span { flex: 1; height: 20px; border-radius: 2px; }
.availability-trend span:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: 2px; }
.is-empty { background: rgba(var(--v-theme-on-surface), 0.15); }
.is-healthy { background: rgb(var(--v-theme-success)); }
.is-degraded { background: rgb(var(--v-theme-warning)); }
.availability-detail { padding: 16px !important; background: rgba(var(--v-theme-on-surface), 0.025); }
.candidate-row { display: flex; flex-wrap: wrap; align-items: center; gap: 12px 24px; padding: 10px 0; border-top: 1px solid rgba(var(--v-theme-outline), 0.18); font-size: 0.8125rem; }
.candidate-row small { display: block; margin-top: 4px; color: rgb(var(--v-theme-on-surface-variant)); }
.candidate-row span { overflow-wrap: anywhere; }
.availability-empty { height: 180px !important; text-align: center; color: rgb(var(--v-theme-on-surface-variant)); }
.availability-footer { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 8px; min-height: 60px; padding: 8px 24px; border-top: 1px solid rgba(var(--v-theme-outline), 0.24); flex-shrink: 0; }
@media (max-width: 900px) {
  .availability-toolbar { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; }
}
@media (max-width: 700px) {
  .availability-toolbar { padding: 0 16px 12px; }
  .availability-meta { padding: 0 16px 12px; }
  .availability-footer { padding: 8px 16px; }
}
@media (max-height: 540px) {
  .availability-panel { overflow-y: auto; }
  .availability-table { flex: none; min-height: 160px; }
}
</style>
