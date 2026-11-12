<template>
  <section class="calls-panel" aria-label="按模型调用聚合的请求日志" :aria-busy="store.loading">
    <v-alert v-if="store.error" type="error" variant="tonal" density="compact">{{ store.error }}；可使用上方刷新按钮重试。</v-alert>
    <p class="calls-help">每行对应一次代理调用，状态以请求生命周期为准。展开只读取尝试指标；次数包含保留的历史记录，不代表实际计费次数。</p>
    <div class="calls-list">
      <p v-if="!store.calls.length" class="pa-4 text-medium-emphasis">{{ store.loading ? '正在读取调用…' : '暂无符合筛选条件的模型调用' }}</p>
      <details v-for="call in store.calls" :key="call.summary.requestId" class="call-row" :open="opened.has(call.summary.requestId)" @toggle="toggle(call, $event)">
        <summary class="call-heading">
          <span class="call-chevron" aria-hidden="true">▸</span>
          <span class="call-time">{{ formatTime(call.summary.startedAt || call.summary.unixMilli) }}</span>
          <span class="call-title"><strong>{{ call.summary.promptPreview || `调用 #${call.summary.requestId.slice(0, 8)}` }}</strong><span>{{ call.summary.apiType || '未知入口' }} · {{ call.summary.model || '模型未记录' }}</span></span>
          <v-chip size="x-small" :color="outcome(call).color" variant="tonal">{{ outcome(call).label }}</v-chip>
          <span>{{ call.attemptCount }} 条尝试</span>
          <span class="call-channel">{{ call.summary.channelName || '渠道未记录' }}</span>
        </summary>
        <div class="call-content">
          <div class="call-actions"><code>{{ call.summary.requestId }}</code><v-btn size="small" variant="text" @click="emit('inspect', call.summary.requestId)">正文详情</v-btn><v-btn size="small" variant="text" @click="emit('locate', call.summary.requestId)">会话轨迹</v-btn></div>
          <p v-if="!hasLifecycle(call)" class="text-caption text-warning">此调用没有完整生命周期记录，最后一次尝试的状态不能确认整个调用的最终结果。</p>
          <p v-if="call.summary.errorMessage" class="text-caption text-error">{{ call.summary.errorMessage }}</p>
          <v-alert v-if="store.attemptErrors[call.summary.requestId]" type="error" variant="tonal" density="compact">{{ store.attemptErrors[call.summary.requestId] }} <v-btn size="small" variant="text" @click="store.loadAttempts(call)">重试</v-btn></v-alert>
          <p v-if="store.attemptLoading[call.summary.requestId]" class="text-caption">正在读取尝试指标…</p>
          <template v-if="store.attempts[call.summary.requestId]">
            <p class="text-caption text-medium-emphasis">{{ store.attempts[call.summary.requestId]!.notice }}</p>
            <p v-if="store.attempts[call.summary.requestId]!.limited" class="text-caption text-warning">记录达到展示上限，以下尝试链不完整。</p>
            <p v-if="!store.attempts[call.summary.requestId]!.attempts.length" class="text-caption text-medium-emphasis">尚未记录尝试指标；调用可能仍在执行、本地结束，或指标已过期。可在正文详情中检查现有阶段。</p>
            <p v-else class="text-caption">渠道路径：{{ attemptPath(store.attempts[call.summary.requestId]!.attempts) }}</p>
            <ol class="attempt-chain">
              <li v-for="(attempt, index) in store.attempts[call.summary.requestId]!.attempts" :key="attempt.attemptId || index">
                <div class="attempt-heading"><strong>记录 {{ index + 1 }} · {{ attempt.channelName || (attempt.channelIndex != null ? `渠道 #${attempt.channelIndex}` : '渠道未记录') }}</strong><span :class="`text-${attempt.status === 'completed' ? 'success' : attempt.status === 'cancelled' ? 'warning' : 'error'}`">{{ attemptStatus(attempt) }}</span><span>{{ attempt.durationMs != null ? `${attempt.durationMs.toLocaleString()} ms` : '耗时未记录' }}</span><span v-if="attempt.retried" class="text-warning">记录标记将重试</span></div>
                <div class="text-caption text-medium-emphasis">{{ attempt.transform || attempt.resolvedModel || attempt.model || '模型未记录' }} · {{ attempt.baseUrl || '地址未记录' }} · {{ attempt.attemptId || '旧记录：尝试 ID 缺失' }}</div>
                <div class="text-caption">输入 {{ attempt.inputTokens ?? '未知' }} / 输出 {{ attempt.outputTokens ?? '未知' }} Token · {{ attempt.stream ? '流式' : '非流式' }}</div>
                <button v-if="attempt.errorType || attempt.errorMessage" type="button" class="attempt-error" @click="emit('failure', attempt)">{{ attempt.errorType || '终止原因' }} · {{ attempt.errorMessage || '查看详情' }}</button>
              </li>
            </ol>
          </template>
        </div>
      </details>
      <v-btn v-if="store.nextCursor && !store.capped" block variant="text" :loading="store.loading" :disabled="store.loading" @click="store.load(filters, true)">加载更早调用</v-btn>
      <p v-if="store.capped" class="text-caption text-medium-emphasis pa-3">当前最多展示 200 次调用，请缩小时间或协议范围继续查询。</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import type { LogCallFilters, LogCallSummary, RequestLogEntry } from '@/types/logs'
