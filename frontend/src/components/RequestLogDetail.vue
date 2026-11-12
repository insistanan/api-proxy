<template>
  <div class="request-log-detail">
    <v-alert v-if="error" type="error" variant="tonal">{{ error }}</v-alert>
    <div v-if="loading" class="pa-4 text-center">正在加载请求详情…</div>
    <template v-else-if="detail">
      <div class="detail-meta">
        <code>{{ requestId }}</code>
        <v-chip size="small" :color="logStatus(detail.summary).color" variant="tonal">{{ logStatus(detail.summary).label }}</v-chip>
        <v-btn size="small" variant="text" @click="copyId">复制 ID</v-btn>
        <v-btn size="small" variant="text" :loading="loading" @click="load(true)">刷新</v-btn>
        <v-btn v-if="parentRequestId" size="small" variant="text" @click="emit('locate', parentRequestId)">查看原请求</v-btn>
      </div>
      <v-alert v-if="detail.summary.errorMessage" type="warning" variant="tonal" density="compact" class="mb-4">{{ detail.summary.errorMessage }}</v-alert>
      <ToolTraceLinks v-if="detail.toolLinks" :links="detail.toolLinks" @locate="emit('locate', $event)" />
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
        <ContextSnapshotPanel v-if="currentContext" :snapshot="currentContext" :body="currentLog.body || ''" />
        <div class="text-caption text-medium-emphasis phase-meta">{{ currentLog.method }} {{ currentLog.url }} <span v-if="currentLog.statusCode"> · HTTP {{ currentLog.statusCode }}</span></div>
        <v-alert v-if="currentLog.truncated" type="info" variant="tonal" density="compact" class="mb-3">原始正文超过采集上限或不完整；这里展示实际保存的内容。</v-alert>
        <SessionInspector :body="currentLog.body || ''" :method="currentLog.method" :url="currentLog.url" :replayable="phase !== 'response' && currentLog.phase !== 'client_input' && !currentLog.truncated" wrap />
        <details class="mt-4" :open="headersOpen" @toggle="headersOpen = ($event.target as HTMLElement).hasAttribute('open')"><summary>请求头与响应头</summary><JsonBodyPane v-if="headersOpen" :body="currentLog.headers || '{}'" wrap /></details>
      </div>
      <div v-else class="pa-4 text-medium-emphasis">此阶段没有正文记录。</div>
      <details v-if="synth" class="mt-4" :open="synthOpen" @toggle="synthOpen = ($event.target as HTMLElement).hasAttribute('open')"><summary>流式响应的结构化合成</summary><SessionInspector v-if="synthOpen" :body="synth.body || ''" :replayable="false" wrap /></details>
    </template>
  </div>
</template>

<script setup lang="ts">
// 完整详情按需加载，共用日志 store 缓存；一次只挂载一个阶段的正文。
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useLogsStore } from '@/stores/logs'
import { getLogAttempts, logStatus } from '@/utils/logConversation'
import type { SystemLogDetail } from '@/types/logs'
import SessionInspector from './SessionInspector.vue'
import JsonBodyPane from './JsonBodyPane.vue'
import ContextSnapshotPanel from './ContextSnapshotPanel.vue'
import ToolTraceLinks from './ToolTraceLinks.vue'

const props = defineProps<{ requestId: string; sourceVersion?: string }>()
const emit = defineEmits<{ locate: [requestId: string] }>()
const logs = useLogsStore()
const detail = shallowRef<SystemLogDetail | null>(null)
const loading = ref(false)
const error = ref('')
const phase = ref<'client' | 'upstream' | 'response'>('client')
const selectedAttempt = ref(0)
const headersOpen = ref(false)
const synthOpen = ref(false)
let revision = 0
const attempts = computed(() => getLogAttempts(detail.value?.trafficLogs || []))
const attemptItems = computed(() => attempts.value.map((attempt, index) => ({ title: attempt.label, value: index })))
const currentAttempt = computed(() => attempts.value[selectedAttempt.value])
const currentLog = computed(() => phase.value === 'client' ? detail.value?.trafficLogs.find(log => log.phase === 'client_input') || detail.value?.trafficLogs.find(log => log.phase === 'client_request') : phase.value === 'upstream' ? currentAttempt.value?.request : currentAttempt.value?.response)
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
  } catch (failure) { if (version === revision) error.value = failure instanceof Error ? failure.message : '加载请求详情失败' }
  finally { if (version === revision) loading.value = false }
}
async function copyId() {
  try { await navigator.clipboard.writeText(props.requestId) }
  catch (failure) { error.value = failure instanceof Error ? failure.message : '复制请求 ID 失败' }
}
watch(() => [props.requestId, props.sourceVersion], () => {
  detail.value = null
  phase.value = 'client'
  // 聚合列表已观测到指标版本时，正文详情也要刷新，避免继续显示旧的尝试结果。
  void load(Boolean(props.sourceVersion))
}, { immediate: true })
onBeforeUnmount(() => { revision++ })
</script>

<style scoped>
.detail-meta { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-bottom: 16px; }
.detail-meta code { overflow-wrap: anywhere; }
.phase-content { padding-top: 16px; }
.phase-meta { overflow-wrap: anywhere; margin-bottom: 12px; }
.context-change { margin-bottom: 16px; font-size: 13px; }
.context-change p { color: rgba(var(--v-theme-on-surface), 0.65); margin-top: 6px; }
summary { cursor: pointer; padding: 8px 0; }
</style>
