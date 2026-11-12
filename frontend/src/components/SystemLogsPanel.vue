<template>
  <div class="system-logs-panel">
    <div class="system-toolbar">
      <span class="text-medium-emphasis">{{ logs.conversations.length }} / {{ logs.totalConversations ?? '—' }} 个会话</span>
      <v-text-field v-model="search" label="搜索历史会话、任务、模型、渠道或请求 ID" maxlength="200" prepend-inner-icon="mdi-magnify" hide-details density="compact" clearable class="search-field" />
      <v-select v-model="logs.typeFilter" :items="typeItems" label="协议" hide-details density="compact" class="type-field" />
      <v-select v-model="logs.statusFilter" :items="statusItems" label="包含调用状态" hide-details density="compact" class="type-field" />
      <v-select v-model="days" :items="dateItems" label="调用时间" hide-details density="compact" class="type-field" />
      <v-btn variant="tonal" prepend-icon="mdi-refresh" :loading="logs.listLoading" @click="logs.refresh()">刷新</v-btn>
    </div>
    <v-alert v-if="logs.listError" type="error" variant="tonal" density="compact">{{ logs.listError }}</v-alert>
    <v-alert v-if="locateError" type="error" variant="tonal" density="compact">{{ locateError }}</v-alert>
    <div class="system-layout">
      <aside class="system-list" aria-label="日志会话列表">
        <div v-if="!logs.conversations.length" class="system-empty">{{ logs.listLoading ? '正在加载会话…' : '暂无会话日志' }}</div>
        <button v-for="group in logs.conversations" :key="group.key" type="button" class="conversation-entry" :class="{ selected: group.key === logs.selectedKey }" :aria-pressed="group.key === logs.selectedKey" @click="logs.selectConversation(group)">
          <div class="entry-heading">
            <span :title="group.title || group.key">{{ conversationTitle(group) }}</span>
            <span class="entry-time">{{ timeLabel(group.latest.timestamp) }}</span>
          </div>
          <div class="entry-model">{{ clientLabel(group.latest.clientFamily || group.latest.client) }} · {{ group.latest.model || entryLabel(group.latest.apiType) }}</div>
          <div class="entry-channel">{{ group.latest.channelName || '渠道未知' }} · {{ group.key.slice(-12) }}</div>
          <div class="entry-meta">
            <span>{{ group.requestCount }} 次调用</span>
            <span :class="`text-${logStatus(group.latest).color}`">最近：{{ logStatus(group.latest).label }}</span>
          </div>
        </button>
        <v-btn v-if="logs.nextConversationCursor" block variant="text" :loading="logs.listLoading" @click="logs.loadConversations(true)">加载更多会话</v-btn>
      </aside>
      <main class="system-detail">
        <div v-if="!logs.selectedKey" class="system-empty">选择一个会话，查看每次请求的新增输入和回复。</div>
        <template v-else>
          <div class="conversation-heading">
            <div>
              <strong>{{ logs.selectedConversation ? conversationTitle(logs.selectedConversation) : '会话轨迹' }}</strong>
              <div class="text-caption text-medium-emphasis">{{ clientLabel(logs.selectedConversation?.latest.clientFamily || logs.selectedConversation?.latest.client) }} · {{ entryLabel(logs.selectedConversation?.latest.apiType) }} · {{ logs.selectedConversation?.requestCount }} 次调用</div>
              <div class="text-caption text-medium-emphasis">{{ identityLabel(logs.selectedConversation?.latest.identitySource) }}；调用完成不代表整个会话结束</div>
            </div>
            <v-btn v-if="highlightRequestId" size="small" variant="text" prepend-icon="mdi-arrow-left" @click="emit('back')">返回请求日志</v-btn>
          </div>
          <v-alert v-if="logs.roundsError" type="error" variant="tonal" density="compact">{{ logs.roundsError }}</v-alert>
          <v-alert v-if="!logs.selectedConversation?.latest.conversationId" type="info" variant="tonal" density="compact">此请求没有可靠的会话标识，单独保留。</v-alert>
          <ContextTrend :group-key="logs.selectedKey" :version="conversationVersion(logs.selectedConversation)" @locate="emit('select', $event)" />
          <ConversationLogTimeline :group-key="logs.selectedKey" :requests="logs.requests" :highlight-request-id="highlightRequestId || requestId" @inspect="inspectRequest" />
        </template>
      </main>
    </div>
    <v-dialog v-model="detailOpen" max-width="1200" scrollable>
      <v-card>
        <v-card-title class="detail-dialog-heading">
          <span>请求流量详情</span>
          <v-btn icon="mdi-close" size="small" variant="text" aria-label="关闭请求详情" @click="detailOpen = false" />
        </v-card-title>
        <v-card-text><RequestLogDetail :request-id="inspectedRequestId" @locate="locateRelated" /></v-card-text>
      </v-card>
    </v-dialog>
  </div>
