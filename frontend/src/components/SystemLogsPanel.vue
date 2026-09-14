<template>
  <div class="system-logs-panel">
    <div class="system-toolbar">
      <div class="system-actions">
        <v-chip size="small" variant="tonal">{{ summaries.length }}/{{ listLimit }}</v-chip>
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
          :loading="listLoading"
          @click="reloadAll"
        >
          刷新
        </v-btn>
      </div>
    </div>

    <v-alert v-if="listError" type="error" variant="tonal" density="compact" closable @click:close="listError = ''">
      {{ listError }}
    </v-alert>

    <div class="system-layout">
      <v-card variant="flat" class="system-list-card">
        <v-alert v-if="locateWarning" type="warning" variant="tonal" density="compact" class="ma-2 mb-0">
          {{ locateWarning }}
        </v-alert>
        <div v-if="listLoading" class="system-empty">
          <v-progress-circular indeterminate size="24" width="2" color="primary" />
        </div>
        <div v-else-if="summaries.length === 0" class="system-empty">
          <span>暂无系统日志。确认 ENABLE_REQUEST_LOGS / ENABLE_RESPONSE_LOGS 未关闭，且代理已处理过大模型请求。</span>
        </div>
        <button
          v-for="entry in summaries"
          :id="`system-log-entry-${entry.requestId}`"
          :key="entry.requestId"
          type="button"
          class="system-list-item"
          :class="{ selected: entry.requestId === selectedRequestId, 'jump-target': Boolean(highlightRequestId) && entry.requestId === highlightRequestId }"
          @click="emit('select', entry.requestId)"
        >
          <div class="system-list-row">
            <span class="system-list-time">{{ formatTime(entry.timestamp) }}</span>
            <v-chip size="x-small" :color="entryColor(entry.apiType)" variant="tonal">
              {{ entryLabel(entry.apiType) }}
            </v-chip>
          </div>
          <div class="system-list-meta">
            <span v-if="entry.statusCode">{{ entry.statusCode }}</span>
            <span v-if="entry.stream">stream</span>
            <span v-if="entry.truncated" class="text-warning">截断</span>
            <span v-if="entry.missingPhases?.length" class="text-error">缺段</span>
          </div>
          <div v-if="entry.channelName || entry.model" class="system-list-channel">
            {{ entry.channelName || '--' }}
            <span v-if="entry.model"> · {{ entry.model }}</span>
          </div>
        </button>
      </v-card>

      <v-card variant="flat" class="system-detail-card">
        <div v-if="detailLoading" class="system-empty">
          <v-progress-circular indeterminate size="24" width="2" color="primary" />
        </div>
        <div v-else-if="detailError" class="system-empty text-error">{{ detailError }}</div>
        <div v-else-if="!detail" class="system-empty">从左侧选择一次请求，查看原请求、发给上游与上游返回。</div>
        <div v-else class="system-detail">
          <div v-if="highlightRequestId" class="detail-jump-bar">
            <span class="text-caption text-medium-emphasis">从请求日志跳转，当前展示该请求的完整流量详情</span>
            <v-btn
              size="small"
              variant="tonal"
              color="primary"
              prepend-icon="mdi-text-box-search-outline"
              @click="detailBodyDialog = true"
            >
              查看正文
            </v-btn>
            <v-btn
              size="small"
              variant="text"
              prepend-icon="mdi-arrow-left"
              @click="emit('back')"
            >
              返回请求日志
            </v-btn>
          </div>
          <div class="detail-header">
            <div class="detail-identity">
              <div class="detail-time">{{ formatTime(detail.summary.timestamp) }}</div>
              <div class="detail-id-row">
                <span class="font-mono detail-id" :title="detail.requestId">{{ detail.requestId }}</span>
                <v-btn
                  icon="mdi-content-copy"
                  size="x-small"
                  variant="text"
                  color="primary"
                  :aria-label="`复制 requestId ${detail.requestId}`"
                  title="复制 requestId"
                  @click="copyText(detail.requestId, 'requestId')"
                />
                <span v-if="copiedKey === 'requestId'" class="text-caption text-success">已复制</span>
              </div>
            </div>
            <div class="detail-chips">
              <v-btn
                v-if="parentRequestId"
                size="small"
                variant="tonal"
                @click="emit('select', parentRequestId)"
              >
                查看原请求
              </v-btn>
              <v-chip size="small" :color="entryColor(detail.summary.apiType)" variant="tonal">
                {{ entryLabel(detail.summary.apiType) }}
              </v-chip>
              <v-chip v-if="detail.summary.statusCode" size="small" variant="tonal">
                {{ detail.summary.statusCode }}
              </v-chip>
              <v-chip v-if="detail.summary.stream" size="small" variant="tonal">stream</v-chip>
              <v-btn
                size="small"
                variant="tonal"
                prepend-icon="mdi-refresh"
                :loading="detailLoading"
                @click="loadDetail(detail.requestId)"
              >
                刷新
              </v-btn>
            </div>
          </div>
          <div v-if="detail.summary.channelName || detail.summary.model" class="detail-route">
            {{ detail.summary.channelName || '渠道未知' }}
            <span v-if="detail.summary.model"> · {{ detail.summary.model }}</span>
            <span v-if="detail.summary.status"> · {{ detail.summary.status }}</span>
          </div>

          <div class="quick-copy">
            <v-btn
              color="primary"
              variant="flat"
              prepend-icon="mdi-content-copy"
              :disabled="!clientLog?.body"
              @click="copyBody(clientLog, 'quick-client')"
            >
              {{ copiedKey === 'quick-client' ? '已复制原请求' : '复制原请求' }}
            </v-btn>
            <v-btn
              color="primary"
              variant="flat"
              prepend-icon="mdi-content-copy"
              :disabled="!currentAttempt?.request?.body"
              @click="copyBody(currentAttempt?.request, 'quick-upstream')"
            >
              {{ copiedKey === 'quick-upstream' ? '已复制发给上游' : '复制发给上游' }}
            </v-btn>
            <v-btn
              color="primary"
              variant="flat"
              prepend-icon="mdi-content-copy"
              :disabled="!currentAttempt?.response?.body"
              @click="copyBody(currentAttempt?.response, 'quick-response')"
            >
              {{ copiedKey === 'quick-response' ? '已复制上游返回' : '复制上游返回' }}
            </v-btn>
          </div>

          <div v-if="attempts.length > 1" class="attempt-row">
            <span class="text-caption text-medium-emphasis">上游尝试</span>
            <v-chip-group v-model="selectedAttemptIndex" mandatory>
              <v-chip
                v-for="(attempt, index) in attempts"
                :key="attempt.key"
                :value="index"
                size="small"
                filter
                variant="tonal"
              >
                {{ attempt.label }}
              </v-chip>
            </v-chip-group>
          </div>

          <section v-for="section in visibleSections" :key="section.key" class="phase-section">
            <div class="phase-header">
              <div class="phase-title-block">
                <div class="phase-title">{{ section.title }}</div>
                <div v-if="section.log" class="phase-meta">
                  <span v-if="section.log.method">{{ section.log.method }}</span>
                  <span v-if="section.log.url" class="phase-url" :title="section.log.url">{{ section.log.url }}</span>
                  <span v-if="section.log.statusCode">{{ section.log.statusCode }}</span>
                  <span>{{ formatBytes(section.log) }}</span>
                  <span v-if="section.log.truncated" class="text-warning">truncated</span>
                </div>
                <div v-else class="phase-meta text-medium-emphasis">无记录</div>
              </div>
              <div v-if="section.log" class="phase-actions">
                <v-btn
                  color="primary"
                  variant="tonal"
                  size="small"
                  prepend-icon="mdi-content-copy"
                  @click="copyBody(section.log, section.key)"
                >
                  {{ copiedKey === section.key ? '已复制' : '复制' }}
                </v-btn>
                <v-switch
                  v-model="headerVisible[section.key]"
                  hide-details
                  density="compact"
                  color="primary"
                  label="显示请求头"
                />
                <v-switch
                  v-model="wrapEnabled[section.key]"
                  hide-details
                  density="compact"
                  color="primary"
                  label="自动换行"
                />
              </div>
            </div>
            <template v-if="section.log">
              <pre v-if="headerVisible[section.key]" class="headers-pre">{{ formatHeaders(section.log.headers) }}</pre>
              <SessionInspector
                :body="section.log.body || ''"
                :wrap="wrapEnabled[section.key]"
                :url="section.log.url || ''"
                :method="section.log.method || 'POST'"
              />
            </template>
          </section>

          <section v-if="synthLogs.length" class="phase-section">
            <div class="phase-header">
              <button type="button" class="synth-toggle" @click="synthExpanded = !synthExpanded">
                <v-icon size="18">{{ synthExpanded ? 'mdi-chevron-down' : 'mdi-chevron-right' }}</v-icon>
                <span class="phase-title">流式合成（观测）</span>
              </button>
              <v-btn
                v-if="synthExpanded && synthLogs[0]"
                size="small"
                color="primary"
                variant="tonal"
                prepend-icon="mdi-content-copy"
                @click="copyText(synthLogs.map(item => formatBodyForCopy(item.body || '')).join('\n\n'), 'synth')"
              >
                {{ copiedKey === 'synth' ? '已复制' : '复制' }}
              </v-btn>
            </div>
            <div v-if="synthExpanded">
              <JsonBodyPane
                v-for="(synthLog, index) in synthLogs"
                :key="synthLog.id || index"
                :body="synthLog.body || ''"
                wrap
              />
            </div>
          </section>
        </div>
      </v-card>
    </div>

    <PromptBodyDialog v-model="detailBodyDialog" :request-id="selectedRequestId" />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import JsonBodyPane from '@/components/JsonBodyPane.vue'
