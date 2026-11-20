<template>
  <div class="request-logs-view" :class="{ 'system-tab-active': activeTab === 'system' }">
    <PageHeader title="请求与会话观测" description="查看上游尝试、会话轨迹，以及模型每次调用的上下文与变化。" />
    <LogStorageStatus />
    <div class="logs-toolbar">
      <UiTabs v-model="activeTab" color="primary" density="comfortable" class="logs-tabs">
        <UiButton role="tab" variant="text" value="request">模型调用</UiButton>
        <UiButton role="tab" variant="text" value="stats">统计概览</UiButton>
        <UiButton role="tab" variant="text" value="system">会话轨迹</UiButton>
        <UiButton role="tab" variant="text" value="runtime">运行日志</UiButton>
      </UiTabs>
    </div>

    <div v-show="activeTab === 'request'" class="request-tab-panel">
      <div class="logs-actions mb-4">
        <UiInput
          v-model="searchText"
          label="搜索请求、会话、渠道、模型或错误"
          prepend-inner-icon="mdi-magnify"
          density="compact"
          hide-details
          clearable
          class="log-search-field"
          @keyup.enter="resetAndLoad"
        />
        <UiSelect v-model="datePreset" :items="datePresetItems" label="时间范围" density="compact" variant="outlined" hide-details class="date-preset-select" />
        <UiSelect v-model="typeFilter" :items="typeItems" label="入口类型" density="compact" hide-details class="type-filter-select" />
        <UiSelect v-model="statusFilter" :items="logStatusItems" label="状态" density="compact" hide-details class="type-filter-select" />
        <UiSelect v-model="sortField" :items="sortItems" label="排序字段" density="compact" hide-details class="type-filter-select" />
        <UiButton variant="outlined" size="small" class="sort-order-button" @click="sortOrder = sortOrder === 'desc' ? 'asc' : 'desc'">
          {{ sortOrder === 'desc' ? '降序' : '升序' }}
        </UiButton>
        <span class="logs-count text-sm text-muted-foreground" role="status">共 {{ totalLogs.toLocaleString() }} 次尝试</span>
        <UiButton color="primary" variant="tonal" size="small" prepend-icon="mdi-refresh" :loading="loading" @click="loadLogs">刷新</UiButton>
        <UiButton variant="tonal" size="small" prepend-icon="mdi-download" :disabled="!requestLogs.length" @click="exportCurrentPage">导出本页</UiButton>
        <UiButton color="error" variant="tonal" size="small" prepend-icon="mdi-broom" @click="openPurgeDialog">清理日志</UiButton>
      </div>

      <details class="advanced-filters mb-4">
        <summary>更多筛选与自定义时间</summary>
        <div class="advanced-filters__grid">
          <UiInput v-model="channelFilter" label="渠道名称（精确）" density="compact" hide-details clearable />
          <UiInput v-model="modelFilter" label="实际模型（精确）" density="compact" hide-details clearable />
          <UiInput v-model="errorTypeFilter" label="错误类型" density="compact" hide-details clearable />
          <UiInput v-model="upstreamFilter" label="上游地址" density="compact" hide-details clearable />
          <UiInput v-model="keyMaskFilter" label="密钥掩码" density="compact" hide-details clearable />
          <UiSelect v-model="retriedFilter" :items="booleanFilterItems" label="是否重试" density="compact" hide-details />
          <UiSelect v-model="streamFilter" :items="booleanFilterItems" label="是否流式" density="compact" hide-details />
          <UiInput v-if="datePreset === 'custom'" v-model="fromTime" type="datetime-local" label="开始时间" density="compact" hide-details />
          <UiInput v-if="datePreset === 'custom'" v-model="toTime" type="datetime-local" label="结束时间" density="compact" hide-details />
          <UiButton variant="text" color="primary" prepend-icon="mdi-filter-remove-outline" @click="clearFilters">清除全部筛选</UiButton>
        </div>
      </details>

      <UiAlert v-if="purgeNotice" type="success" variant="tonal" density="compact" class="mb-4" closable @click:close="purgeNotice = ''">
        {{ purgeNotice }}
      </UiAlert>

      <UiAlert v-if="error" type="error" variant="tonal" density="compact" class="mb-4">
        {{ error }}
        <span v-if="requestLogs.length">；下方保留上次成功加载的结果。</span>
      </UiAlert>

      <UiCard variant="flat" class="logs-table-card" :aria-busy="loading">
        <UiProgress :active="loading" :indeterminate="loading" color="primary" height="2" />
        <UiTable density="comfortable" class="request-logs-table" fixed-header>
          <thead>
            <tr>
              <th>时间</th>
              <th>入口</th>
              <th>渠道</th>
              <th>状态</th>
              <th>模型转换</th>
              <th>首字/耗时</th>
              <th>Token I/O</th>
              <th>缓存</th>
              <th>输出速度</th>
              <th>终止/失败原因</th>
              <th class="action-column">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="loading && requestLogs.length === 0">
              <td colspan="11" class="text-center text-muted-foreground py-8">
                <UiSpinner indeterminate size="24" width="2" color="primary" />
              </td>
            </tr>
            <tr v-else-if="!loading && requestLogs.length === 0">
              <td colspan="11"><EmptyState variant="inline" icon="mdi-text-box-search-outline" title="暂无请求日志" description="当前筛选条件下没有请求记录，可放宽时间范围或更换模型。" /></td>
            </tr>
            <tr v-for="log in requestLogs" :key="log.attemptId">
              <td class="whitespace-nowrap">{{ formatTime(log.timestamp) }}</td>
              <td>
                <UiBadge size="x-small" :color="entryColor(log.apiType)" variant="tonal">
                  {{ entryLabel(log.apiType) }}
                </UiBadge>
              </td>
              <td class="channel-cell">
                <div class="font-medium">{{ log.channelName || `#${log.channelIndex}` }}</div>
                <div class="text-xs text-muted-foreground">{{ log.baseUrl || '--' }}</div>
              </td>
              <td>
                <UiBadge size="x-small" :color="statusColor(log.status)" variant="tonal">
                  {{ formatStatus(log) }}
                </UiBadge>
                <div class="text-xs text-muted-foreground">{{ formatStatusMeta(log) }}</div>
              </td>
              <td class="model-cell">
                <div>{{ log.transform || log.resolvedModel || log.model || '--' }}</div>
                <div v-if="log.keyMask" class="text-xs text-muted-foreground">{{ log.keyMask }}</div>
              </td>
              <td class="whitespace-nowrap">
                <div>首 {{ formatSeconds(log.firstTokenMs) }}</div>
                <div class="text-xs text-muted-foreground">总 {{ formatSeconds(log.durationMs) }}</div>
              </td>
              <td class="whitespace-nowrap">
                {{ formatPair(log.inputTokens, log.outputTokens) }}
                <div class="text-xs text-muted-foreground">
                  {{ formatPairPercent(log.inputTokens, log.outputTokens, 'I', 'O') }}
                </div>
              </td>
              <td class="whitespace-nowrap">
                <div>{{ formatNumber(log.cacheReadTokens) }}</div>
                <div class="text-xs text-muted-foreground">命中 {{ formatCacheHitRate(log) }}</div>
              </td>
              <td class="whitespace-nowrap performance-cell">
                <div>{{ formatTPS(log.tps) }}</div>
                <div class="text-xs text-muted-foreground">TPM {{ formatTPM(log.tpm) }}</div>
              </td>
              <td class="error-cell">
                <button v-if="log.errorType || log.errorMessage" type="button" class="error-summary" aria-label="查看完整终止或失败原因" @click="selectedFailure = log">
                  <span class="error-summary__heading">
                    <span>{{ formatErrorType(log.errorType) }}</span>
                    <UiIcon size="16" aria-hidden="true">mdi-chevron-right</UiIcon>
                  </span>
                  <span v-if="log.errorMessage" class="error-summary__message">{{ log.errorMessage }}</span>
                </button>
                <span v-else class="text-muted-foreground">--</span>
              </td>
              <td class="action-cell">
                <UiTooltip text="查看流量详情">
                  <template #activator="{ props: tooltipProps }">
                    <UiButton
                      v-bind="tooltipProps"
                      icon="mdi-text-box-search-outline"
                      size="small"
                      variant="text"
                      aria-label="查看流量详情"
                      :disabled="!log.requestId"
                      @click="openCallDetail(log.requestId)"
                    />
                  </template>
                </UiTooltip>
                <UiTooltip text="查看详情日志">
                  <template #activator="{ props: tooltipProps }">
                    <UiButton
                      v-bind="tooltipProps"
                      icon="mdi-file-find-outline"
                      size="small"
                      variant="text"
                      aria-label="查看详情日志"
                      :disabled="!log.requestId"
                      @click="openSystemLog(log.requestId)"
                    />
                  </template>
                </UiTooltip>
              </td>
            </tr>
          </tbody>
        </UiTable>
        <div class="logs-table-footer">
          <span class="text-xs text-muted-foreground">第 {{ page }} / {{ totalPages }} 页 · 当前 {{ requestLogs.length }} 条</span>
          <UiSelect v-model="pageSize" :items="[25, 50, 100]" label="每页" density="compact" hide-details class="page-size-select" />
          <UiPagination v-model="page" :length="totalPages" :total-visible="5" density="compact" size="small" />
        </div>
      </UiCard>
    </div>

    <div v-show="activeTab === 'stats'" class="stats-tab-panel">
      <RequestStatsPanel v-if="activeTab === 'stats'" show-protocol @drill="applyStatsDrill" />
    </div>

    <RequestFailureDialog v-model="selectedFailure" @locate="openSystemLog" />
    <AppDialog v-model="callDetailOpen" title="请求流量详情" size="xl">
      <RequestLogDetail v-if="callDetailOpen" :request-id="callDetailId" :source-version="callDetailVersion" @locate="locateFromDetail" />
    </AppDialog>

    <SystemLogsPanel
      v-if="activeTab === 'system'"
      :request-id="systemRequestId"
      :highlight-request-id="highlightRequestId"
      @select="openSystemLog"
      @back="backToRequestTab"
    />
    <RuntimeLogsPanel v-if="activeTab === 'runtime'" />

    <AppDialog v-model="purgeDialog" title="清理日志" description="清理 logs.db 全部日志（运行日志、请求正文、请求记录），不可恢复，清理后自动收缩数据库文件。" size="sm" :busy="purging">
      <UiToggleGroup v-model="purgeScope" mandatory density="compact" variant="outlined" divided class="mb-3">
        <UiButton value="day" size="small">按天清理</UiButton>
        <UiButton value="all" size="small">清理全部</UiButton>
      </UiToggleGroup>
      <UiSelect
        v-if="purgeScope === 'day'"
        v-model="purgeDate"
        :items="purgeDateItems"
        item-title="title"
        item-value="date"
        label="选择要清理的日期"
        density="compact"
        hide-details
        :loading="purgeDatesLoading"
        no-data-text="数据库中暂无可清理的日志"
      />
      <UiAlert v-if="purgeError" type="error" variant="tonal" density="compact" class="mt-3">
        {{ purgeError }}
      </UiAlert>
      <template #actions>
        <UiButton variant="text" :disabled="purging" @click="purgeDialog = false">取消</UiButton>
        <UiButton
          color="error"
          variant="flat"
          prepend-icon="mdi-delete"
          :loading="purging"
          :disabled="purgeScope === 'day' && !purgeDate"
          @click="confirmPurge"
        >
          {{ purgeScope === 'all' ? '确认清理全部' : '确认清理该日' }}
        </UiButton>
      </template>
    </AppDialog>
  </div>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiToggleGroup from '@/shared/ui/button/ToggleGroup.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiPagination from '@/shared/ui/data/Pagination.vue'
