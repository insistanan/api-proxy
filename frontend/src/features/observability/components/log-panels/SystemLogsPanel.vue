<template>
  <div class="system-logs-panel">
    <div class="system-toolbar">
      <span class="text-muted-foreground">{{ logs.conversations.length }} / {{ logs.totalConversations ?? '—' }} 个会话</span>
      <UiInput v-model="search" label="搜索历史会话、任务、模型、渠道或请求 ID" maxlength="200" prepend-inner-icon="mdi-magnify" hide-details density="compact" clearable class="search-field" />
      <UiSelect v-model="logs.typeFilter" :items="typeItems" label="协议" hide-details density="compact" class="type-field" />
      <UiSelect v-model="logs.statusFilter" :items="statusItems" label="包含调用状态" hide-details density="compact" class="type-field" />
      <UiSelect v-model="days" :items="dateItems" label="调用时间" hide-details density="compact" class="type-field" />
      <UiButton variant="tonal" prepend-icon="mdi-refresh" :loading="logs.listLoading" @click="logs.refresh()">刷新</UiButton>
    </div>
    <UiAlert v-if="logs.listError" type="error" variant="tonal" density="compact">{{ logs.listError }}</UiAlert>
    <UiAlert v-if="locateError" type="error" variant="tonal" density="compact">{{ locateError }}</UiAlert>
    <div class="system-layout">
      <aside class="system-list" aria-label="日志会话列表">
        <EmptyState v-if="logs.listLoading && !logs.conversations.length" variant="inline" icon="mdi-progress-clock" title="正在加载会话…" />
        <EmptyState v-else-if="!logs.conversations.length" variant="inline" icon="mdi-history" title="暂无会话日志" description="当前筛选条件下没有会话记录，可放宽筛选或稍后刷新。" />
        <button v-for="group in logs.conversations" :key="group.key" type="button" class="conversation-entry" :class="{ selected: group.key === logs.selectedKey }" :aria-pressed="group.key === logs.selectedKey" @click="logs.selectConversation(group)">
          <div class="entry-heading">
            <span :title="group.name || group.title || group.key">{{ conversationTitle(group) }}</span>
            <span class="entry-time">{{ formatClockTime(group.latest.timestamp) }}</span>
          </div>
          <div class="entry-model">{{ clientLabel(group.latest.clientFamily || group.latest.client) }} · {{ group.latest.model || entryLabel(group.latest.apiType) }}</div>
          <div class="entry-channel">{{ group.latest.channelName || '渠道未知' }} · {{ group.key.slice(-12) }}</div>
          <div class="entry-meta">
            <span>{{ group.requestCount }} 次调用</span>
            <span :class="`text-${logStatus(group.latest).color}`">最近：{{ logStatus(group.latest).label }}</span>
          </div>
        </button>
        <UiButton v-if="logs.nextConversationCursor" block variant="text" :loading="logs.listLoading" @click="logs.loadConversations(true)">加载更多会话</UiButton>
      </aside>
      <main class="system-detail">
        <EmptyState v-if="!logs.selectedKey" variant="inline" icon="mdi-format-list-bulleted" title="选择一个会话，查看每次请求的新增输入和回复。" />
        <template v-else>
          <div class="conversation-heading">
            <div>
              <strong>{{ logs.selectedConversation ? conversationTitle(logs.selectedConversation) : '会话轨迹' }}</strong>
              <div class="text-xs text-muted-foreground">{{ clientLabel(logs.selectedConversation?.latest.clientFamily || logs.selectedConversation?.latest.client) }} · {{ entryLabel(logs.selectedConversation?.latest.apiType) }} · {{ logs.selectedConversation?.requestCount }} 次调用</div>
              <div class="text-xs text-muted-foreground">{{ identityLabel(logs.selectedConversation?.latest.identitySource) }}；调用完成不代表整个会话结束</div>
            </div>
            <UiButton v-if="highlightRequestId" size="small" variant="text" prepend-icon="mdi-arrow-left" @click="emit('back')">返回请求日志</UiButton>
          </div>
          <UiAlert v-if="logs.roundsError" type="error" variant="tonal" density="compact">{{ logs.roundsError }}</UiAlert>
          <UiAlert v-if="!logs.selectedConversation?.latest.conversationId" type="info" variant="tonal" density="compact">此请求没有可靠的会话标识，单独保留。</UiAlert>
          <ContextTrend :group-key="logs.selectedKey" :version="conversationVersion(logs.selectedConversation)" @locate="emit('select', $event)" />
          <ConversationLogTimeline :group-key="logs.selectedKey" :requests="logs.requests" :highlight-request-id="highlightRequestId || requestId" @inspect="inspectRequest" />
        </template>
      </main>
    </div>
    <AppDialog v-model="detailOpen" title="请求流量详情" size="xl">
      <RequestLogDetail :request-id="inspectedRequestId" @locate="locateRelated" />
    </AppDialog>
  </div>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
