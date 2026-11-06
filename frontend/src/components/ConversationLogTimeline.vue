<template>
  <div class="conversation-timeline">
    <div class="timeline-toolbar">
      <div>
        <strong>会话时间线</strong>
        <div class="text-caption text-medium-emphasis">按实际请求比较历史；上游响应单独展示。当前列表范围内共 {{ requests.length }} 次请求。</div>
      </div>
      <v-btn size="small" variant="tonal" :loading="loading" @click="loadVisible(true)">刷新</v-btn>
    </div>
    <v-btn v-if="shownCount < requests.length" class="mb-3" size="small" variant="tonal" :loading="loading" @click="loadEarlier">
      加载更早的请求（{{ requests.length - shownCount }}）
    </v-btn>
    <article v-for="row in rows" :key="row.summary.requestId" class="timeline-round">
      <div class="round-header">
        <div>
          <strong>{{ new Date(row.summary.startedAt || row.summary.unixMilli).toLocaleString() }}</strong>
          <v-chip class="ml-2" size="x-small" :color="logStatus(row.detail?.summary || row.summary).color" variant="tonal">
            {{ logStatus(row.detail?.summary || row.summary).label }}
          </v-chip>
        </div>
        <v-btn size="small" variant="text" @click="emit('inspect', row.summary.requestId)">完整请求详情</v-btn>
      </div>
      <div class="text-caption text-medium-emphasis mb-2">requestId：{{ row.summary.requestId }}</div>
      <v-alert v-if="errors[row.summary.requestId]" type="error" variant="tonal" density="compact" class="mb-2">
        {{ errors[row.summary.requestId] }}
      </v-alert>
      <template v-if="row.detail">
        <div v-if="row.delta" class="delta-notice">{{ row.delta.notice }}</div>
        <details v-if="row.delta?.top" class="round-config" :open="row.index === 0">
          <summary>{{ row.index === 0 ? '请求配置' : '请求配置发生变化' }}（system、tools、模型和生成参数）</summary>
          <div v-if="row.delta.removedTopFields.length" class="text-caption text-warning">已移除的配置字段：{{ row.delta.removedTopFields.join('、') }}</div>
          <JsonBodyPane :body="row.delta.top" wrap />
        </details>
        <div v-if="row.delta && !row.delta.repeated" class="round-input">
          <div class="section-label">本轮输入</div>
          <SessionInspector :body="row.delta.body" :replayable="false" wrap />
        </div>
        <v-alert v-if="!row.delta" type="info" variant="tonal" density="compact">本轮没有原请求正文记录，可展开完整详情查看上游记录。</v-alert>
        <div v-if="row.attempts.length" class="round-attempts">
          <div v-for="attempt in row.attempts" :key="attempt.key" class="attempt-summary">
            <span>{{ attempt.label }}</span>
            <span v-if="attempt.response?.statusCode || attempt.headers?.statusCode">HTTP {{ attempt.response?.statusCode || attempt.headers?.statusCode }}</span>
            <span v-if="attempt.error" class="text-error">{{ attempt.error.body }}</span>
            <span v-else-if="!attempt.response" class="text-medium-emphasis">{{ ['processing', 'retrying'].includes(row.detail.summary.status || '') ? '等待响应结束' : '没有响应正文记录' }}</span>
          </div>
        </div>
        <div v-if="row.output" class="round-output">
          <div class="section-label">{{ row.synthesized ? '上游响应的结构化合成' : '收到的上游响应' }}</div>
          <SessionInspector :body="row.output.body || ''" :replayable="false" wrap />
          <details v-if="row.synthesized && row.rawOutput" class="round-config">
            <summary>查看原始上游响应</summary>
            <JsonBodyPane :body="row.rawOutput.body || ''" wrap />
          </details>
        </div>
        <v-alert v-if="row.detail.summary.errorMessage" class="mt-2" :type="row.detail.summary.status === 'cancelled' ? 'info' : 'warning'" variant="tonal" density="compact">
          {{ row.detail.summary.errorMessage }}
        </v-alert>
      </template>
      <div v-else-if="loading" class="text-caption text-medium-emphasis">正在加载本轮详情…</div>
    </article>
  </div>
</template>