import UiTable from '@/shared/ui/data/Table.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiProgress from '@/shared/ui/feedback/Progress.vue'
import UiSpinner from '@/shared/ui/feedback/Spinner.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiTabs from '@/shared/ui/navigation/Tabs.vue'
import UiTooltip from '@/shared/ui/overlay/Tooltip.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { computed, onMounted, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import PageHeader from '@/shared/components/layout/PageHeader.vue'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'
import LogStorageStatus from '@/features/observability/components/log-panels/LogStorageStatus.vue'
import { useRoute, useRouter } from 'vue-router'
import RequestFailureDialog from '@/features/observability/components/log-panels/RequestFailureDialog.vue'
import SystemLogsPanel from '@/features/observability/components/log-panels/SystemLogsPanel.vue'
import RuntimeLogsPanel from '@/features/observability/components/log-panels/RuntimeLogsPanel.vue'
import RequestLogDetail from '@/features/observability/components/log-panels/RequestLogDetail.vue'
import RequestStatsPanel from '@/features/observability/components/stats/RequestStatsPanel.vue'
import { downloadCsv } from '@/features/observability/utils/exportCsv'
import { useLogsStore } from '@/features/observability/stores/logs'
import { formatDateTime, toDateTimeLocal } from '@/shared/utils/datetime'
import { api, type LogAPIType, type LogAvailableDate, type RequestLogEntry, type RequestStatsDrill } from '@/api'

