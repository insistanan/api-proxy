<template>
  <div class="session-inspector">
    <div class="inspector-toolbar mb-2">
      <div class="d-flex align-center ga-2">
        <v-btn-toggle v-model="viewMode" mandatory density="compact" variant="outlined" divided>
          <v-btn value="structured" size="small" prepend-icon="mdi-brain">
            结构化 ({{ itemsCount }})
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

    <!-- 结构化卡片视图 -->
    <div v-if="viewMode === 'structured' && parsedItems.length > 0" class="inspector-content">
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
            <pre class="text-pre">{{ item.text }}</pre>
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
              <pre class="tool-args-pre text-caption">{{ formatArgs(call.args) }}</pre>
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
              <pre class="tool-result-pre text-caption">{{ res.content }}</pre>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 无法解析或无结构化消息时退回 JSON -->
    <div v-else-if="viewMode === 'structured'" class="empty-structured pa-4 text-center text-medium-emphasis rounded border">
      <span>未检测到结构化对话或工具调用，已切换为 JSON 模式查看。</span>
      <JsonBodyPane :body="body || ''" :wrap="wrap" class="mt-2 text-left" />
    </div>

    <!-- 原始 JSON 视图 -->
    <JsonBodyPane v-else :body="body || ''" :wrap="wrap" />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import JsonBodyPane from './JsonBodyPane.vue'

const props = withDefaults(
  defineProps<{
    body?: string
    wrap?: boolean
    url?: string
    method?: string
    replayable?: boolean
  }>(),
  {
    body: '',
    wrap: true,
    url: '',
    method: 'POST',
    replayable: true
  }
)

const viewMode = ref<'structured' | 'raw'>('structured')
const curlCopied = ref(false)
const rawCopied = ref(false)

interface ParsedToolCall {
  id?: string
  name: string
  args: unknown
}

interface ParsedToolResult {
  toolUseId?: string
  content: string
  isError?: boolean
}

interface ParsedItem {
  role: string
  name?: string
  text?: string
  thinking?: string
  toolCalls?: ParsedToolCall[]
  toolResults?: ParsedToolResult[]
}

const parsedItems = computed<ParsedItem[]>(() => {
  if (!props.body) return []
  try {
    const data = JSON.parse(props.body)
    const items: ParsedItem[] = []

    // 1. Anthropic Claude 格式 (messages / content)
    const claudeMessages = Array.isArray(data.messages) ? data.messages : Array.isArray(data.content) && data.role ? [data] : undefined
    if (claudeMessages) {
      for (const m of claudeMessages) {
        const item: ParsedItem = { role: m.role || 'user' }
        if (typeof m.content === 'string') {
          item.text = m.content
        } else if (Array.isArray(m.content)) {
          for (const block of m.content) {
            if (block.type === 'text') {
              item.text = (item.text ? item.text + '\n' : '') + (block.text || '')
            } else if (block.type === 'thinking' || block.type === 'redacted_thinking') {
              item.thinking = (item.thinking ? item.thinking + '\n' : '') + (block.thinking || '[redacted thinking]')
            } else if (block.type === 'tool_use') {
              if (!item.toolCalls) item.toolCalls = []
              item.toolCalls.push({ id: block.id, name: block.name, args: block.input })
            } else if (block.type === 'tool_result') {
              if (!item.toolResults) item.toolResults = []
              const textContent = typeof block.content === 'string' ? block.content : JSON.stringify(block.content, null, 2)
              item.toolResults.push({ toolUseId: block.tool_use_id, content: textContent, isError: block.is_error })
            }
          }
        }
        if (Array.isArray(m.tool_calls)) {
          item.toolCalls = m.tool_calls.map((call: { id?: string; function?: { name?: string; arguments?: unknown } }) => ({ id: call.id, name: call.function?.name || 'function', args: call.function?.arguments }))
        }
        if (m.role === 'tool') item.toolResults = [{ toolUseId: m.tool_call_id, content: typeof m.content === 'string' ? m.content : JSON.stringify(m.content) }]
        items.push(item)
      }
      return items
    }

    // 2. OpenAI Chat Completions 格式
    if (Array.isArray(data.choices) && data.choices[0]?.message) {
      const msg = data.choices[0].message
      const item: ParsedItem = {
        role: msg.role || 'assistant',
        text: msg.content || '',
        thinking: msg.reasoning_content || ''
      }
      if (Array.isArray(msg.tool_calls)) {
        item.toolCalls = msg.tool_calls.map((tc: { id?: string; function?: { name?: string; arguments?: unknown } }) => ({
          id: tc.id,
          name: tc.function?.name || 'function',
          args: tc.function?.arguments
        }))
      }
      items.push(item)
      return items
    }

    // 3. OpenAI Responses 格式 (input 列表)
    const responseItems = Array.isArray(data.input) ? data.input : Array.isArray(data.output) ? data.output : typeof data.input === 'string' ? [{ type: 'message', role: 'user', content: [{ text: data.input }] }] : undefined
    if (responseItems) {
      for (const entry of responseItems) {
        if (entry.type === 'message' || (!entry.type && entry.role)) {
          const item: ParsedItem = { role: entry.role || 'user' }
          if (Array.isArray(entry.content)) {
            item.text = entry.content.map((c: { text?: string }) => c.text || '').filter(Boolean).join('\n')
          } else if (typeof entry.content === 'string') {
            item.text = entry.content
          }
          items.push(item)
        } else if (entry.type === 'function_call') {
          items.push({
            role: 'assistant',
            toolCalls: [{ id: entry.call_id, name: entry.name, args: entry.arguments }]
          })
        } else if (entry.type === 'function_call_output') {
          items.push({
            role: 'tool',
            toolResults: [{ toolUseId: entry.call_id, content: entry.output }]
          })
        }
      }
      return items
    }

    // 4. Gemini 格式 (contents)
    if (Array.isArray(data.contents)) {
      for (const c of data.contents) {
        const item: ParsedItem = { role: c.role === 'model' ? 'assistant' : 'user' }
        if (Array.isArray(c.parts)) {
          for (const p of c.parts) {
            if (p.thought) {
              item.thinking = (item.thinking ? item.thinking + '\n' : '') + (p.text || '')
            } else if (p.text) {
              item.text = (item.text ? item.text + '\n' : '') + p.text
            } else if (p.functionCall) {
              if (!item.toolCalls) item.toolCalls = []
              item.toolCalls.push({ name: p.functionCall.name, args: p.functionCall.args })
            } else if (p.functionResponse) {
              if (!item.toolResults) item.toolResults = []
              item.toolResults.push({ toolUseId: p.functionResponse.name, content: JSON.stringify(p.functionResponse.response, null, 2) })
            }
          }
        }
        items.push(item)
      }
      return items
    }

    return []
  } catch {
    return []
  }
})

const itemsCount = computed(() => parsedItems.value.length)
const canReplay = computed(() => Boolean(props.body && props.body.trim()))

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
  await navigator.clipboard.writeText(props.body)
  rawCopied.value = true
  setTimeout(() => {
    rawCopied.value = false
  }, 2000)
}

async function copyCurlCommand() {
  if (!props.body) return
  const url = new URL(props.url || '/v1/messages', window.location.origin).href
  const curl = `curl -X ${props.method || 'POST'} "${url}" \\\n  -H "Content-Type: application/json" \\\n  -d '${props.body.replace(/'/g, "'\\''")}'`
  await navigator.clipboard.writeText(curl)
  curlCopied.value = true
  setTimeout(() => {
    curlCopied.value = false
  }, 2000)
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