<script setup lang="ts">
// 按需加载少量轮次，避免打开会话就下载并渲染全部正文。
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/services/api'
import type { SystemLogDetail, SystemLogSummary } from '@/types/logs'
import { getLogAttempts, getRequestDelta, logStatus } from '@/utils/logConversation'
import JsonBodyPane from './JsonBodyPane.vue'
import SessionInspector from './SessionInspector.vue'

const props = defineProps<{ groupKey: string; requests: SystemLogSummary[]; seed?: SystemLogDetail | null }>()
const emit = defineEmits<{ inspect: [requestId: string] }>()
const shownCount = ref(3)
const loading = ref(false)
const details = ref<Record<string, SystemLogDetail>>({})
const errors = ref<Record<string, string>>({})
let generation = 0
let loadedGroupKey = ''

const visible = computed(() => props.requests.slice(-shownCount.value))
const rows = computed(() => visible.value.map((summary, index) => {
  const detail = details.value[summary.requestId]
  const input = detail?.trafficLogs.find(log => log.phase === 'client_request')
  const previous = index > 0 ? details.value[visible.value[index - 1]!.requestId]?.trafficLogs.find(log => log.phase === 'client_request') : undefined
  const attempts = getLogAttempts(detail?.trafficLogs || [])
  const finalAttempt = attempts[attempts.length - 1]
  const synthesized = detail ? [...detail.trafficLogs].reverse().find(log => log.phase === 'stream_synth' && (!finalAttempt?.request?.attemptId || log.attemptId === finalAttempt.request.attemptId)) : undefined
  const rawOutput = finalAttempt?.response
  return { summary, index, detail, delta: input ? getRequestDelta(previous, input) : undefined, attempts, output: synthesized || rawOutput, rawOutput, synthesized: Boolean(synthesized) }
}))

async function loadVisible(refresh = false) {
  const revision = ++generation
  loading.value = true
  const pending = visible.value.filter(entry => refresh || !details.value[entry.requestId])
  // 每批最多三次只读请求，减少切换会话时的并发和内存峰值。
  for (let offset = 0; offset < pending.length; offset += 3) {
    const batch = pending.slice(offset, offset + 3)
    const results = await Promise.allSettled(batch.map(entry => api.getSystemLog(entry.requestId)))
    if (revision !== generation) return
    results.forEach((result, index) => {
      const id = batch[index]!.requestId
      if (result.status === 'fulfilled') { details.value[id] = result.value; delete errors.value[id] }
      else errors.value[id] = result.reason instanceof Error ? result.reason.message : '加载请求详情失败'
    })
  }
  if (revision === generation) loading.value = false
}

function loadEarlier() { shownCount.value += 3; void loadVisible() }

watch(() => `${props.groupKey}:${props.requests.map(entry => `${entry.requestId}:${entry.unixMilli}:${entry.status}`).join(',')}`, () => {
  const groupChanged = loadedGroupKey !== props.groupKey
  if (groupChanged) {
    generation++
    shownCount.value = 3
    details.value = {}
    errors.value = {}
    loadedGroupKey = props.groupKey
  }
  if (props.seed) details.value[props.seed.requestId] = props.seed
  void loadVisible(!groupChanged)
}, { immediate: true })

watch(() => props.seed, seed => { if (seed) details.value[seed.requestId] = seed })
onBeforeUnmount(() => { generation++ })
</script>

<style scoped>
.conversation-timeline { padding: 18px; }
.timeline-toolbar, .round-header { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; margin-bottom: 14px; }
.timeline-round { border-left: 2px solid rgba(var(--v-theme-primary), 0.4); padding: 0 0 24px 16px; margin-bottom: 16px; overflow-wrap: anywhere; }
.delta-notice { color: rgb(var(--v-theme-on-surface)); background: rgba(var(--v-theme-primary), 0.08); padding: 8px 12px; margin-bottom: 12px; font-size: 13px; }
.round-config { margin-bottom: 12px; }
.round-config summary { cursor: pointer; padding: 10px 0; }
.round-config summary:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: 2px; }
.section-label { font-size: 14px; font-weight: 600; margin: 12px 0 8px; }
.round-attempts { margin: 12px 0; }
.attempt-summary { display: flex; flex-wrap: wrap; gap: 10px; font-size: 13px; padding: 6px 0; }
@media (max-width: 600px) { .conversation-timeline { padding: 12px; } .timeline-round { padding-left: 10px; } }
</style>