type TypeFilter = LogAPIType | ''
type LogsTab = 'request' | 'stats' | 'system' | 'runtime'
type DatePreset = 'today' | 'yesterday' | 'all' | 'custom'

const typeLabelMap: Record<LogAPIType, string> = {
  messages: 'Messages',
  responses: 'Responses',
  gemini: 'Gemini',
  chat: 'Chat',
  images: 'Images',
  embeddings: 'Embeddings',
  decisions: 'Decisions',
  cmd_gate: '命令 AI 复核',
  llmgate: '内部 AI 调用'
}

const typeItems: Array<{ title: string; value: TypeFilter }> = [
  { title: '全部', value: '' },
  ...Object.entries(typeLabelMap).map(([value, title]) => ({ title, value: value as LogAPIType }))
]

const datePresetItems: Array<{ title: string; value: DatePreset }> = [
  { title: '今天', value: 'today' },
  { title: '昨天', value: 'yesterday' },
  { title: '全部', value: 'all' },
  { title: '自定义', value: 'custom' }
]

const typeFilter = ref<TypeFilter>('')
const logsStore = useLogsStore()
const callDetailOpen = ref(false)
const callDetailId = ref('')
const callDetailVersion = computed(() => {
  const attempts = requestLogs.value.filter(item => item.requestId === callDetailId.value)
  return JSON.stringify(attempts)
})
const datePreset = ref<DatePreset>('all')
const fromTime = ref('')
const toTime = ref('')
let exactCustomRange: DateRange | undefined
function setCustomRange(range: DateRange) {
  exactCustomRange = range
  datePreset.value = 'custom'
  fromTime.value = range.from ? toDateTimeLocal(range.from) : ''
  toTime.value = range.to ? toDateTimeLocal(range.to) : ''
}
const searchText = ref('')
const channelFilter = ref<string | null>(null)
const modelFilter = ref<string | null>(null)
const statusFilter = ref('')
const errorTypeFilter = ref<string | null>(null)
const upstreamFilter = ref<string | null>(null)
const keyMaskFilter = ref<string | null>(null)
const retriedFilter = ref('')
const streamFilter = ref('')
const sortField = ref<'timestamp' | 'durationMs' | 'firstTokenMs' | 'inputTokens' | 'outputTokens' | 'totalTokens'>('timestamp')
const sortOrder = ref<'asc' | 'desc'>('desc')
const page = ref(1)
const pageSize = ref(50)
const totalLogs = ref(0)
const totalPages = computed(() => Math.max(1, Math.ceil(totalLogs.value / pageSize.value)))
const logStatusItems = [
  { title: '全部状态', value: '' }, { title: '已完成', value: 'completed' }, { title: '失败', value: 'failed' },
  { title: '已取消', value: 'cancelled' }, { title: '未正常收尾', value: 'interrupted' }, { title: '已拦截', value: 'blocked' }, { title: '状态未知', value: 'unknown' }
]
const booleanFilterItems = [{ title: '全部', value: '' }, { title: '是', value: 'true' }, { title: '否', value: 'false' }]
const sortItems = [
  { title: '按时间', value: 'timestamp' }, { title: '按总耗时', value: 'durationMs' }, { title: '按首字耗时', value: 'firstTokenMs' },
  { title: '按输入 Token', value: 'inputTokens' }, { title: '按输出 Token', value: 'outputTokens' }, { title: '按总 Token', value: 'totalTokens' }
]
const loading = ref(false)
const error = ref('')
const requestLogs = shallowRef<RequestLogEntry[]>([])
const selectedFailure = shallowRef<RequestLogEntry | null>(null)

