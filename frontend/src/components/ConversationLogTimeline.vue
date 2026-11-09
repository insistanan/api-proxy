<template>
  <div class="conversation-timeline">
    <div class="timeline-actions">
      <v-btn v-if="shownCount < requests.length || logs.nextRequestCursor" size="small" variant="text" :loading="logs.roundsLoading" @click="loadEarlier">加载更早的请求</v-btn>
      <v-btn v-if="logs.anchored" size="small" variant="tonal" @click="logs.loadRounds({ latest: true })">回到最新请求</v-btn>
      <v-btn size="small" variant="text" prepend-icon="mdi-unfold-more-horizontal" :disabled="!rows.length" @click="expandAll">展开全部轮次</v-btn>
      <v-btn size="small" variant="text" prepend-icon="mdi-chevron-up" :disabled="!opened.size" @click="collapseAll">全部收起</v-btn>
      <span class="text-caption text-medium-emphasis">显示 {{ visible.length }} 个轮次</span>
    </div>
    <div v-if="!requests.length" class="pa-4 text-medium-emphasis">{{ logs.roundsLoading ? '正在加载轮次…' : '此会话没有可用的请求记录。' }}</div>
    <details
      v-for="row in rows"
      :id="`log-round-${row.summary.requestId}`"
      :key="row.summary.requestId"
      class="timeline-round"
      :class="[`round-${row.status.color}`, { highlighted: row.summary.requestId === highlightRequestId }]"
      :open="opened.has(row.summary.requestId)"
      @toggle="toggleRound(row.summary, $event)"
    >
      <summary class="round-header">
        <span class="round-index">第 {{ row.roundNumber }} 轮</span>
        <span class="round-time">{{ new Date(row.summary.startedAt || row.summary.unixMilli).toLocaleString() }}</span>
        <v-chip size="x-small" :color="row.status.color" variant="tonal">{{ row.status.label }}</v-chip>
        <span v-if="row.summary.model" class="round-meta">{{ row.summary.model }}</span>
        <span v-if="row.summary.channelName" class="round-meta">{{ row.summary.channelName }}</span>
        <span class="round-meta">{{ row.summary.stream ? 'stream' : 'normal' }}</span>
        <span class="round-spacer"></span>
        <v-icon class="round-chevron" icon="mdi-chevron-down" size="small" />
      </summary>
      <div v-if="opened.has(row.summary.requestId)" class="round-content">
        <div class="round-actions">
          <v-btn size="small" variant="text" prepend-icon="mdi-text-box-search-outline" @click="emit('inspect', row.summary.requestId)">流量详情</v-btn>
        </div>
        <v-alert v-if="logs.detailErrors[row.summary.requestId]" type="error" variant="tonal" density="compact">{{ logs.detailErrors[row.summary.requestId] }}</v-alert>
        <v-btn v-if="!row.detail || logs.detailErrors[row.summary.requestId]" size="small" variant="text" @click="loadOpened">加载本轮内容</v-btn>
        <template v-if="row.detail">
          <div v-if="row.delta" class="delta-notice">{{ row.delta.notice }}</div>
          <details v-if="row.delta?.top" class="round-config">
            <summary>请求配置</summary>
            <span v-if="row.delta.removedTopFields.length" class="text-caption">已移除：{{ row.delta.removedTopFields.join('、') }}</span>
            <JsonBodyPane :body="row.delta.top" wrap />
          </details>
          <div v-if="row.delta && !row.delta.repeated" class="trace-section">
            <div class="section-label">本次新增输入</div>
            <SessionInspector :body="row.delta.body" :replayable="false" compact history-input wrap />
          </div>
          <div v-else-if="!row.delta" class="text-caption text-medium-emphasis">本次没有输入正文记录。</div>
          <div v-if="row.output" class="trace-section">
            <div class="section-label">模型回复</div>
            <SessionInspector :body="row.output.body || ''" :replayable="false" compact wrap />
          </div>
          <div v-else class="text-caption text-medium-emphasis mt-3">{{ ['processing', 'retrying'].includes(row.detail.summary.status || '') ? '等待响应结束…' : '本次没有响应正文记录。' }}</div>
          <details v-if="row.attempts.length > 1" class="round-config">
            <summary>{{ row.attempts.length }} 次上游尝试</summary>
            <div v-for="attempt in row.attempts" :key="attempt.key" class="text-caption attempt-summary">
              {{ attempt.label }} · {{ attempt.response?.statusCode || attempt.headers?.statusCode || '未收到响应' }}
              <span v-if="attempt.error" class="text-error">{{ attempt.error.body }}</span>
            </div>
          </details>
          <v-alert v-if="row.detail.summary.errorMessage" class="mt-3" type="warning" variant="tonal" density="compact">{{ row.detail.summary.errorMessage }}</v-alert>
        </template>
      </div>
    </details>
  </div>