import PromptBodyDialog from '@/components/PromptBodyDialog.vue'
import SessionInspector from '@/components/SessionInspector.vue'
import { api, type LogAPIType, type SystemLogDetail, type SystemLogSummary, type TrafficLog } from '@/services/api'

type TypeFilter = LogAPIType | ''

interface UpstreamAttempt {
  key: string
  label: string
  request?: TrafficLog
  response?: TrafficLog
}

interface PhaseSection {
  key: string
  title: string
  log?: TrafficLog
}

const props = defineProps<{
  requestId?: string
  highlightRequestId?: string
}>()

const emit = defineEmits<{
  select: [requestId: string]
  back: []
}>()

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

const typeFilter = ref<TypeFilter>('')
const listLoading = ref(false)
const listError = ref('')
const summaries = ref<SystemLogSummary[]>([])
const listLimit = ref(100)
const detailLoading = ref(false)
const detailError = ref('')
const detail = ref<SystemLogDetail | null>(null)
const selectedAttemptIndex = ref(0)
const synthExpanded = ref(false)
const detailBodyDialog = ref(false)
const copiedKey = ref('')
const headerVisible = reactive<Record<string, boolean>>({})
const wrapEnabled = reactive<Record<string, boolean>>({})

const selectedRequestId = computed(() => props.requestId || '')

