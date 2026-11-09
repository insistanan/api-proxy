<template>
  <div class="session-inspector">
    <div v-if="!compact" class="inspector-toolbar mb-2">
      <div class="d-flex align-center ga-2">
        <v-btn-toggle v-model="viewMode" mandatory density="compact" variant="outlined" divided>
          <v-btn value="structured" size="small">
            对话内容
          </v-btn>
          <v-btn value="raw" size="small" prepend-icon="mdi-code-json">
            原始 JSON
          </v-btn>
        </v-btn-toggle>
      </div>
      <div class="d-flex align-center ga-2">
        <v-btn
          v-if="replayable"
          size="small"
          variant="tonal"
          color="primary"
          prepend-icon="mdi-replay"
          :disabled="!canReplay"
          @click="copyCurlCommand"
        >
          {{ curlCopied ? '已复制 cURL' : '复制为 cURL' }}
        </v-btn>
        <v-btn
          size="small"
          variant="tonal"
          prepend-icon="mdi-content-copy"
          @click="copyRawBody"
        >
          {{ rawCopied ? '已复制' : '复制正文' }}
        </v-btn>
      </div>
    </div>

    <v-alert v-if="copyError" type="error" variant="tonal" density="compact" class="mb-2">{{ copyError }}</v-alert>
    <div v-if="viewMode === 'structured' && needsExpansion" class="pa-4 text-medium-emphasis">
      结构化正文较大，展开后再解析与渲染。
      <v-btn size="small" variant="text" @click="structuredExpanded = true">展开内容</v-btn>
    </div>
    <!-- 结构化卡片视图 -->
    <div v-else-if="viewMode === 'structured' && parsedItems.length > 0" class="inspector-content">
      <div v-for="(item, index) in parsedItems" :key="index" class="message-card mb-3" :class="item.role">
        <div class="message-card-header d-flex align-center justify-space-between px-3 py-1">
          <div class="d-flex align-center ga-2">
            <v-chip size="x-small" :color="roleColor(item.role)" variant="flat" class="font-weight-bold text-uppercase">
              {{ item.role }}
            </v-chip>
            <span v-if="item.name" class="text-caption font-mono text-medium-emphasis">[{{ item.name }}]</span>
          </div>
          <span class="text-caption text-medium-emphasis">#{{ index + 1 }}</span>
        </div>

        <div class="message-card-body pa-3">
          <!-- 思考过程 (Thinking / Reasoning) -->
          <v-expansion-panels v-if="item.thinking" class="mb-2" density="compact">
            <v-expansion-panel elevation="0" rounded="sm" class="border border-info">
              <v-expansion-panel-title class="py-1 px-3 text-caption font-weight-bold d-flex align-center">
                <v-icon icon="mdi-brain" size="small" color="info" class="mr-2" />
                思考过程 ({{ item.thinking.length }} 字符)
              </v-expansion-panel-title>
              <v-expansion-panel-text>
                <pre class="thinking-pre text-caption">{{ item.thinking }}</pre>
              </v-expansion-panel-text>
            </v-expansion-panel>
          </v-expansion-panels>

          <!-- 文本正文 -->
          <div v-if="item.text" class="message-text mb-2">
            <details v-if="historyInput && item.role === 'assistant'" :open="expandedBlocks.has(`history-${index}`)" @toggle="toggleBlock(`history-${index}`, $event)"><summary>客户端携带的历史回复</summary><pre v-if="expandedBlocks.has(`history-${index}`)" class="text-pre">{{ item.text }}</pre></details>
            <pre v-else class="text-pre">{{ item.text }}</pre>
          </div>

          <!-- 工具调用 (Tool Calls / Use) -->
          <div v-if="item.toolCalls && item.toolCalls.length > 0" class="tool-calls-container mb-2">
            <div v-for="(call, cIdx) in item.toolCalls" :key="cIdx" class="tool-call-card pa-2 mb-2 rounded border">
              <div class="d-flex align-center justify-space-between mb-1">
                <div class="d-flex align-center ga-2">
                  <v-icon icon="mdi-wrench" size="small" color="warning" />
                  <span class="font-weight-bold text-caption font-mono text-warning">调用工具: {{ call.name }}</span>
                </div>
                <span v-if="call.id" class="text-caption font-mono text-medium-emphasis">{{ call.id }}</span>
              </div>
              <details :open="expandedBlocks.has(`args-${index}-${cIdx}`)" @toggle="toggleBlock(`args-${index}-${cIdx}`, $event)"><summary>工具参数</summary><pre v-if="expandedBlocks.has(`args-${index}-${cIdx}`)" class="tool-args-pre text-caption">{{ formatArgs(call.args) }}</pre></details>
            </div>
          </div>

          <!-- 工具执行结果 (Tool Results) -->
          <div v-if="item.toolResults && item.toolResults.length > 0" class="tool-results-container mb-2">
            <div v-for="(res, rIdx) in item.toolResults" :key="rIdx" class="tool-result-card pa-2 mb-2 rounded border" :class="{ 'border-error': res.isError }">
              <div class="d-flex align-center justify-space-between mb-1">
                <div class="d-flex align-center ga-2">
                  <v-icon :icon="res.isError ? 'mdi-alert-circle' : 'mdi-check-circle'" size="small" :color="res.isError ? 'error' : 'success'" />
                  <span class="font-weight-bold text-caption font-mono" :class="res.isError ? 'text-error' : 'text-success'">
                    工具执行结果
                  </span>
                </div>
                <span v-if="res.toolUseId" class="text-caption font-mono text-medium-emphasis">{{ res.toolUseId }}</span>
              </div>
              <details :open="expandedBlocks.has(`result-${index}-${rIdx}`)" @toggle="toggleBlock(`result-${index}-${rIdx}`, $event)"><summary>查看工具结果</summary><pre v-if="expandedBlocks.has(`result-${index}-${rIdx}`)" class="tool-result-pre text-caption">{{ res.content }}</pre></details>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 无法解析或无结构化消息时退回 JSON -->
    <div v-else-if="viewMode === 'structured'" class="empty-structured pa-4 text-center text-medium-emphasis rounded border">
      <span>{{ parsed.notice }}</span>
      <JsonBodyPane :body="body || ''" :wrap="wrap" class="mt-2 text-left" />
    </div>

    <!-- 原始 JSON 视图 -->
    <JsonBodyPane v-else :body="body || ''" :wrap="wrap" />
  </div>