const highlightRequestId = ref('')

const purgeDialog = ref(false)
const purgeScope = ref<'day' | 'all'>('day')
const purgeDate = ref('')
const purgeDates = ref<LogAvailableDate[]>([])
const purgeDatesLoading = ref(false)
const purging = computed(() => logsStore.purging)
const purgeError = ref('')

const route = useRoute()
const router = useRouter()

const routeValue = (key: string): string => String(route.query[key] || '')
if (routeValue('type')) typeFilter.value = routeValue('type') as TypeFilter
channelFilter.value = routeValue('channel') || null
modelFilter.value = routeValue('model') || null
statusFilter.value = routeValue('status')
errorTypeFilter.value = routeValue('errorType') || null
upstreamFilter.value = routeValue('upstream') || null
keyMaskFilter.value = routeValue('keyMask') || null
if (routeValue('from') || routeValue('to')) {
  setCustomRange({ from: routeValue('from'), to: routeValue('to') })
}

const activeTab = computed({
  get: (): LogsTab => {
    if (route.query.tab === 'system') return 'system'
    if (route.query.tab === 'runtime') return 'runtime'
    if (route.query.tab === 'stats') return 'stats'
    return 'request'
  },
  set: (tab: LogsTab) => {
    const query = { ...route.query }
    if (tab !== 'request') {
      query.tab = tab
      if (tab === 'runtime') delete query.id
    } else {
      delete query.tab
      delete query.id
    }
    router.replace({ query })
  }
})