import { callVersion, useLogCallsStore } from '@/stores/logCalls'
import { logStatus } from '@/utils/logConversation'

const props = defineProps<{ filters: LogCallFilters }>()
const emit = defineEmits<{ inspect: [requestId: string]; locate: [requestId: string]; failure: [attempt: RequestLogEntry] }>()
const store = useLogCallsStore()
const opened = ref(new Set<string>())
const hasLifecycle = (call: LogCallSummary) => call.summary.phases.includes('request_end') || ['processing', 'retrying', 'interrupted'].includes(call.summary.status || '') && call.summary.phases.includes('request_start')
const outcome = (call: LogCallSummary) => hasLifecycle(call) ? logStatus(call.summary) : { label: '生命周期未记录', color: 'warning' }
const formatTime = (stamp: number) => new Date(stamp).toLocaleString()
const attemptStatus = (attempt: RequestLogEntry) => `${attempt.status === 'completed' ? '已完成' : attempt.status === 'cancelled' ? '已取消' : attempt.status === 'failed' ? '失败' : '状态未知'}${attempt.statusCode ? ` · HTTP ${attempt.statusCode}` : ''}`
const attemptPath = (attempts: RequestLogEntry[]) => attempts.map(attempt => attempt.channelName || (attempt.channelIndex != null ? `渠道 #${attempt.channelIndex}` : '渠道未知')).join(' → ')

function toggle(call: LogCallSummary, event: Event) {
  const expanded = (event.target as HTMLElement).hasAttribute('open')
  if (opened.value.has(call.summary.requestId) === expanded) return
  const next = new Set(opened.value)
  if (expanded) next.add(call.summary.requestId)
  else next.delete(call.summary.requestId)
  opened.value = next
  if (expanded) void store.loadAttempts(call)
}
watch(() => props.filters, (next, previous) => {
  if (JSON.stringify(next) !== JSON.stringify(previous)) opened.value = new Set()
  void store.load(props.filters)
}, { immediate: true })
watch(() => store.calls.map(callVersion).join('|'), () => {
  for (const call of store.calls) if (opened.value.has(call.summary.requestId)) void store.loadAttempts(call)
})
onBeforeUnmount(() => { store.reset() })
</script>

<style scoped>
.calls-panel { display: flex; flex: 1; flex-direction: column; min-height: 0; gap: 8px; }
.calls-help { font-size: 12px; color: rgba(var(--v-theme-on-surface), 0.65); margin-bottom: 8px; }
.calls-list { flex: 1; min-height: 0; overflow: auto; border-top: 1px solid rgba(var(--v-theme-outline), 0.24); }
.call-row { border-bottom: 1px solid rgba(var(--v-theme-outline), 0.24); }
.call-heading { display: flex; align-items: center; flex-wrap: wrap; gap: 16px; padding: 16px 12px; cursor: pointer; font-size: 12px; }
.call-heading:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: -2px; }
.call-chevron { color: rgb(var(--v-theme-primary)); transition: transform 0.15s ease; }
.call-row[open] .call-chevron { transform: rotate(90deg); }
.call-title { display: flex; flex: 1; min-width: 180px; flex-direction: column; gap: 4px; }
.call-title strong { font-size: 14px; overflow-wrap: anywhere; }
.call-title > span, .call-time, .call-channel { color: rgba(var(--v-theme-on-surface), 0.65); }
.call-content { padding: 0 16px 20px 28px; overflow-wrap: anywhere; }
.call-content p { margin: 8px 0; }
.call-actions, .attempt-heading { display: flex; gap: 12px; flex-wrap: wrap; align-items: center; }
.call-actions code { font-size: 12px; flex: 1; }
.attempt-chain { margin: 12px 0 0; padding-left: 20px; }
.attempt-chain li { padding: 12px; border-left: 2px solid rgba(var(--v-theme-primary), 0.35); }
.attempt-chain li + li { border-top: 1px solid rgba(var(--v-theme-outline), 0.12); }
.attempt-heading { font-size: 13px; margin-bottom: 6px; }
.attempt-error { display: block; margin-top: 6px; color: rgb(var(--v-theme-error)); text-align: left; font-size: 12px; white-space: pre-wrap; overflow-wrap: anywhere; }
@media (max-width: 600px) { .call-heading { gap: 10px; } .call-time { flex-basis: 100%; } .call-content { padding-left: 12px; } }
</style>
