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
      </div>

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
            <th>首 token</th>
            <th>Token I/O</th>
            <th>缓存 C/R</th>
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
            <td class="text-no-wrap">{{ formatMs(log.firstTokenMs) }}</td>
            <td class="text-no-wrap">
              {{ formatPair(log.inputTokens, log.outputTokens) }}
              <div class="text-caption text-medium-emphasis">
                {{ formatPairPercent(log.inputTokens, log.outputTokens, 'I', 'O') }}
              </div>
            </td>
            <td class="text-no-wrap">
              {{ formatPair(log.cacheCreationTokens, log.cacheReadTokens) }}
              <div class="text-caption text-medium-emphasis">
                {{ formatPairPercent(log.cacheCreationTokens, log.cacheReadTokens, 'C', 'R') }}
              </div>
              <div v-if="hasCacheTTL(log)" class="text-caption text-medium-emphasis">
                5m {{ formatNumber(log.cacheCreation5mTokens) }} / 1h {{ formatNumber(log.cacheCreation1hTokens) }}
              </div>
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
                prepend-icon="mdi-code-braces"
                :disabled="!log.requestId"
                @click="openSystemLog(log.requestId)"
              >
                查看正文
              </v-btn>
            </td>
          </tr>
        </tbody>
      </v-table>
    </v-card>
    </div>

    <SystemLogsPanel
      v-if="activeTab === 'system'"
      :request-id="systemRequestId"
      @select="openSystemLog"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import SystemLogsPanel from '@/components/SystemLogsPanel.vue'
import { api, type LogAPIType, type RequestLogEntry } from '@/services/api'

type TypeFilter = LogAPIType | ''
type LogsTab = 'request' | 'system'

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

const typeFilter = ref<TypeFilter>('')
const loading = ref(false)
const error = ref('')
const requestLogs = ref<RequestLogEntry[]>([])

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
  router.replace({
    query: {
      ...route.query,
      tab: 'system',
      id: requestId
    }
  })
}

const loadLogs = async () => {
  loading.value = true
  error.value = ''
  try {
    const response = await api.getRequestLogs({
      type: typeFilter.value,
      limit: 50
    })
    requestLogs.value = response.logs || []
  } catch (err) {
    error.value = err instanceof Error ? err.message : '加载请求日志失败'
  } finally {
    loading.value = false
  }
}

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
const formatMs = (value?: number) => value && value > 0 ? `${value}ms` : '--'
const formatTPM = (value?: number) => value && value > 0 ? value.toFixed(1) : '--'
const hasCacheTTL = (log: RequestLogEntry) => Boolean((log.cacheCreation5mTokens || 0) + (log.cacheCreation1hTokens || 0) || log.cacheTTL)

watch(typeFilter, () => {
  loadLogs()
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