const systemRequestId = computed(() => String(route.query.id || ''))

const openSystemLog = (requestId: string) => {
  if (!requestId) return
  highlightRequestId.value = requestId
  router.replace({
    query: {
      ...route.query,
      tab: 'system',
      id: requestId
    }
  })
}

const backToRequestTab = () => {
  highlightRequestId.value = ''
  activeTab.value = 'request'
}

const openCallDetail = (requestId: string) => {
  if (!requestId) return
  callDetailId.value = requestId
  callDetailOpen.value = true
}
const locateFromDetail = (requestId: string) => {
  callDetailOpen.value = false
  openSystemLog(requestId)
}

type DateRange = { from: string; to: string }

const dayRangeRFC = (offsetDays: number): DateRange => {
  const base = new Date()
  const from = new Date(base.getFullYear(), base.getMonth(), base.getDate() - offsetDays, 0, 0, 0)
  const to = new Date(base.getFullYear(), base.getMonth(), base.getDate() - offsetDays, 23, 59, 59, 999)
  return { from: from.toISOString(), to: to.toISOString() }
}

const customRangeRFC = (): DateRange | null => {
  if (!fromTime.value && !toTime.value) return { from: '', to: '' }
  // 下钻传入的时间可能包含秒和毫秒。日期控件以分钟显示，手动编辑前保留查询边界。
  const fromValue = exactCustomRange?.from && fromTime.value === toDateTimeLocal(exactCustomRange.from) ? exactCustomRange.from : fromTime.value
  const toValue = exactCustomRange?.to && toTime.value === toDateTimeLocal(exactCustomRange.to) ? exactCustomRange.to : toTime.value
  const from = fromValue ? new Date(fromValue) : null
  const to = toValue ? new Date(toValue) : null
  if (from && Number.isNaN(from.getTime())) {
    error.value = '开始时间无效'
    return null
  }
  if (to && Number.isNaN(to.getTime())) {
    error.value = '结束时间无效'
    return null
  }
  if (from && to && from > to) {
    error.value = '开始时间不能晚于结束时间'
    return null
  }
  return { from: from ? from.toISOString() : '', to: to ? to.toISOString() : '' }
}

const resolveDateRange = (): DateRange | null => {
  if (datePreset.value === 'today') return dayRangeRFC(0)
  if (datePreset.value === 'yesterday') return dayRangeRFC(1)
  if (datePreset.value === 'all') return { from: '', to: '' }
  return customRangeRFC()
}

let logRequestSequence = 0
const loadLogs = async () => {
  const sequence = ++logRequestSequence
  const range = resolveDateRange()
  if (!range) {
    loading.value = false
    return
  }
  loading.value = true
  error.value = ''
  try {
    const response = await api.getRequestLogs({
      type: typeFilter.value,
      limit: 50,
      page: page.value,
      pageSize: pageSize.value,
      from: range.from,
      to: range.to,
      q: searchText.value.trim(),
      channel: channelFilter.value || '',
      model: modelFilter.value || '',
      status: statusFilter.value,
      errorType: errorTypeFilter.value || '',
      upstream: upstreamFilter.value || '',
      keyMask: keyMaskFilter.value || '',
      retried: retriedFilter.value ? retriedFilter.value === 'true' : undefined,
      stream: streamFilter.value ? streamFilter.value === 'true' : undefined,
      sort: sortField.value,
      order: sortOrder.value
    })
    if (sequence === logRequestSequence) {
      requestLogs.value = response.logs || []
      totalLogs.value = response.total ?? response.logs?.length ?? 0
    }
  } catch (err) {
    if (sequence === logRequestSequence) error.value = err instanceof Error ? err.message : '加载请求日志失败'
  } finally {
    if (sequence === logRequestSequence) loading.value = false
  }
}