const trafficLogs = computed(() => detail.value?.trafficLogs || [])
const parentRequestId = computed(() => detail.value?.requestLogs?.find(entry => entry.parentRequestId)?.parentRequestId || '')

const attempts = computed((): UpstreamAttempt[] => {
  const requests = trafficLogs.value.filter(entry => entry.phase === 'upstream_request')
  const responses = trafficLogs.value.filter(entry => entry.phase === 'upstream_response')
  const hasAttemptIds = [...requests, ...responses].some(entry => Boolean(entry.attemptId))
  if (hasAttemptIds) {
    const grouped = new Map<string, UpstreamAttempt>()
    const remember = (entry: TrafficLog, field: 'request' | 'response') => {
      const key = entry.attemptId || `row-${entry.id || entry.timestamp}`
      const current = grouped.get(key) || { key, label: key.slice(0, 8) }
      current[field] = entry
      grouped.set(key, current)
    }
    requests.forEach(entry => remember(entry, 'request'))
    responses.forEach(entry => remember(entry, 'response'))
    return Array.from(grouped.values()).map((attempt, index) => ({
      ...attempt,
      label: `尝试 ${index + 1}`
    }))
  }
  const count = Math.max(requests.length, responses.length)
  return Array.from({ length: count }, (_, index) => ({
    key: `attempt-${index + 1}`,
    label: `尝试 ${index + 1}`,
    request: requests[index],
    response: responses[index]
  }))
})

