<template>
  <div class="request-log-detail">
    <v-alert v-if="error" type="error" variant="tonal">{{ error }}</v-alert>
    <div v-if="loading" class="pa-4 text-center">正在加载请求详情…</div>
    <template v-else-if="detail">
      <div class="detail-meta">
        <code>{{ requestId }}</code>
        <v-chip size="small" :color="logStatus(detail.summary).color" variant="tonal">{{ logStatus(detail.summary).label }}</v-chip>
        <span v-if="detail.summary.durationMs != null" class="text-caption">网关总耗时 {{ detail.summary.durationMs.toLocaleString() }} ms</span>
        <span v-if="detail.summary.clientStatusCode" class="text-caption">网关写出 HTTP {{ detail.summary.clientStatusCode }}</span>
        <v-btn size="small" variant="text" @click="copyId">复制 ID</v-btn>
        <v-btn size="small" variant="text" :loading="loading" @click="load(true)">刷新</v-btn>
        <v-btn v-if="parentRequestId" size="small" variant="text" @click="emit('locate', parentRequestId)">查看原请求</v-btn>
      </div>
      <div v-if="detail.summary.errorMessage || currentCallAttempt?.errorMessage" class="attempt-error mb-4">
        <div class="attempt-error__heading">
          <strong>{{ currentCallAttempt?.errorType || detail.summary.errorType || '调用未正常完成' }}</strong>
          <v-btn size="x-small" variant="text" prepend-icon="mdi-content-copy" @click="copyError">复制完整原因</v-btn>
        </div>
        <pre>{{ currentCallAttempt?.errorMessage || detail.summary.errorMessage }}</pre>
      </div>
      <ToolTraceLinks v-if="detail.toolLinks" :links="detail.toolLinks" @locate="emit('locate', $event)" />
      <section v-if="callAttempts.length" class="attempt-section">
        <div class="attempt-section__heading">
          <strong>上游尝试时间线</strong>
          <span>{{ callAttempts.length }} 次尝试 · 网关总耗时与单次上游耗时分别展示</span>
        </div>
        <ol class="attempt-timeline">
          <li v-for="(attempt, index) in callAttempts" :key="attempt.attemptId || index" :class="{ active: attempt.attemptId === selectedCallAttemptId, failed: !attempt.success }">
            <button type="button" @click="selectCallAttempt(attempt)">
              <span class="attempt-timeline__rail" aria-hidden="true"></span>
              <span class="attempt-timeline__content">
                <span class="attempt-timeline__heading">
                  <strong>第 {{ index + 1 }} 次</strong>
                  <v-chip size="x-small" :color="attempt.success ? 'success' : 'error'" variant="tonal">{{ attempt.success ? '成功' : (attempt.status || '失败') }}</v-chip>
                  <time>{{ formatAttemptTime(attempt.timestamp) }}</time>
                </span>
                <span class="attempt-timeline__meta">{{ attempt.channelName || '无渠道' }} · {{ attempt.resolvedModel || attempt.model || '--' }} · 上游 {{ formatDuration(attempt.durationMs) }} · 首字 {{ formatDuration(attempt.firstTokenMs) }}</span>
                <span class="attempt-timeline__tokens">输入 {{ attempt.inputTokens || 0 }} · 输出 {{ attempt.outputTokens || 0 }} · 缓存读取 {{ attempt.cacheReadTokens || 0 }}</span>
              </span>
            </button>
          </li>
        </ol>
        <div v-if="currentCallAttempt" class="selected-attempt-meta">
          <span>渠道 HTTP {{ currentCallAttempt.statusCode || '--' }}</span>
          <span>响应头 {{ formatDuration(currentCallAttempt.responseHeadersMs) }}</span>
          <span>{{ currentCallAttempt.stream ? '流式' : '非流式' }}</span>
          <span>{{ currentCallAttempt.retried ? '重试尝试' : '首次尝试' }}</span>
          <code>{{ currentCallAttempt.attemptId }}</code>
        </div>
      </section>
      <div v-if="phase === 'client' && detail.contextChange" class="context-change">
        <span v-if="detail.contextChange.comparable">与前序调用相比：新增 {{ detail.contextChange.added }} 块 · 移除 {{ detail.contextChange.removed }} 块 · 估算净变化 {{ detail.contextChange.estimatedDelta > 0 ? '+' : '' }}{{ detail.contextChange.estimatedDelta.toLocaleString() }} Token</span>
        <p>{{ detail.contextChange.notice }}</p>
        <p v-if="detail.contextChange.orderChanged">内容块相同，但排列顺序发生变化。</p>
        <v-btn size="small" variant="text" @click="emit('locate', detail.contextChange.previousRequestId)">查看前序调用</v-btn>
      </div>
      <v-select v-if="attempts.length > 1" v-model="selectedAttempt" :items="attemptItems" label="上游尝试" density="compact" hide-details class="mb-4" />
      <v-alert v-if="currentAttempt?.admission" type="info" variant="tonal" density="compact" class="mb-4">{{ currentAttempt.admission.body }}</v-alert>
      <v-alert v-if="currentAttempt?.error" type="warning" variant="tonal" density="compact" class="mb-4">{{ currentAttempt.error.body }}</v-alert>
      <v-tabs v-model="phase" density="compact">
        <v-tab value="client">入口输入</v-tab>
        <v-tab value="upstream">发送上游</v-tab>
        <v-tab value="response">上游响应</v-tab>
      </v-tabs>
      <div v-if="currentLog" class="phase-content">
        <p v-if="phase === 'client'" class="text-caption text-medium-emphasis mb-3">入口输入在内容安全处理后采集，媒体已省略；摘要不能用于重放请求。</p>
        <ContextSnapshotPanel v-if="currentContext && bodyAvailable" :snapshot="currentContext" :body="currentLog.body || ''" />
        <div class="text-caption text-medium-emphasis phase-meta">{{ currentLog.method }} {{ currentLog.url }} <span v-if="currentLog.statusCode"> · HTTP {{ currentLog.statusCode }}</span></div>
        <v-alert v-if="bodyNotice" type="info" variant="tonal" density="compact" class="mb-3">{{ bodyNotice }}</v-alert>
        <SessionInspector v-if="bodyAvailable" :body="currentLog.body || ''" :method="currentLog.method" :url="currentLog.url" :replayable="phase !== 'response' && currentLog.phase !== 'client_input' && !currentLog.truncated" wrap />
        <details v-if="currentLog.headers" class="mt-4" :open="headersOpen" @toggle="headersOpen = ($event.target as HTMLElement).hasAttribute('open')"><summary>请求头与响应头</summary><JsonBodyPane v-if="headersOpen" :body="currentLog.headers" title="请求头与响应头" :meta="requestId" wrap /></details>
      </div>
      <div v-else class="pa-4 text-medium-emphasis">此阶段没有正文记录。</div>
      <details v-if="synth?.body" class="mt-4" :open="synthOpen" @toggle="synthOpen = ($event.target as HTMLElement).hasAttribute('open')"><summary>流式响应的结构化合成</summary><SessionInspector v-if="synthOpen" :body="synth.body" :replayable="false" wrap /></details>
    </template>
  </div>