// 页面只编排选择、筛选和可见期间的刷新；日志状态由 store 统一持有。
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ConversationLogTimeline from '@/features/observability/components/conversation/ConversationLogTimeline.vue'
import RequestLogDetail from '@/features/observability/components/log-panels/RequestLogDetail.vue'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'
import ContextTrend from '@/features/observability/components/context/ContextTrend.vue'
import { conversationVersion, useLogsStore } from '@/features/observability/stores/logs'
import { logStatus } from '@/features/observability/utils/logConversation'
import { formatClockTime } from '@/shared/utils/datetime'
import type { LogAPIType, LogConversationSummary } from '@/types/logs'

const props = defineProps<{ requestId?: string; highlightRequestId?: string }>()
const emit = defineEmits<{ select: [requestId: string]; back: [] }>()
const logs = useLogsStore()
const search = ref(logs.searchFilter)
const days = ref(0)
const locateError = ref('')
const detailOpen = ref(false)
const inspectedRequestId = ref('')
let disposed = false
let locateRevision = 0
let timer: ReturnType<typeof setTimeout> | undefined

const typeItems: { title: string; value: LogAPIType | '' }[] = [
  { title: '全部协议', value: '' }, { title: 'Messages', value: 'messages' },
  { title: 'Responses', value: 'responses' }, { title: 'Gemini', value: 'gemini' },
  { title: 'Chat', value: 'chat' }, { title: 'Images', value: 'images' },
  { title: 'Embeddings', value: 'embeddings' }, { title: 'Decisions', value: 'decisions' },
  { title: '命令 AI 复核', value: 'cmd_gate' }, { title: '内部 AI 调用', value: 'llmgate' }
]
const entryLabel = (kind?: string) => typeItems.find(item => item.value === kind?.toLowerCase())?.title || kind || '未知协议'
const statusItems = [
  { title: '全部状态', value: '' }, { title: '有失败调用', value: 'failed' },
  { title: '有进行中调用', value: 'processing' }, { title: '有重试调用', value: 'retrying' },
  { title: '有已完成调用', value: 'completed' }, { title: '有取消调用', value: 'cancelled' },
  { title: '有中断调用', value: 'interrupted' }, { title: '有拦截调用', value: 'blocked' }
]
const dateItems = [{ title: '全部历史', value: 0 }, { title: '最近 24 小时', value: 1 }, { title: '最近 7 天', value: 7 }]
const conversationTitle = (group: LogConversationSummary) => group.name || group.title || group.latest.promptPreview || `会话 ${group.key.slice(-12)}`
const identityLabels: Record<string, string> = {
  conversation_header: '会话请求头', claude_code_session: 'Claude Code 会话', conversation_query: '会话查询参数',
  session_header: '会话请求头', codex_thread: 'Codex 线程', request_conversation: '请求会话字段',
  metadata_conversation: '元数据会话字段', previous_response: '前序响应引用', request_session: '请求会话字段',
  structured_user_session: '客户端用户会话字段'
}
const identityLabel = (source?: string) => source ? `会话标识来源：${identityLabels[source] || source}` : '会话标识来源未记录'
const clientLabel = (client?: string) => {
  if (!client) return '未知客户端'
  if (/codex/i.test(client)) return 'Codex'
  if (/claude-cli|claude-code/i.test(client)) return 'Claude Code'
  if (/opencode/i.test(client)) return 'OpenCode'
  return client
}

