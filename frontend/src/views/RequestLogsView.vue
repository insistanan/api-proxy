<template>
  <div class="request-logs-view">
    <div class="logs-toolbar">
      <v-tabs v-model="activeTab" color="primary" density="comfortable" class="logs-tabs">
        <v-tab value="request">请求日志</v-tab>
        <v-tab value="system">系统日志</v-tab>
      </v-tabs>
    </div>

    <div v-show="activeTab === 'request'">
      <div class="logs-actions mb-4">
        <v-select
          v-model="datePreset"
          :items="datePresetItems"
          item-title="title"
          item-value="value"
          label="时间范围"
          density="compact"
          variant="outlined"
          hide-details
          class="date-preset-select"
        />
        <template v-if="datePreset === 'custom'">
          <v-text-field v-model="fromTime" type="datetime-local" label="开始时间" density="compact" hide-details />
          <v-text-field v-model="toTime" type="datetime-local" label="结束时间" density="compact" hide-details />
        </template>
        <v-chip size="small" variant="tonal">{{ requestLogs.length }}/50</v-chip>
        <v-btn-toggle v-model="typeFilter" mandatory density="compact" variant="outlined" divided>
          <v-btn v-for="item in typeItems" :key="item.value" :value="item.value" size="small">
            {{ item.title }}
          </v-btn>
        </v-btn-toggle>
        <v-btn
          color="primary"
          variant="tonal"
          size="small"
          prepend-icon="mdi-refresh"
          :loading="loading"
          @click="loadLogs"
        >
          刷新
        </v-btn>
        <v-btn
          color="error"
          variant="tonal"
          size="small"
          prepend-icon="mdi-broom"
          @click="openPurgeDialog"
        >
          清理日志
        </v-btn>
      </div>

      <v-alert v-if="purgeNotice" type="success" variant="tonal" density="compact" class="mb-4" closable @click:close="purgeNotice = ''">
        {{ purgeNotice }}
      </v-alert>

      <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mb-4">
        {{ error }}
      </v-alert>

      <v-card variant="flat" class="logs-table-card">
        <v-table density="compact" class="request-logs-table">
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
              <th>TPM</th>
              <th>终止/失败原因</th>
              <th class="action-column">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="loading">
              <td colspan="11" class="text-center text-medium-emphasis py-8">
                <v-progress-circular indeterminate size="24" width="2" color="primary" />
              </td>
            </tr>
            <tr v-else-if="requestLogs.length === 0">
              <td colspan="11" class="text-center text-medium-emphasis py-8">暂无请求日志</td>
            </tr>
            <tr v-for="log in requestLogs" :key="log.attemptId">
              <td class="text-no-wrap">{{ formatTime(log.timestamp) }}</td>
              <td>
                <v-chip size="x-small" :color="entryColor(log.apiType)" variant="tonal">
                  {{ entryLabel(log.apiType) }}
                </v-chip>
              </td>
              <td class="channel-cell">
                <div class="font-weight-medium">{{ log.channelName || `#${log.channelIndex}` }}</div>
                <div class="text-caption text-medium-emphasis">{{ log.baseUrl || '--' }}</div>
              </td>
              <td>
                <v-chip size="x-small" :color="statusColor(log.status)" variant="tonal">
                  {{ formatStatus(log) }}
                </v-chip>
                <div class="text-caption text-medium-emphasis">{{ formatStatusMeta(log) }}</div>
              </td>
              <td class="model-cell">
                <div>{{ log.transform || log.resolvedModel || log.model || '--' }}</div>
                <div v-if="log.keyMask" class="text-caption text-medium-emphasis">{{ log.keyMask }}</div>
              </td>
              <td class="text-no-wrap">
                <div>首 {{ formatSeconds(log.firstTokenMs) }}</div>
                <div class="text-caption text-medium-emphasis">总 {{ formatSeconds(log.durationMs) }}</div>
              </td>
              <td class="text-no-wrap">
                {{ formatPair(log.inputTokens, log.outputTokens) }}
                <div class="text-caption text-medium-emphasis">
                  {{ formatPairPercent(log.inputTokens, log.outputTokens, 'I', 'O') }}
                </div>
              </td>
              <td class="text-no-wrap">
                <div>{{ formatNumber(log.cacheReadTokens) }}</div>
                <div class="text-caption text-medium-emphasis">命中 {{ formatCacheHitRate(log) }}</div>
              </td>
              <td class="text-no-wrap">{{ formatTPM(log.tpm) }}</td>
              <td class="error-cell">
                <template v-if="log.errorType || log.errorMessage">
                  <div class="font-weight-medium">{{ formatErrorType(log.errorType) }}</div>
                  <div class="text-caption text-medium-emphasis">{{ log.errorMessage }}</div>
                </template>
                <span v-else class="text-medium-emphasis">--</span>
              </td>
              <td class="action-cell">
                <v-btn
                  size="small"
                  variant="text"
                  prepend-icon="mdi-text-box-search-outline"
                  :disabled="!log.requestId"
                  @click="openBodyDialog(log.requestId)"
                >
                  查看正文
                </v-btn>
                <v-btn
                  size="small"
                  variant="text"
                  prepend-icon="mdi-file-find-outline"
                  :disabled="!log.requestId"
                  @click="openSystemLog(log.requestId)"
                >
                  查看详情日志
                </v-btn>
              </td>
            </tr>
          </tbody>
        </v-table>
      </v-card>
    </div>

    <PromptBodyDialog v-model="bodyDialogVisible" :request-id="bodyRequestId" />

    <SystemLogsPanel
      v-if="activeTab === 'system'"
      :request-id="systemRequestId"
      :highlight-request-id="highlightRequestId"
      @select="openSystemLog"
      @back="backToRequestTab"
    />

    <v-dialog v-model="purgeDialog" max-width="480" persistent>
      <v-card>
        <v-card-title class="dialog-title">清理日志</v-card-title>
        <v-divider />
        <v-card-text>
          <p class="mb-3 text-body-2">
            清理 logs.db 全部日志（运行日志、请求正文、请求记录），不可恢复，清理后自动收缩数据库文件。
          </p>
          <v-btn-toggle v-model="purgeScope" mandatory density="compact" variant="outlined" divided class="mb-3">
            <v-btn value="day" size="small">按天清理</v-btn>
            <v-btn value="all" size="small">清理全部</v-btn>
          </v-btn-toggle>
          <v-select
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
          <v-alert v-if="purgeError" type="error" variant="tonal" density="compact" class="mt-3">
            {{ purgeError }}
          </v-alert>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" :disabled="purging" @click="purgeDialog = false">取消</v-btn>
          <v-btn
            color="error"
            variant="flat"
            prepend-icon="mdi-delete"
            :loading="purging"
            :disabled="purgeScope === 'day' && !purgeDate"
            @click="confirmPurge"
          >
            {{ purgeScope === 'all' ? '确认清理全部' : '确认清理该日' }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import PromptBodyDialog from '@/components/PromptBodyDialog.vue'
import SystemLogsPanel from '@/components/SystemLogsPanel.vue'
import { api, type LogAPIType, type LogAvailableDate, type RequestLogEntry } from '@/services/api'

type TypeFilter = LogAPIType | ''
type LogsTab = 'request' | 'system'
type DatePreset = 'today' | 'yesterday' | 'all' | 'custom'

const typeItems: Array<{ title: string; value: TypeFilter }> = [
  { title: '全部', value: '' },
  { title: 'Messages', value: 'messages' },
  { title: 'Responses', value: 'responses' },
  { title: 'Gemini', value: 'gemini' },
  { title: 'Chat', value: 'chat' },
  { title: 'Images', value: 'images' },
  { title: '命令 AI 复核', value: 'cmd_gate' },
  { title: '内部 AI 调用', value: 'llmgate' }
]

const datePresetItems: Array<{ title: string; value: DatePreset }> = [
  { title: '今天', value: 'today' },
  { title: '昨天', value: 'yesterday' },
  { title: '全部', value: 'all' },
  { title: '自定义', value: 'custom' }
]

const typeFilter = ref<TypeFilter>('')
const datePreset = ref<DatePreset>('all')
const fromTime = ref('')
const toTime = ref('')
const loading = ref(false)
const error = ref('')
const requestLogs = ref<RequestLogEntry[]>([])

const bodyDialogVisible = ref(false)
const bodyRequestId = ref('')
const highlightRequestId = ref('')

const purgeDialog = ref(false)
const purgeScope = ref<'day' | 'all'>('day')
const purgeDate = ref('')
const purgeDates = ref<LogAvailableDate[]>([])
const purgeDatesLoading = ref(false)
const purging = ref(false)
const purgeError = ref('')

const route = useRoute()
const router = useRouter()

const activeTab = computed({
  get: (): LogsTab => (route.query.tab === 'system' ? 'system' : 'request'),
  set: (tab: LogsTab) => {
    const query = { ...route.query }
    if (tab === 'system') {
      query.tab = 'system'
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

const openBodyDialog = (requestId: string) => {
  if (!requestId) return
  bodyRequestId.value = requestId
  bodyDialogVisible.value = true
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
  const from = fromTime.value ? new Date(fromTime.value) : null
  const to = toTime.value ? new Date(toTime.value) : null
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

const loadLogs = async () => {
  const range = resolveDateRange()
  if (!range) return
  loading.value = true
  error.value = ''
  try {
    const response = await api.getRequestLogs({
      type: typeFilter.value,
      limit: 50,
      from: range.from,
      to: range.to
    })
    requestLogs.value = response.logs || []
  } catch (err) {
    error.value = err instanceof Error ? err.message : '加载请求日志失败'
  } finally {
    loading.value = false
  }
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
  purging.value = true
  purgeError.value = ''
  try {
    const payload: { scope: 'all' | 'day'; date?: string } =
      purgeScope.value === 'all' ? { scope: 'all' } : { scope: 'day', date: purgeDate.value }
    const response = await api.purgeLogs(payload)
    const total =
      response.purged.deletedAppLogs + response.purged.deletedTrafficLogs + response.purged.deletedRequestLogs
    purgeDialog.value = false
    purgeDates.value = []
    purgeDate.value = ''
    await loadLogs()
    emitNotice(`清理完成，共删除 ${total.toLocaleString()} 条日志记录`)
  } catch (err) {
    purgeError.value = err instanceof Error ? err.message : '清理日志失败'
  } finally {
    purging.value = false
  }
}

const purgeNotice = ref('')
const emitNotice = (message: string) => {
  purgeNotice.value = message
}
watch(purgeNotice, value => {
  if (!value) return
  window.setTimeout(() => {
    if (purgeNotice.value === value) purgeNotice.value = ''
  }, 6000)
})

const typeLabelMap: Record<LogAPIType, string> = {
  messages: 'Messages',
  responses: 'Responses',
  gemini: 'Gemini',
  chat: 'Chat',
  images: 'Images',
  cmd_gate: '命令 AI 复核',
  llmgate: '内部 AI 调用'
}

const typeColorMap: Record<LogAPIType, string> = {
  messages: 'primary',
  responses: 'secondary',
  gemini: 'success',
  chat: 'info',
  images: 'warning',
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
  return date.toLocaleString()
}

const formatNumber = (value?: number) => {
  if (!value) return '0'
  return new Intl.NumberFormat().format(value)
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

watch(typeFilter, () => {
  loadLogs()
})

watch(datePreset, () => {
  if (datePreset.value === 'custom') return
  loadLogs()
})

watch([fromTime, toTime], () => {
  if (datePreset.value === 'custom') {
    loadLogs()
  }
})

onMounted(() => {
  loadLogs()
})
</script>

<style scoped>
.request-logs-view {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.logs-toolbar {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 16px;
  padding: 16px 0 8px;
}

.logs-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.logs-tabs {
  flex: 0 0 auto;
}

.logs-tabs :deep(.v-tab) {
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 0;
  min-width: 96px;
}

.date-preset-select {
  max-width: 160px;
}

.action-cell {
  white-space: nowrap;
}

.logs-table-card {
  border: 2px solid rgb(var(--v-theme-on-surface));
  border-radius: 0;
  box-shadow: 5px 5px 0 0 rgb(var(--v-theme-on-surface));
  overflow-x: auto;
}

.request-logs-table {
  min-width: 1180px;
}

.request-logs-table :deep(th) {
  font-size: 12px;
  font-weight: 700;
  color: rgb(var(--v-theme-on-surface));
  background: rgba(var(--v-theme-primary), 0.08);
  white-space: nowrap;
}

.request-logs-table :deep(td) {
  vertical-align: top;
  font-size: 13px;
}

.channel-cell,
.model-cell,
.error-cell {
  max-width: 260px;
  overflow-wrap: anywhere;
}

.dialog-title {
  font-weight: 700;
}

@media (max-width: 900px) {
  .logs-toolbar {
    align-items: flex-start;
    flex-direction: column;
  }

  .logs-actions {
    justify-content: flex-start;
  }
}
</style>