</template>

<script setup lang="ts">
// 完整详情按需加载，共用日志 store 缓存；一次只挂载一个阶段的正文。
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useLogSession } from '@/features/observability/composables/useLogSession'
import { getLogAttempts, logStatus } from '@/features/observability/utils/logConversation'
import type { RequestLogEntry, SystemLogDetail } from '@/types/logs'
import SessionInspector from '@/features/observability/components/conversation/SessionInspector.vue'
import JsonBodyPane from '@/features/observability/components/common/JsonBodyPane.vue'
import ContextSnapshotPanel from '@/features/observability/components/context/ContextSnapshotPanel.vue'
import ToolTraceLinks from '@/features/observability/components/conversation/ToolTraceLinks.vue'
import { useClipboard } from '@/shared/composables/useClipboard'

const props = defineProps<{ requestId: string; sourceVersion?: string }>()
const emit = defineEmits<{ locate: [requestId: string] }>()
const logs = useLogSession()
const { copyText } = useClipboard()
const detail = shallowRef<SystemLogDetail | null>(null)
const loading = ref(false)
const error = ref('')
const phase = ref<'client' | 'upstream' | 'response'>('client')
const selectedAttempt = ref(0)
const selectedCallAttemptId = ref('')
const headersOpen = ref(false)
const synthOpen = ref(false)
let revision = 0
const attempts = computed(() => getLogAttempts(detail.value?.trafficLogs || []))
const attemptItems = computed(() => attempts.value.map((attempt, index) => ({ title: attempt.label, value: index })))
const currentAttempt = computed(() => attempts.value[selectedAttempt.value])
const callAttempts = computed(() => detail.value?.requestLogs || [])
const currentCallAttempt = computed(() => callAttempts.value.find(attempt => attempt.attemptId === selectedCallAttemptId.value) || callAttempts.value[callAttempts.value.length - 1])
const currentLog = computed(() => phase.value === 'client' ? detail.value?.trafficLogs.find(log => log.phase === 'client_input') || detail.value?.trafficLogs.find(log => log.phase === 'client_request') : phase.value === 'upstream' ? currentAttempt.value?.request : currentAttempt.value?.response)
const bodyAvailable = computed(() => !['disabled', 'metadata', 'budget', 'expired'].includes(currentLog.value?.bodyState || ''))
const bodyNotice = computed(() => {
  const state = currentLog.value?.bodyState
  if (state === 'disabled') return '该请求开始时正文采集开关已关闭。'
  if (state === 'metadata') return '该请求使用仅元数据采集档位，未保存正文与头部。'
  if (state === 'budget') return '该请求开始时日志预算已触发，正文采集降为元数据。'
  if (state === 'expired') return '正文与头部已按保留策略清理；调用摘要和工具 ID 索引仍保留。'
  if (state === 'summary') return '原始正文已省略，当前仅展示受大小限制的入口摘要，无法重放。'
  return currentLog.value?.truncated ? '正文超过采集上限或不完整；这里展示实际保存的内容。' : ''
})
const currentContext = computed(() => phase.value === 'response' ? undefined : detail.value?.contexts?.find(snapshot => snapshot.trafficId === currentLog.value?.id))
const synth = computed(() => [...(detail.value?.trafficLogs || [])].reverse().find(log => log.phase === 'stream_synth' && (!currentAttempt.value?.request?.attemptId || log.attemptId === currentAttempt.value.request.attemptId)))
const parentRequestId = computed(() => detail.value?.requestLogs?.find(log => log.parentRequestId)?.parentRequestId || '')