</template>

<script setup lang="ts">
// 轮次摘要可分页；仅展开轮次和它的前一轮需要正文，增量始终来自实际请求。
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useLogsStore, summaryVersion } from '@/stores/logs'
import type { SystemLogSummary } from '@/types/logs'
import { getLogAttempts, getRequestDelta, logStatus, requestInputLog } from '@/utils/logConversation'
import JsonBodyPane from './JsonBodyPane.vue'
import SessionInspector from './SessionInspector.vue'

const props = defineProps<{ groupKey: string; requests: SystemLogSummary[]; highlightRequestId?: string }>()
const emit = defineEmits<{ inspect: [requestId: string] }>()
const logs = useLogsStore()
const shownCount = ref(3)
const opened = ref(new Set<string>())
let generation = 0
let loadedGroupKey = ''
let latestRequestId = ''
const visible = computed(() => props.requests.slice(-shownCount.value))
// 会话通常只加载最近 20 条，轮次编号按会话总请求数校正，避免长会话从“第 1 轮”重新计数。
const roundOffset = computed(() => {
  const total = logs.selectedConversation?.requestCount || 0
  if (logs.anchored || !logs.nextRequestCursor || total <= props.requests.length) return 0
  return total - props.requests.length
})
const rows = computed(() => visible.value.map(summary => {
  const detail = logs.details[summary.requestId]
  const index = props.requests.findIndex(item => item.requestId === summary.requestId)
  const previous = index > 0 ? logs.details[props.requests[index - 1]!.requestId] : undefined
  const input = detail ? requestInputLog(detail.trafficLogs) : undefined
  const attempts = getLogAttempts(detail?.trafficLogs || [])
  const finalAttempt = attempts[attempts.length - 1]
  const synthesized = detail ? [...detail.trafficLogs].reverse().find(log => log.phase === 'stream_synth' && (!finalAttempt?.request?.attemptId || log.attemptId === finalAttempt.request.attemptId)) : undefined
  return {
    summary,
    detail,
    attempts,
    output: synthesized || finalAttempt?.response,
    delta: opened.value.has(summary.requestId) && input ? getRequestDelta(previous ? requestInputLog(previous.trafficLogs) : undefined, input) : undefined,
    roundNumber: roundOffset.value + index + 1,
    status: logStatus(detail?.summary || summary)
  }
}))

async function loadOpened() {
  const revision = ++generation
  const needed = new Map<string, SystemLogSummary>()
  props.requests.forEach((summary, index) => {
    if (!opened.value.has(summary.requestId)) return
    needed.set(summary.requestId, summary)
    if (index > 0) needed.set(props.requests[index - 1]!.requestId, props.requests[index - 1]!)
  })
  const pending = Array.from(needed.values())
  for (let offset = 0; offset < pending.length; offset += 3) {
    if (revision !== generation) return
    // 错误已写入 store 的逐轮错误状态；一轮失败不阻断其他轮次。
    await Promise.allSettled(pending.slice(offset, offset + 3).map(summary => logs.loadDetail(summary.requestId, summary)))
  }
}
function toggleRound(summary: SystemLogSummary, event: Event) {
  const expanded = (event.target as HTMLElement).hasAttribute('open')
  if (opened.value.has(summary.requestId) === expanded) return
  const next = new Set(opened.value)
  if (expanded) next.add(summary.requestId)
  else next.delete(summary.requestId)
  opened.value = next
  if (expanded) void loadOpened()
}
function expandAll() {
  opened.value = new Set(visible.value.map(summary => summary.requestId))
  void loadOpened()
}
function collapseAll() {
  opened.value = new Set()
}
async function loadEarlier() {
  if (shownCount.value >= props.requests.length && logs.nextRequestCursor) await logs.loadRounds({ more: true })
  shownCount.value += 3
}
async function revealHighlight() {
  const index = props.requests.findIndex(summary => summary.requestId === props.highlightRequestId)
  if (index < 0) return
  shownCount.value = Math.max(shownCount.value, props.requests.length - index)
  opened.value = new Set([...opened.value, props.highlightRequestId!])
  await loadOpened()
  await nextTick()
  document.getElementById(`log-round-${props.highlightRequestId}`)?.scrollIntoView({ block: 'nearest' })
}
watch(() => `${props.groupKey}:${props.requests.map(summary => `${summary.requestId}:${summaryVersion(summary)}`).join(',')}`, () => {
  const changed = loadedGroupKey !== props.groupKey
  if (changed) { generation++; shownCount.value = 3; opened.value = new Set(); loadedGroupKey = props.groupKey; latestRequestId = '' }
  const latest = props.requests[props.requests.length - 1]
  if (latest && latest.requestId !== latestRequestId) {
    const next = new Set(opened.value)
    next.delete(latestRequestId)
    next.add(latest.requestId)
    opened.value = next
    latestRequestId = latest.requestId
  }
  void loadOpened()
  void revealHighlight()
}, { immediate: true })
watch(() => props.highlightRequestId, () => { void revealHighlight() })
onBeforeUnmount(() => { generation++ })
</script>