</template>

<script setup lang="ts">
// 页面只编排选择、筛选和可见期间的刷新；日志状态由 store 统一持有。
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ConversationLogTimeline from './ConversationLogTimeline.vue'
import RequestLogDetail from './RequestLogDetail.vue'
import ContextTrend from './ContextTrend.vue'
import { conversationVersion, useLogsStore } from '@/stores/logs'
import { logStatus } from '@/utils/logConversation'
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
const conversationTitle = (group: LogConversationSummary) => group.title || group.latest.promptPreview || `会话 ${group.key.slice(-12)}`
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
const timeLabel = (timestamp: string) => new Date(timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })

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
.system-logs-panel { display: flex; flex-direction: column; gap: 16px; height: 100%; min-height: 0; }
.system-toolbar { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.search-field { flex: 1; min-width: 200px; }
.type-field { flex: 0 1 180px; min-width: 150px; }
.system-layout { display: grid; grid-template-columns: 280px minmax(0, 1fr); gap: 20px; flex: 1; min-height: 0; }
.system-list, .system-detail { overflow: auto; min-height: 0; }
.system-list { border-right: 1px solid rgba(var(--v-theme-on-surface), 0.12); padding-right: 12px; }
.conversation-entry { display: block; width: 100%; text-align: left; color: rgb(var(--v-theme-on-surface)); padding: 14px 12px; margin-bottom: 4px; border-radius: 8px; background: transparent; border: 0; cursor: pointer; }
.conversation-entry:hover { background: rgba(var(--v-theme-primary), 0.05); }
.conversation-entry.selected { background: rgba(var(--v-theme-primary), 0.12); }
.conversation-entry:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: -2px; }
.entry-heading, .entry-meta { display: flex; justify-content: space-between; gap: 12px; }
.entry-heading > span:first-child { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.entry-time, .entry-meta { font-size: 12px; }
.entry-channel { font-size: 12px; color: rgba(var(--v-theme-on-surface), 0.55); margin-bottom: 8px; }
.entry-time { flex-shrink: 0; }
.entry-model { margin: 6px 0 10px; overflow-wrap: anywhere; }
.entry-time, .entry-model, .entry-meta { color: rgba(var(--v-theme-on-surface), 0.75); }
.conversation-heading, .detail-dialog-heading { display: flex; justify-content: space-between; align-items: center; gap: 12px; flex-wrap: wrap; padding-bottom: 16px; }
.system-empty { padding: 32px 16px; color: rgba(var(--v-theme-on-surface), 0.75); text-align: center; }
@media (max-width: 800px) { .system-layout { grid-template-columns: 210px minmax(0, 1fr); gap: 12px; } }
@media (max-width: 600px) { .system-layout { display: flex; flex-direction: column; overflow: auto; } .system-list { flex-shrink: 0; max-height: 240px; border-right: 0; border-bottom: 1px solid rgba(var(--v-theme-on-surface), 0.12); } .system-detail { flex-shrink: 0; overflow: visible; } }
</style>