async function load(force = false) {
  const version = ++revision
  loading.value = true
  error.value = ''
  try {
    const result = await logs.loadDetail(props.requestId, undefined, force)
    if (version !== revision) return
    detail.value = result
    selectedAttempt.value = Math.max(0, getLogAttempts(result.trafficLogs).length - 1)
    selectedCallAttemptId.value = result.requestLogs?.[result.requestLogs.length - 1]?.attemptId || ''
  } catch (failure) { if (version === revision) error.value = failure instanceof Error ? failure.message : '加载请求详情失败' }
  finally { if (version === revision) loading.value = false }
}
async function copyId() {
  if (!await copyText(props.requestId, '请求 ID 已复制')) error.value = '复制请求 ID 失败'
}
async function copyError() {
  const message = currentCallAttempt.value?.errorMessage || detail.value?.summary.errorMessage
  if (message && !await copyText(message, '完整失败原因已复制')) error.value = '复制失败原因失败'
}
function selectCallAttempt(attempt: RequestLogEntry) {
  selectedCallAttemptId.value = attempt.attemptId
  const index = attempts.value.findIndex(item => item.key === attempt.attemptId || item.request?.attemptId === attempt.attemptId || item.response?.attemptId === attempt.attemptId)
  if (index >= 0) {
    selectedAttempt.value = index
    phase.value = 'upstream'
  }
}
function formatAttemptTime(value: string): string {
  const date = new Date(value)
  return `${date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })}.${String(date.getMilliseconds()).padStart(3, '0')}`
}
function formatDuration(value?: number): string {
  if (!value || value <= 0) return '--'
  return value < 1000 ? `${Math.round(value)} ms` : `${(value / 1000).toFixed(2)} s`
}
watch(selectedAttempt, index => {
  const id = attempts.value[index]?.request?.attemptId || attempts.value[index]?.response?.attemptId
  if (id) selectedCallAttemptId.value = id
})
watch(() => [props.requestId, props.sourceVersion], () => {
  detail.value = null
  phase.value = 'client'
  // 聚合列表已观测到指标版本时，正文详情也要刷新，避免继续显示旧的尝试结果。
  void load(Boolean(props.sourceVersion))
}, { immediate: true })
watch(() => [logs.storage?.maintenance.expiredBodies, logs.storage?.maintenance.deletedRows], (next, previous) => {
  if (detail.value && previous[0] !== undefined && (next[0] !== previous[0] || next[1] !== previous[1])) {
    detail.value = null
    void load(true)
  }
})
onBeforeUnmount(() => { revision++ })
</script>

