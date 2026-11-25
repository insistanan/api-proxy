<template>
  <div class="conversation-timeline">
    <div class="timeline-actions">
      <UiButton v-if="shownCount < requests.length || logs.nextRequestCursor" size="small" variant="text" :loading="logs.roundsLoading" @click="loadEarlier">加载更早的请求</UiButton>
      <UiButton v-if="logs.anchored" size="small" variant="tonal" @click="logs.loadRounds({ latest: true })">回到最新请求</UiButton>
      <UiButton size="small" variant="text" prepend-icon="mdi-unfold-more-horizontal" :disabled="!rows.length" @click="expandAll">展开当前调用</UiButton>
      <UiButton size="small" variant="text" prepend-icon="mdi-chevron-up" :disabled="!opened.size" @click="collapseAll">全部收起</UiButton>
      <span class="text-xs text-muted-foreground">显示 {{ visible.length }} 次调用；一次用户轮次可能包含多次调用</span>
    </div>
    <div v-if="!requests.length" class="p-4 text-muted-foreground">{{ logs.roundsLoading ? '正在加载调用…' : '此会话没有可用的请求记录。' }}</div>
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
        <span class="round-index" :title="row.summary.requestId">调用 #{{ row.summary.requestId.slice(0, 8) }}</span>
        <span class="round-time">{{ formatDateTime(row.summary.startedAt || row.summary.unixMilli) }}</span>
        <UiBadge size="x-small" :color="row.status.color" variant="tonal">{{ row.status.label }}</UiBadge>
        <span class="round-meta tabular-nums" :title="row.active ? '自本次请求开始计时' : '本次请求的总耗时'">{{ requestDuration(row.summary) }}</span>
        <span v-if="row.summary.model" class="round-meta">{{ row.summary.model }}</span>
        <span v-if="row.summary.channelName" class="round-meta">{{ row.summary.channelName }}</span>
        <span class="round-meta">{{ row.summary.stream ? 'stream' : 'normal' }}</span>
        <span class="round-spacer"></span>
        <UiIcon class="round-chevron" icon="mdi-chevron-down" size="small" />
        <span v-if="row.summary.promptPreview" class="round-prompt" :title="row.summary.promptPreview">
          <span class="text-muted-foreground">输入摘要：</span>{{ row.summary.promptPreview }}
        </span>
      </summary>
      <div v-if="opened.has(row.summary.requestId)" class="round-content">
        <div class="round-actions">
          <UiButton size="small" variant="text" prepend-icon="mdi-text-box-search-outline" @click="emit('inspect', row.summary.requestId)">流量详情</UiButton>
        </div>
        <UiAlert v-if="logs.detailErrors[row.summary.requestId]" type="error" variant="tonal" density="compact">{{ logs.detailErrors[row.summary.requestId] }}</UiAlert>
        <UiButton v-if="!row.detail || logs.detailErrors[row.summary.requestId]" size="small" variant="text" @click="loadOpened">加载本轮内容</UiButton>
        <template v-if="row.detail">
          <ContextSnapshotPanel v-if="row.context && row.input" :snapshot="row.context" :body="row.input.body || ''" />
          <ToolTraceLinks v-if="row.detail.toolLinks" :links="row.detail.toolLinks" @locate="emit('inspect', $event)" />
          <div v-if="row.delta" class="delta-notice">{{ row.delta.notice }}</div>
          <details v-if="row.delta?.top" class="round-config">
            <summary>请求配置</summary>
            <span v-if="row.delta.removedTopFields.length" class="text-xs">已移除：{{ row.delta.removedTopFields.join('、') }}</span>
            <JsonBodyPane :body="row.delta.top" title="本次请求配置" :meta="row.summary.requestId" wrap />
          </details>
          <div v-if="row.delta && !row.delta.repeated" class="trace-section">
            <div class="section-label">本次新增输入</div>
            <SessionInspector :body="row.delta.body" :replayable="false" compact history-input wrap />
          </div>
          <div v-else-if="!row.delta" class="text-xs text-muted-foreground">本次没有输入正文记录。</div>
          <div v-if="row.output" class="trace-section">
            <div class="section-label">模型回复</div>
            <SessionInspector :body="row.output.body || ''" :replayable="false" compact wrap />
          </div>
          <div v-else class="text-xs text-muted-foreground mt-3">{{ row.active ? '等待响应结束…' : '本次没有响应正文记录。' }}</div>
          <details v-if="row.attempts.length > 1" class="round-config">
            <summary>{{ row.attempts.length }} 次上游尝试</summary>
            <div v-for="attempt in row.attempts" :key="attempt.key" class="text-xs attempt-summary">
              {{ attempt.label }} · {{ attempt.response?.statusCode || attempt.headers?.statusCode || '未收到响应' }}
              <span v-if="attempt.error" class="text-destructive">{{ attempt.error.body }}</span>
            </div>
          </details>
          <UiAlert v-if="row.detail.summary.errorMessage" class="mt-3" type="warning" variant="tonal" density="compact">{{ row.detail.summary.errorMessage }}</UiAlert>
        </template>
      </div>
    </details>
  </div>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