function formatTPS(value: number | undefined): string {
  return value && value > 0 ? `${value.toFixed(value >= 100 ? 0 : 1)} /s` : '--'
}

function exportCurrentPage() {
  downloadCsv(`模型调用-第${page.value}页.csv`,
    ['时间', '请求 ID', '尝试 ID', '入口', '状态', '渠道', '请求模型', '实际模型', '总耗时(ms)', '首字(ms)', '输入 Token', '输出 Token', '缓存创建', '缓存读取', '重试', '流式', '错误类型', '错误原因'],
    requestLogs.value.map(log => [
      log.timestamp, log.requestId || '', log.attemptId || '', log.entry || log.apiType, log.status,
      log.channelName || '', log.model, log.resolvedModel || '', log.durationMs || '', log.firstTokenMs || '',
      log.inputTokens || 0, log.outputTokens || 0, log.cacheCreationTokens || 0, log.cacheReadTokens || 0,
      log.retried ? '是' : '否', log.stream ? '是' : '否', log.errorType || '', log.errorMessage || ''
    ]))
}

function clearFilters() {
  datePreset.value = 'all'
  exactCustomRange = undefined
  fromTime.value = ''
  toTime.value = ''
  searchText.value = ''
  typeFilter.value = ''
  channelFilter.value = null
  modelFilter.value = null
  statusFilter.value = ''
  errorTypeFilter.value = null
  upstreamFilter.value = null
  keyMaskFilter.value = null
  retriedFilter.value = ''
  streamFilter.value = ''
  scheduleLoad()
}

function applyStatsDrill(filters: RequestStatsDrill) {
  if (filters.from || filters.to) {
    setCustomRange({ from: filters.from || '', to: filters.to || '' })
  } else {
    datePreset.value = 'all'
    exactCustomRange = undefined
  }
  typeFilter.value = (filters.type || '') as TypeFilter
  channelFilter.value = filters.channel || null
  modelFilter.value = filters.model || null
  statusFilter.value = filters.status || ''
  errorTypeFilter.value = filters.errorType || null
  upstreamFilter.value = filters.upstream || null
  keyMaskFilter.value = filters.keyMask || null
  searchText.value = ''
  retriedFilter.value = ''
  streamFilter.value = ''
  activeTab.value = 'request'
  scheduleLoad()
}

const purgeDateItems = computed(() =>
  purgeDates.value.map(item => ({
    title: `${item.date}（${item.count} 条）`,
    date: item.date
  }))
)

const openPurgeDialog = async () => {
  purgeDialog.value = true
  purgeError.value = ''
  purgeDatesLoading.value = true
  try {
    const response = await api.getLogDates()
    purgeDates.value = response.dates || []
    if (!purgeDate.value && purgeDates.value.length > 0) {
      purgeDate.value = purgeDates.value[0].date
    }
  } catch (err) {
    purgeError.value = err instanceof Error ? err.message : '加载可清理日期失败'
  } finally {
    purgeDatesLoading.value = false
  }
}

const confirmPurge = async () => {
  if (purging.value) return
  purgeError.value = ''
  try {
    const payload: { scope: 'all' | 'day'; date?: string } =
      purgeScope.value === 'all' ? { scope: 'all' } : { scope: 'day', date: purgeDate.value }
    const total = await logsStore.purgeLogs(payload)
    purgeDialog.value = false
    purgeDates.value = []
    purgeDate.value = ''
    callDetailOpen.value = false
    await loadLogs()
    purgeNotice.value = `清理完成，共删除 ${total.toLocaleString()} 条日志记录`
  } catch (err) {
    purgeError.value = err instanceof Error ? err.message : '清理日志失败'
  }
}

const purgeNotice = ref('')
watch(purgeNotice, (value, _oldValue, onCleanup) => {
  if (!value) return
  const timer = window.setTimeout(() => {
    if (purgeNotice.value === value) purgeNotice.value = ''
  }, 6000)
  onCleanup(() => window.clearTimeout(timer))
})