<style scoped>
.detail-meta { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-bottom: 16px; }
.detail-meta code { overflow-wrap: anywhere; }
.attempt-error { padding: 12px 14px; color: rgb(var(--v-theme-on-error-container)); border: 1px solid rgba(var(--v-theme-error), .28); border-radius: 8px; background: rgba(var(--v-theme-error), .08); }
.attempt-error__heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.attempt-error pre { max-height: 220px; margin: 8px 0 0; overflow: auto; font: 12px/1.6 inherit; white-space: pre-wrap; overflow-wrap: anywhere; }
.attempt-section { margin: 18px 0; padding: 14px; border: 1px solid rgba(var(--v-theme-outline), .18); border-radius: 10px; background: rgba(var(--v-theme-surface-bright), .3); }
.attempt-section__heading { display: flex; justify-content: space-between; gap: 12px; flex-wrap: wrap; margin-bottom: 10px; font-size: 12px; }
.attempt-section__heading span { color: rgb(var(--v-theme-on-surface-variant)); }
.attempt-timeline { margin: 0; padding: 0; list-style: none; }
.attempt-timeline li { position: relative; }
.attempt-timeline button { display: flex; width: 100%; padding: 0; color: inherit; text-align: left; background: transparent; border: 0; cursor: pointer; }
.attempt-timeline__rail { position: relative; width: 24px; flex: 0 0 24px; }
.attempt-timeline__rail::before { position: absolute; z-index: 1; top: 18px; left: 7px; width: 9px; height: 9px; background: rgb(var(--v-theme-success)); border: 2px solid rgb(var(--v-theme-surface)); border-radius: 50%; content: ''; box-shadow: 0 0 0 1px rgba(var(--v-theme-success), .4); }
.attempt-timeline__rail::after { position: absolute; top: 28px; bottom: -8px; left: 11px; width: 1px; background: rgba(var(--v-theme-outline), .35); content: ''; }
.attempt-timeline li:last-child .attempt-timeline__rail::after { display: none; }
.attempt-timeline li.failed .attempt-timeline__rail::before { background: rgb(var(--v-theme-error)); box-shadow: 0 0 0 1px rgba(var(--v-theme-error), .4); }
.attempt-timeline__content { display: flex; min-width: 0; flex: 1; flex-direction: column; gap: 4px; padding: 10px 12px; border: 1px solid transparent; border-radius: 8px; }
.attempt-timeline li.active .attempt-timeline__content { border-color: rgba(var(--v-theme-primary), .35); background: rgba(var(--v-theme-primary), .07); }
.attempt-timeline button:hover .attempt-timeline__content { background: rgba(var(--v-theme-primary), .05); }
.attempt-timeline__heading { display: flex; align-items: center; gap: 8px; }
.attempt-timeline__heading time { margin-left: auto; color: rgb(var(--v-theme-on-surface-variant)); font-size: 11px; font-variant-numeric: tabular-nums; }
.attempt-timeline__meta, .attempt-timeline__tokens { color: rgb(var(--v-theme-on-surface-variant)); font-size: 12px; overflow-wrap: anywhere; }
.selected-attempt-meta { display: flex; gap: 8px 16px; flex-wrap: wrap; margin: 8px 0 0 24px; padding: 10px 12px; color: rgb(var(--v-theme-on-surface-variant)); font-size: 11px; border-top: 1px solid rgba(var(--v-theme-outline), .16); }
.selected-attempt-meta code { overflow-wrap: anywhere; }
.phase-content { padding-top: 16px; }
.phase-meta { overflow-wrap: anywhere; margin-bottom: 12px; }
.context-change { margin-bottom: 16px; font-size: 13px; }
.context-change p { color: rgba(var(--v-theme-on-surface), 0.65); margin-top: 6px; }
summary { cursor: pointer; padding: 8px 0; }
</style>