// 轮次摘要可分页；仅展开轮次和它的前一轮需要正文，增量始终来自实际请求。
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { conversationVersion, summaryVersion } from '@/features/observability/stores/logs'
import { useLogSession } from '@/features/observability/composables/useLogSession'
import type { SystemLogSummary } from '@/types/logs'
import { getLogAttempts, getRequestDelta, logStatus, requestInputLog } from '@/features/observability/utils/logConversation'
import JsonBodyPane from '@/features/observability/components/common/JsonBodyPane.vue'
import SessionInspector from '@/features/observability/components/conversation/SessionInspector.vue'
import ContextSnapshotPanel from '@/features/observability/components/context/ContextSnapshotPanel.vue'
import ToolTraceLinks from '@/features/observability/components/conversation/ToolTraceLinks.vue'
import { formatDateTime } from '@/shared/utils/datetime'
import { useAutoRefresh } from '@/shared/composables/useAutoRefresh'

const props = defineProps<{ groupKey: string; requests: SystemLogSummary[]; highlightRequestId?: string }>()
const emit = defineEmits<{ inspect: [requestId: string] }>()
const logs = useLogSession()
const shownCount = ref(3)
const opened = ref(new Set<string>())
const now = ref(Date.now())
let generation = 0
let loadedGroupKey = ''
let latestRequestId = ''
const visible = computed(() => props.requests.slice(-shownCount.value))
const rows = computed(() => visible.value.map(summary => {
  const detail = logs.details[summary.requestId]
  // 已结束的摘要优先，避免折叠卡片的旧详情让计时继续增长。
  const detailEnded = detail && ['completed', 'cancelled', 'failed', 'blocked', 'interrupted'].includes(detail.summary.status || '')
  const current = isRequestActive(summary) && detail && detailEnded ? detail.summary : summary
  const index = props.requests.findIndex(item => item.requestId === summary.requestId)
  const previous = index > 0 ? logs.details[props.requests[index - 1]!.requestId] : undefined
  const input = detail ? requestInputLog(detail.trafficLogs) : undefined
  const attempts = getLogAttempts(detail?.trafficLogs || [])
  const finalAttempt = attempts[attempts.length - 1]
  const synthesized = detail ? [...detail.trafficLogs].reverse().find(log => log.phase === 'stream_synth' && (!finalAttempt?.request?.attemptId || log.attemptId === finalAttempt.request.attemptId)) : undefined
  return {
    summary: current,
    active: isRequestActive(current),
    detail,
    input,
    context: detail?.contexts?.find(snapshot => snapshot.phase === 'client_input' || snapshot.phase === 'client_request'),
    attempts,
    output: synthesized || finalAttempt?.response,
    delta: opened.value.has(summary.requestId) && input ? getRequestDelta(previous ? requestInputLog(previous.trafficLogs) : undefined, input) : undefined,
    status: logStatus(current)
  }
}))

function isRequestActive(summary: SystemLogSummary) {
  return !summary.endedAt && ['processing', 'retrying'].includes(summary.status || '')
}

function requestDuration(summary: SystemLogSummary): string {
  const active = isRequestActive(summary)
  const startedAt = summary.startedAt
  const duration = active
    ? startedAt ? now.value - startedAt : undefined
    : summary.durationMs ?? (startedAt && summary.endedAt ? summary.endedAt - startedAt : undefined)
  if (duration === undefined || !Number.isFinite(duration) || duration < 0) return '耗时未知'
  const seconds = Math.floor(duration / 1000)
  const minutes = Math.floor(seconds / 60)
  const elapsed = seconds < 60 ? `${seconds}秒` : minutes < 60 ? `${minutes}分${seconds % 60}秒` : `${Math.floor(minutes / 60)}小时${minutes % 60}分${seconds % 60}秒`
  return `${active ? '已运行' : '耗时'} ${elapsed}`
}