const typeColorMap: Record<LogAPIType, string> = {
  messages: 'primary',
  responses: 'secondary',
  gemini: 'success',
  chat: 'info',
  images: 'warning',
  embeddings: 'info',
  decisions: 'success',
  cmd_gate: 'warning',
  llmgate: 'secondary'
}

const entryLabel = (value?: string) => typeLabelMap[(value || '').toLowerCase() as LogAPIType] || value || '未知'
const entryColor = (value?: string) => typeColorMap[(value || '').toLowerCase() as LogAPIType] || 'secondary'

const statusColor = (status: string) => {
  if (status === 'completed') return 'success'
  if (status === 'cancelled') return 'warning'
  return 'error'
}

const formatStatus = (log: RequestLogEntry) => {
  if (log.status === 'cancelled') return '客户端中止'
  const label = log.status === 'completed' ? '成功' : '失败'
  return log.statusCode ? `${label} ${log.statusCode}` : label
}

const formatStatusMeta = (log: RequestLogEntry) => {
  const parts = [log.statusCode && log.status === 'cancelled' ? `上游 ${log.statusCode}` : '', log.stream ? 'stream' : 'normal']
  return parts.filter(Boolean).join(' · ')
}

const formatErrorType = (value?: string) => value === 'client_cancelled' ? '客户端已中止' : value || 'error'

const formatTime = (value?: string) => {
  if (!value) return '--'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return formatDateTime(date)
}

const numberFormatter = new Intl.NumberFormat()
const formatNumber = (value?: number) => {
  if (!value) return '0'
  return numberFormatter.format(value)
}

const formatPair = (left?: number, right?: number) => {
  if (typeof left !== 'number' && typeof right !== 'number') return '--'
  return `${formatNumber(left)} / ${formatNumber(right)}`
}
const formatPairPercent = (left?: number, right?: number, leftLabel = 'L', rightLabel = 'R') => {
  const leftValue = left || 0
  const rightValue = right || 0
  const total = leftValue + rightValue
  if (total <= 0) return '--'
  const leftPercent = (leftValue / total * 100).toFixed(1)
  const rightPercent = (rightValue / total * 100).toFixed(1)
  return `${leftLabel} ${leftPercent}% / ${rightLabel} ${rightPercent}%`
}
const formatSeconds = (value?: number) => {
  if (!value || value <= 0) return '--'
  if (value < 1000) return `${value}ms`
  return `${(value / 1000).toFixed(1)}s`
}
const formatTPM = (value?: number) => value && value > 0 ? value.toFixed(1) : '--'
const formatCacheHitRate = (log: RequestLogEntry) => {
  const read = log.cacheReadTokens || 0
  const total = (log.inputTokens || 0) + (log.cacheCreationTokens || 0) + read
  if (total <= 0) return '--'
  return `${((read / total) * 100).toFixed(1)}%`
}

let filterTimer: ReturnType<typeof setTimeout> | undefined
function scheduleLoad(resetPage = true) {
  if (resetPage) page.value = 1
  if (filterTimer !== undefined) clearTimeout(filterTimer)
  filterTimer = setTimeout(() => {
    filterTimer = undefined
    if (activeTab.value === 'request') void loadLogs()
  }, 180)
}
function resetAndLoad() {
  if (filterTimer !== undefined) clearTimeout(filterTimer)
  filterTimer = undefined
  page.value = 1
  void loadLogs()
}

watch([typeFilter, statusFilter, channelFilter, modelFilter, errorTypeFilter, upstreamFilter, keyMaskFilter, retriedFilter, streamFilter, sortField, sortOrder], () => scheduleLoad())
watch(searchText, () => scheduleLoad())
watch(datePreset, () => { if (datePreset.value !== 'custom') scheduleLoad() })
watch([fromTime, toTime], () => { if (datePreset.value === 'custom') scheduleLoad() })
watch([page, pageSize], () => scheduleLoad(false))
watch(activeTab, tab => { if (tab === 'request') scheduleLoad() })
onMounted(() => { if (activeTab.value === 'request') void loadLogs() })
onBeforeUnmount(() => {
  logRequestSequence++
  if (filterTimer !== undefined) clearTimeout(filterTimer)
})
</script>