const currentAttempt = computed(() => {
  if (attempts.value.length === 0) return undefined
  const index = Math.min(selectedAttemptIndex.value, attempts.value.length - 1)
  return attempts.value[index]
})

const synthLogs = computed(() => trafficLogs.value.filter(entry => entry.phase === 'stream_synth'))

const clientLog = computed(() => trafficLogs.value.find(entry => entry.phase === 'client_request'))

const visibleSections = computed((): PhaseSection[] => [
  {
    key: 'client',
    title: detail.value?.summary.apiType === 'cmd_gate' ? '提交给 AI 的复核内容' : '打进代理的原请求',
    log: trafficLogs.value.find(entry => entry.phase === 'client_request')
  },
  {
    key: 'upstream',
    title: '代理发给上游的请求',
    log: currentAttempt.value?.request
  },
  {
    key: 'response',
    title: '代理收到的上游响应',
    log: currentAttempt.value?.response
  }
])

const entryLabel = (value?: string) => typeLabelMap[(value || '').toLowerCase() as LogAPIType] || value || '未知'
const entryColor = (value?: string) => typeColorMap[(value || '').toLowerCase() as LogAPIType] || 'secondary'

const formatTime = (value?: string) => {
  if (!value) return '--'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

const formatBytes = (log: TrafficLog) => {
  const bytes = log.originalBytes || log.body?.length || 0
  if (!bytes) return '0 B'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

const formatHeaders = (raw?: string) => {
  if (!raw) return '无请求头'
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

const copyText = async (text: string, key: string) => {
  try {
    await navigator.clipboard.writeText(text)
    copiedKey.value = key
    window.setTimeout(() => {
      if (copiedKey.value === key) copiedKey.value = ''
    }, 1500)
  } catch {
    copiedKey.value = ''
  }
}

const formatBodyForCopy = (raw: string) => {
  const trimmed = raw.trim()
  if (!trimmed) return ''
  try {
    return JSON.stringify(JSON.parse(trimmed), null, 2)
  } catch {
    return raw
  }
}

const copyBody = async (log: TrafficLog | undefined, key: string) => {
  if (!log?.body) return
  await copyText(formatBodyForCopy(log.body), key)
}

const loadList = async () => {
  listLoading.value = true
  listError.value = ''
  try {
    const response = await api.getSystemLogs({
      type: typeFilter.value,
      limit: 100
    })
    summaries.value = response.logs || []
    listLimit.value = response.limit || 100
  } catch (err) {
    listError.value = err instanceof Error ? err.message : '加载系统日志失败'
  } finally {
    listLoading.value = false
  }
}

const loadDetail = async (requestId: string) => {
  if (!requestId) {
    detail.value = null
    return
  }
  detailLoading.value = true
  detailError.value = ''
  try {
    detail.value = await api.getSystemLog(requestId)
    selectedAttemptIndex.value = Math.max((attempts.value.length || 1) - 1, 0)
    synthExpanded.value = false
  } catch (err) {
    detail.value = null
    detailError.value = err instanceof Error ? err.message : '加载系统日志详情失败'
  } finally {
    detailLoading.value = false
  }
}

const reloadAll = async () => {
  await loadList()
  if (selectedRequestId.value) {
    await loadDetail(selectedRequestId.value)
  }
}

// scrollToEntry 把列表滚动到指定 requestId 条目；条目不在当前列表时给出提示。
const scrollToEntry = async (requestId: string) => {
  if (!requestId) return
  await nextTick()
  const element = document.getElementById(`system-log-entry-${requestId}`)
  if (!element) {
    locateWarning.value = '该请求不在当前列表（可能已被更新日志挤出或筛选不匹配），下方仍展示其详情。'
    return
  }
  locateWarning.value = ''
  element.scrollIntoView({ block: 'center', behavior: 'smooth' })
}

const locateWarning = ref('')

watch(summaries, () => {
  if (props.highlightRequestId) {
    scrollToEntry(props.highlightRequestId)
  }
})

watch(
  () => props.highlightRequestId,
  requestId => {
    if (requestId) {
      scrollToEntry(requestId)
    }
  }
)

watch(typeFilter, () => {
  loadList()
})

watch(
  () => props.requestId,
  requestId => {
    loadDetail(requestId || '')
  }
)

watch(attempts, value => {
  if (selectedAttemptIndex.value > value.length - 1) {
    selectedAttemptIndex.value = Math.max(value.length - 1, 0)
  }
})

onMounted(async () => {
  await loadList()
  if (selectedRequestId.value) {
    await loadDetail(selectedRequestId.value)
  }
  if (props.highlightRequestId) {
    scrollToEntry(props.highlightRequestId)
  }
})
</script>

<style scoped>
.system-logs-panel {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.system-toolbar {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 16px;
}

.system-actions,
.detail-chips,
.detail-id-row,
.phase-header,
.phase-actions,
.attempt-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.system-actions {
  justify-content: flex-end;
}

.system-layout {
  display: grid;
  grid-template-columns: 360px minmax(0, 1fr);
  gap: 16px;
  min-height: 70vh;
}

.system-list-card,
.system-detail-card {
  border: 2px solid rgb(var(--v-theme-on-surface));
  border-radius: 0;
  box-shadow: 5px 5px 0 0 rgb(var(--v-theme-on-surface));
  overflow: auto;
}

.system-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 220px;
  padding: 24px;
  text-align: center;
  color: rgba(var(--v-theme-on-surface), 0.62);
}

.system-list-item {
  display: block;
  width: 100%;
  padding: 12px 14px;
  border: 0;
  border-bottom: 1px solid rgba(var(--v-theme-on-surface), 0.12);
  background: transparent;
  text-align: left;
  cursor: pointer;
}

.system-list-item.selected,
.system-list-item:hover {
  background: rgba(var(--v-theme-primary), 0.08);
}

.system-list-item.jump-target {
  border: 2px solid rgb(var(--v-theme-warning));
  animation: jump-target-pulse 0.9s ease-in-out 3;
}

@keyframes jump-target-pulse {
  0%,
  100% {
    background: transparent;
  }
  50% {
    background: rgba(var(--v-theme-warning), 0.28);
  }
}

.detail-jump-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  padding: 8px 12px;
  border: 1px dashed rgba(var(--v-theme-warning), 0.6);
  background: rgba(var(--v-theme-warning), 0.08);
}

.system-list-row,
.system-list-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.system-list-time,
.system-list-meta,
.system-list-channel,
.phase-meta,
.detail-time,
.detail-route {
  font-size: 12px;
  color: rgba(var(--v-theme-on-surface), 0.72);
}

.system-list-channel,
.detail-route {
  margin-top: 4px;
}

.system-detail {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 16px;
}

.quick-copy {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.detail-header {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.detail-id {
  font-size: 13px;
  overflow-wrap: anywhere;
}

.phase-section {
  border: 1px solid rgba(var(--v-theme-on-surface), 0.18);
}

.phase-header {
  justify-content: space-between;
  padding: 10px 12px;
  background: rgba(var(--v-theme-primary), 0.08);
}

.phase-title {
  font-weight: 700;
  font-size: 14px;
}

.phase-url {
  max-width: 420px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.headers-pre {
  margin: 0;
  padding: 10px 12px;
  border-bottom: 1px solid rgba(var(--v-theme-on-surface), 0.12);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  white-space: pre-wrap;
  overflow: auto;
  max-height: 180px;
}

.synth-toggle {
  display: flex;
  align-items: center;
  gap: 6px;
  border: 0;
  background: transparent;
  cursor: pointer;
  padding: 0;
  color: inherit;
}

.font-mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

@media (max-width: 1100px) {
  .system-layout {
    grid-template-columns: 1fr;
  }

  .system-toolbar {
    align-items: flex-start;
    flex-direction: column;
  }
}
</style>
