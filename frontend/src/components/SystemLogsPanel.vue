<template>
  <div class="system-logs-panel">
    <div class="system-toolbar">
      <span class="text-medium-emphasis">{{ logs.conversations.length }} 个会话</span>
      <v-text-field v-model="search" label="搜索当前会话列表" prepend-inner-icon="mdi-magnify" hide-details density="compact" clearable class="search-field" />
      <v-select v-model="logs.typeFilter" :items="typeItems" label="协议" hide-details density="compact" class="type-field" />
      <v-btn variant="tonal" prepend-icon="mdi-refresh" :loading="logs.listLoading" @click="logs.refresh()">刷新</v-btn>
    </div>
    <v-alert v-if="logs.listError" type="error" variant="tonal" density="compact">{{ logs.listError }}</v-alert>
    <v-alert v-if="locateError" type="error" variant="tonal" density="compact">{{ locateError }}</v-alert>
    <div class="system-layout">
      <aside class="system-list" aria-label="日志会话列表">
        <div v-if="!logs.conversations.length" class="system-empty">{{ logs.listLoading ? '正在加载会话…' : '暂无会话日志' }}</div>
        <button v-for="group in filteredGroups" :key="group.key" type="button" class="conversation-entry" :class="{ selected: group.key === logs.selectedKey }" :aria-pressed="group.key === logs.selectedKey" @click="logs.selectConversation(group)">
          <div class="entry-heading">
            <span>{{ group.latest.channelName || clientLabel(group.latest.client) }}</span>
            <span class="entry-time">{{ timeLabel(group.latest.timestamp) }}</span>
          </div>
          <div class="entry-model">{{ group.latest.model || entryLabel(group.latest.apiType) }}</div>
          <div class="entry-meta">
            <span>{{ group.requestCount }} 次请求</span>
            <span :class="`text-${logStatus(group.latest).color}`">{{ logStatus(group.latest).label }}</span>
          </div>
        </button>
        <v-btn v-if="logs.nextConversationCursor" block variant="text" :loading="logs.listLoading" @click="logs.loadConversations(true)">加载更多会话</v-btn>
      </aside>
      <main class="system-detail">
        <div v-if="!logs.selectedKey" class="system-empty">选择一个会话，查看每次请求的新增输入和回复。</div>
        <template v-else>
          <div class="conversation-heading">
            <div>
              <strong>{{ logs.selectedConversation?.latest.model || '会话日志' }}</strong>
              <div class="text-caption text-medium-emphasis">{{ clientLabel(logs.selectedConversation?.latest.client) }} · {{ entryLabel(logs.selectedConversation?.latest.apiType) }} · {{ logs.selectedConversation?.requestCount }} 次请求</div>
            </div>
            <v-btn v-if="highlightRequestId" size="small" variant="text" prepend-icon="mdi-arrow-left" @click="emit('back')">返回请求日志</v-btn>
          </div>
          <v-alert v-if="logs.roundsError" type="error" variant="tonal" density="compact">{{ logs.roundsError }}</v-alert>
          <v-alert v-if="!logs.selectedConversation?.latest.conversationId" type="info" variant="tonal" density="compact">此请求没有可靠的会话标识，单独保留。</v-alert>
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
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ConversationLogTimeline from './ConversationLogTimeline.vue'
import RequestLogDetail from './RequestLogDetail.vue'
import { useLogsStore } from '@/stores/logs'
import { logStatus } from '@/utils/logConversation'
import type { LogAPIType } from '@/types/logs'

const props = defineProps<{ requestId?: string; highlightRequestId?: string }>()
const emit = defineEmits<{ select: [requestId: string]; back: [] }>()
const logs = useLogsStore()
const search = ref('')
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
  { title: '命令 AI 复核', value: 'cmd_gate' }, { title: '内部 AI 调用', value: 'llmgate' }
]
const entryLabel = (kind?: string) => typeItems.find(item => item.value === kind?.toLowerCase())?.title || kind || '未知协议'
const clientLabel = (client?: string) => {
  if (!client) return '未知客户端'
  if (/codex/i.test(client)) return 'Codex'
  if (/claude-cli/i.test(client)) return 'Claude Code'
  if (/opencode/i.test(client)) return 'OpenCode'
  return client
}
const timeLabel = (timestamp: string) => new Date(timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
const filteredGroups = computed(() => {
  const needle = (search.value || '').trim().toLowerCase()
  return logs.conversations.filter(group => !needle || [group.latest.model, group.latest.channelName, group.latest.client, group.key].some(value => value?.toLowerCase().includes(needle)))
})

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
watch(() => logs.typeFilter, () => { logs.reset(); void logs.loadConversations() })
onMounted(async () => { await logs.loadConversations(); if (!disposed) await locate(props.requestId); scheduleRefresh() })
onBeforeUnmount(() => { disposed = true; locateRevision++; if (timer) clearTimeout(timer); logs.reset() })
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
.entry-time { flex-shrink: 0; }
.entry-model { margin: 6px 0 10px; overflow-wrap: anywhere; }
.entry-time, .entry-model, .entry-meta { color: rgba(var(--v-theme-on-surface), 0.75); }
.conversation-heading, .detail-dialog-heading { display: flex; justify-content: space-between; align-items: center; gap: 12px; flex-wrap: wrap; padding-bottom: 16px; }
.system-empty { padding: 32px 16px; color: rgba(var(--v-theme-on-surface), 0.75); text-align: center; }
@media (max-width: 800px) { .system-layout { grid-template-columns: 210px minmax(0, 1fr); gap: 12px; } }
@media (max-width: 600px) { .system-layout { display: flex; flex-direction: column; overflow: auto; } .system-list { flex-shrink: 0; max-height: 240px; border-right: 0; border-bottom: 1px solid rgba(var(--v-theme-on-surface), 0.12); } .system-detail { flex-shrink: 0; overflow: visible; } }
</style>