async function locate(id?: string) {
  const revision = ++locateRevision
  locateError.value = ''
  if (!id) return
  try { await logs.locateRequest(id) }
  catch (error) { if (revision === locateRevision && !disposed) locateError.value = error instanceof Error ? error.message : '定位请求失败' }
}
function inspectRequest(id: string) { inspectedRequestId.value = id; detailOpen.value = true }
function locateRelated(id: string) { detailOpen.value = false; emit('select', id) }
function scheduleRefresh() {
  if (disposed) return
  timer = setTimeout(async () => {
    try { if (!document.hidden && !logs.listLoading && !logs.roundsLoading) await logs.refresh() }
    finally { scheduleRefresh() }
  }, logs.active ? 5000 : 15000)
}

watch(() => props.requestId, id => { void locate(id) })
watch(search, (value, _previous, onCleanup) => {
  logs.searchFilter = (value || '').trim()
  logs.reset()
  const pending = setTimeout(() => { void logs.loadConversations() }, 300)
  onCleanup(() => clearTimeout(pending))
})
watch([() => logs.typeFilter, () => logs.statusFilter, days], () => {
  logs.reset()
  logs.searchFilter = (search.value || '').trim()
  logs.fromFilter = days.value ? new Date(Date.now() - days.value * 86400000).toISOString() : ''
  logs.toFilter = ''
  void logs.loadConversations()
})
onMounted(async () => { await logs.loadConversations(); if (!disposed) await locate(props.requestId); scheduleRefresh() })
onBeforeUnmount(() => {
  disposed = true
  locateRevision++
  if (timer) clearTimeout(timer)
  logs.reset()
  logs.searchFilter = ''
  logs.statusFilter = ''
  logs.fromFilter = ''
  logs.toFilter = ''
})
</script>

<style scoped>
@reference "@/styles/index.css";

.system-logs-panel { @apply flex flex-col gap-4 h-full min-h-0; }
.system-toolbar { @apply flex items-center gap-3 flex-wrap; }
.search-field { @apply [flex:1] min-w-50; }
.type-field { @apply [flex:0_1_180px] min-w-37.5; }
.system-layout { @apply grid [grid-template-columns:280px_minmax(0,_1fr)] gap-5 [flex:1] min-h-0; }
.system-list, .system-detail { @apply overflow-auto min-h-0; }
.system-list { @apply [border-right:1px_solid_color-mix(in_srgb,_var(--foreground)_12%,_transparent)] pr-3; }
.conversation-entry { @apply block w-full text-left text-foreground [padding:14px_12px] mb-1 rounded-control [background:transparent] border-0 cursor-pointer; }
.conversation-entry:hover { @apply [background:color-mix(in_srgb,_var(--primary)_5%,_transparent)]; }
.conversation-entry.selected { @apply [background:color-mix(in_srgb,_var(--primary)_12%,_transparent)]; }
.conversation-entry:focus-visible { @apply [outline:2px_solid_var(--primary)] [outline-offset:-2px]; }
.entry-heading, .entry-meta { @apply flex justify-between gap-3; }
.entry-heading > span:first-child { @apply overflow-hidden text-ellipsis whitespace-nowrap; }
.entry-time, .entry-meta { @apply [font-size:12px]; }
.entry-channel { @apply [font-size:12px] [color:color-mix(in_srgb,_var(--foreground)_55%,_transparent)] mb-2; }
.entry-time { @apply shrink-0; }
.entry-model { @apply [margin:6px_0_10px] [overflow-wrap:anywhere]; }
.entry-time, .entry-model, .entry-meta { @apply [color:color-mix(in_srgb,_var(--foreground)_75%,_transparent)]; }
.conversation-heading, .detail-dialog-heading { @apply flex justify-between items-center gap-3 flex-wrap pb-4; }
@media (max-width: 800px) { .system-layout { @apply [grid-template-columns:210px_minmax(0,_1fr)] gap-3; } }
@media (max-width: 600px) { .system-layout { @apply flex flex-col overflow-auto; } .system-list { @apply shrink-0 max-h-60 [border-right:0] [border-bottom:1px_solid_color-mix(in_srgb,_var(--foreground)_12%,_transparent)]; } .system-detail { @apply shrink-0 overflow-visible; } }
</style>