<style scoped>
@reference "@/styles/index.css";

.request-logs-view {
  @apply flex flex-col gap-4;
  /* 视口高度减去顶栏与内容区上下内边距，表格在页面内滚动。 */
  @apply [height:calc(100dvh_-_var(--app-header-height)_-_var(--app-content-gap)_-_var(--app-content-gap))] min-h-0 overflow-hidden;
}

.request-logs-view > :deep(.page-header) { @apply shrink-0 mb-0; }

.logs-toolbar {
  @apply flex items-center justify-start gap-4 p-0 shrink-0;
}

.logs-actions {
  @apply flex items-center gap-3 flex-wrap justify-start p-4 bg-card border border-border rounded-card;
}

.logs-tabs {
  @apply [flex:0_0_auto];
}

.logs-tabs :deep([role=tab]) {
  @apply [font-size:14px] font-semibold [letter-spacing:0] min-w-24;
}

.date-preset-select, .type-filter-select {
  @apply [flex:0_1_180px] min-w-36;
}
.logs-count { @apply [flex:1] whitespace-nowrap; }

.system-tab-active > :deep(.system-logs-panel) {
  @apply [flex:1_1_auto] min-h-0;
}

.request-tab-panel {
  @apply [flex:1_1_auto] min-h-0 flex flex-col overflow-y-auto pr-0.5;
}

.stats-tab-panel { @apply [flex:1_1_auto] min-h-0 p-0.5 overflow-y-auto; }
.log-search-field { @apply [flex:1_1_280px] min-w-60; }
.sort-order-button { @apply min-h-10; }
.advanced-filters { @apply shrink-0 [padding:10px_16px] text-muted-foreground [border:1px_solid_color-mix(in_srgb,_var(--outline)_20%,_transparent)] rounded-control [background:color-mix(in_srgb,_var(--surface-bright)_30%,_transparent)]; }
.advanced-filters summary { @apply [font-size:12px] cursor-pointer [user-select:none]; }
.advanced-filters__grid { @apply grid [grid-template-columns:repeat(auto-fit,_minmax(190px,_1fr))] gap-3 items-center pt-3.5; }
.logs-table-footer { @apply flex items-center justify-end gap-3 flex-wrap [padding:8px_12px] [border-top:1px_solid_color-mix(in_srgb,_var(--outline)_18%,_transparent)]; }
.logs-table-footer > span { @apply mr-auto; }
.page-size-select { @apply w-22.5 [flex:0_0_90px]; }
.performance-cell { @apply min-w-23; }

.action-cell {
  @apply whitespace-nowrap;
}

.logs-table-card {
  @apply flex flex-col [flex:1] min-h-0 overflow-hidden;
}

.request-logs-table {
  @apply [flex:1] min-h-0;
}
.request-logs-table :deep(table) { @apply min-w-310; }
.request-tab-panel .logs-actions { @apply shrink-0; }

.request-logs-table :deep(th) {
  @apply text-xs font-medium text-muted-foreground bg-muted/45 whitespace-nowrap;
}

.request-logs-table :deep(td) {
  @apply [vertical-align:top] [font-size:13px] py-3 tabular-nums;
}

.error-summary { @apply flex flex-col gap-1 w-full min-h-11 p-0 text-left rounded-badge; }
.error-summary__heading { @apply flex items-center justify-between gap-2 font-semibold text-foreground; }
.error-summary__heading > span { @apply min-w-0; }
.error-summary__heading .ui-icon { @apply shrink-0 text-muted-foreground; }
.error-summary:hover .error-summary__heading { @apply text-primary; }
.error-summary__message { @apply [display:-webkit-box] [-webkit-line-clamp:2] [-webkit-box-orient:vertical] overflow-hidden text-muted-foreground [font-size:0.75rem] [line-height:1.5]; }

.channel-cell,
.model-cell,
.error-cell {
  @apply min-w-37.5 max-w-65 [overflow-wrap:anywhere];
}

.dialog-title {
  @apply font-bold;
}

@media (max-width: 600px) {
  .request-logs-view {
    @apply min-h-120;
  }
  .date-preset-select, .type-filter-select { @apply [flex:1_1_140px]; }
}

@media (max-width: 900px) {
  .logs-toolbar {
    @apply items-start flex-col;
  }

  .logs-actions {
    @apply justify-start;
  }
}
</style>