</template>

<script setup lang="ts">
// 协议解析由纯函数负责；大正文与工具细节展开后才解析或挂载。
import { computed, ref, watch } from 'vue'
import { parseSessionBody } from '@/utils/sessionPresentation'
import JsonBodyPane from './JsonBodyPane.vue'

const props = withDefaults(
  defineProps<{
    body?: string
    wrap?: boolean
    url?: string
    method?: string
    replayable?: boolean
    compact?: boolean
    historyInput?: boolean
  }>(),
  {
    body: '',
    wrap: true,
    url: '',
    method: 'POST',
    replayable: true,
    compact: false,
    historyInput: false
  }
)

const viewMode = ref<'structured' | 'raw'>('structured')
const curlCopied = ref(false)
const rawCopied = ref(false)
const copyError = ref('')
const expandedBlocks = ref(new Set<string>())

const structuredExpanded = ref(false)
const LARGE_BODY_CHARS = 200 * 1024
const needsExpansion = computed(() => props.body.length > LARGE_BODY_CHARS && !structuredExpanded.value)
const parsed = computed(() => viewMode.value === 'raw' || needsExpansion.value ? { items: [], notice: '' } : parseSessionBody(props.body))
const parsedItems = computed(() => parsed.value.items)

watch(() => props.body, () => { structuredExpanded.value = false; expandedBlocks.value = new Set(); copyError.value = '' })
const canReplay = computed(() => Boolean(props.body && props.body.trim()))

function toggleBlock(key: string, event: Event) {
  const next = new Set(expandedBlocks.value)
  if ((event.target as HTMLElement).hasAttribute('open')) next.add(key)
  else next.delete(key)
  expandedBlocks.value = next
}

function roleColor(role: string): string {
  switch (role.toLowerCase()) {
    case 'user':
      return 'primary'
    case 'assistant':
      return 'success'
    case 'system':
      return 'secondary'
    case 'tool':
      return 'warning'
    default:
      return 'grey'
  }
}

function formatArgs(args: unknown): string {
  if (!args) return '{}'
  if (typeof args === 'string') {
    try {
      return JSON.stringify(JSON.parse(args), null, 2)
    } catch {
      return args
    }
  }
  return JSON.stringify(args, null, 2)
}

async function copyRawBody() {
  if (!props.body) return
  copyError.value = ''
  try {
    await navigator.clipboard.writeText(props.body)
    rawCopied.value = true
    setTimeout(() => { rawCopied.value = false }, 2000)
  } catch (error) { copyError.value = error instanceof Error ? error.message : '复制正文失败' }
}

async function copyCurlCommand() {
  if (!props.body) return
  copyError.value = ''
  try {
    const url = new URL(props.url || '/v1/messages', window.location.origin).href
    const curl = `curl -X ${props.method || 'POST'} "${url}" \\\n  -H "Content-Type: application/json" \\\n  -d '${props.body.replace(/'/g, "'\\''")}'`
    await navigator.clipboard.writeText(curl)
    curlCopied.value = true
    setTimeout(() => { curlCopied.value = false }, 2000)
  } catch (error) { copyError.value = error instanceof Error ? error.message : '复制 cURL 失败' }
}
</script>

<style scoped>
.session-inspector {
  display: flex;
  flex-direction: column;
  width: 100%;
}
.inspector-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
}
.message-card {
  border: 1px solid rgba(var(--v-theme-outline), 0.25);
  border-radius: 8px;
  background: rgba(var(--v-theme-surface), 0.6);
  overflow: hidden;
}
.message-card-header {
  background: rgba(var(--v-theme-surface-variant), 0.35);
  border-bottom: 1px solid rgba(var(--v-theme-outline), 0.15);
}
.thinking-pre {
  white-space: pre-wrap;
  word-break: break-word;
  color: rgba(var(--v-theme-on-surface), 0.85);
  font-family: monospace;
  max-height: 300px;
  overflow-y: auto;
}
.text-pre {
  white-space: pre-wrap;
  word-break: break-word;
  font-family: inherit;
  line-height: 1.6;
  margin: 0;
}
.tool-args-pre,
.tool-result-pre {
  white-space: pre-wrap;
  word-break: break-word;
  font-family: monospace;
  background: rgba(var(--v-theme-surface-variant), 0.4);
  padding: 6px 8px;
  border-radius: 4px;
  margin: 0;
  max-height: 240px;
  overflow-y: auto;
}
</style>