<style scoped>
.timeline-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-bottom: 16px; }
.timeline-round {
  border: 1px solid rgba(var(--v-theme-outline), 0.4);
  border-left: 4px solid rgba(var(--v-theme-outline), 0.4);
  border-radius: 10px;
  background: rgb(var(--v-theme-surface));
  margin-bottom: 16px;
  overflow-wrap: anywhere;
}
.timeline-round.round-success { border-left-color: rgba(var(--v-theme-success), 0.75); }
.timeline-round.round-warning { border-left-color: rgba(var(--v-theme-warning), 0.75); }
.timeline-round.round-error { border-left-color: rgba(var(--v-theme-error), 0.8); }
.timeline-round.round-info { border-left-color: rgba(var(--v-theme-info), 0.75); }
.timeline-round.round-secondary { border-left-color: rgba(var(--v-theme-secondary), 0.6); }
.timeline-round.highlighted { border-color: rgb(var(--v-theme-primary)); box-shadow: 0 0 0 1px rgb(var(--v-theme-primary)); }
.round-header {
  position: sticky;
  top: 0;
  z-index: 2;
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  padding: 12px 16px;
  cursor: pointer;
  background: rgb(var(--v-theme-surface));
  border-bottom: 1px solid rgba(var(--v-theme-outline), 0.18);
  border-radius: 10px 10px 0 0;
  list-style: none;
  font-size: 14px;
}
.round-header::-webkit-details-marker { display: none; }
.timeline-round:not([open]) .round-header { border-bottom-color: transparent; }
.round-header:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: -2px; }
.round-index {
  font-size: 12px;
  font-weight: 700;
  padding: 2px 10px;
  border-radius: 999px;
  background: rgba(var(--v-theme-primary), 0.12);
  color: rgb(var(--v-theme-primary));
  white-space: nowrap;
}
.round-time { font-size: 13px; color: rgba(var(--v-theme-on-surface), 0.75); white-space: nowrap; }
.round-meta { font-size: 12px; color: rgba(var(--v-theme-on-surface), 0.55); }
.round-spacer { flex: 1 1 auto; }
.round-chevron { color: rgb(var(--v-theme-primary)); transition: transform 0.15s ease; }
.timeline-round[open] .round-chevron { transform: rotate(180deg); }
.round-content { padding: 4px 16px 16px; }
.round-actions { display: flex; justify-content: flex-end; }
.delta-notice {
  font-size: 13px;
  color: rgba(var(--v-theme-on-surface), 0.75);
  margin: 8px 0 12px;
  padding: 6px 10px;
  border-radius: 6px;
  background: rgba(var(--v-theme-surface-variant), 0.35);
}
.round-config { margin: 12px 0; }
.round-config > summary { cursor: pointer; padding: 8px 0; font-size: 13px; }
.trace-section { margin-top: 4px; }
.section-label {
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.02em;
  color: rgba(var(--v-theme-on-surface), 0.55);
  margin: 16px 0 8px;
}
.attempt-summary { padding: 6px 0; }
</style>