const { start: startClock } = useAutoRefresh(() => { now.value = Date.now() }, computed(() => !rows.value.some(row => row.active)), 1000)
onMounted(startClock)

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
watch(() => `${props.groupKey}:${conversationVersion(logs.selectedConversation)}:${props.requests.map(summary => `${summary.requestId}:${summaryVersion(summary)}`).join(',')}`, () => {
  now.value = Date.now()
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
@reference "@/styles/index.css";

.timeline-actions { @apply flex items-center gap-2 flex-wrap mb-4; }
.timeline-round {
  @apply [border:1px_solid_color-mix(in_srgb,_var(--outline)_40%,_transparent)] [border-left:4px_solid_color-mix(in_srgb,_var(--outline)_40%,_transparent)] rounded-control bg-card mb-4 [overflow-wrap:anywhere];
}
.timeline-round.round-success { @apply [border-left-color:color-mix(in_srgb,_var(--success)_75%,_transparent)]; }
.timeline-round.round-warning { @apply [border-left-color:color-mix(in_srgb,_var(--warning)_75%,_transparent)]; }
.timeline-round.round-error { @apply [border-left-color:color-mix(in_srgb,_var(--destructive)_80%,_transparent)]; }
.timeline-round.round-info { @apply [border-left-color:color-mix(in_srgb,_var(--info)_75%,_transparent)]; }
.timeline-round.round-secondary { @apply [border-left-color:color-mix(in_srgb,_var(--secondary)_60%,_transparent)]; }
.timeline-round.highlighted { @apply border-primary [box-shadow:0_0_0_1px_var(--primary)]; }
.round-header {
  @apply sticky top-0 [z-index:2] flex items-center gap-2.5 flex-wrap [padding:12px_16px] cursor-pointer [background:var(--surface)] [border-bottom:1px_solid_color-mix(in_srgb,_var(--outline)_18%,_transparent)] [border-radius:10px_10px_0_0] [list-style:none] [font-size:14px];
}
.round-header::-webkit-details-marker { @apply hidden; }
.timeline-round:not([open]) .round-header { @apply [border-bottom-color:transparent]; }
.round-header:focus-visible { @apply [outline:2px_solid_var(--primary)] [outline-offset:-2px]; }
.round-index {
  @apply [font-size:12px] font-bold [padding:2px_10px] rounded-dialog [background:color-mix(in_srgb,_var(--primary)_12%,_transparent)] text-primary whitespace-nowrap;
}
.round-time { @apply [font-size:13px] [color:color-mix(in_srgb,_var(--foreground)_75%,_transparent)] whitespace-nowrap; }
.round-meta { @apply [font-size:12px] [color:color-mix(in_srgb,_var(--foreground)_55%,_transparent)]; }
.round-prompt { @apply basis-full min-w-0 truncate text-sm text-foreground; }
.round-spacer { @apply [flex:1_1_auto]; }
.round-chevron { @apply text-primary [transition:transform_0.15s_ease]; }
.timeline-round[open] .round-chevron { @apply [transform:rotate(180deg)]; }
.round-content { @apply [padding:4px_16px_16px]; }
.round-actions { @apply flex justify-end; }
.delta-notice {
  @apply [font-size:13px] [color:color-mix(in_srgb,_var(--foreground)_75%,_transparent)] [margin:8px_0_12px] [padding:6px_10px] rounded-badge [background:color-mix(in_srgb,_var(--surface-variant)_35%,_transparent)];
}
.round-config { @apply [margin:12px_0]; }
.round-config > summary { @apply cursor-pointer [padding:8px_0] [font-size:13px]; }
.trace-section { @apply mt-1; }
.section-label {
  @apply [font-size:12px] font-semibold [letter-spacing:0.02em] [color:color-mix(in_srgb,_var(--foreground)_55%,_transparent)] [margin:16px_0_8px];
}
.attempt-summary { @apply [padding:6px_0]; }
</style>
