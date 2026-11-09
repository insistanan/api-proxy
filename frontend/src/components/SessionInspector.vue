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
    <!-- 轨迹视图：按 user / reasoning / tool_calls / tool 结果 / assistant 内容顺序展开 -->
    <div v-else-if="viewMode === 'structured' && blocks.length > 0" class="trace-list">
      <div
        v-for="(block, index) in blocks"
        :key="index"
        class="trace-block"
        :class="[`trace-${block.kind}`, { 'trace-error': block.isError }]"
      >
        <details v-if="block.muted" class="trace-history">
          <summary>{{ block.label }}（客户端携带的历史回复）</summary>
          <ClampText :text="block.text" :max-height="220" />
        </details>
        <template v-else>
          <div class="trace-block-head">
            <span class="trace-label">{{ block.label }}</span>
            <span v-if="block.meta" class="trace-meta">{{ block.meta }}</span>
          </div>
          <ClampText :text="block.text" :max-height="block.kind === 'tool_call' ? 200 : 260" />
        </template>
      </div>
    </div>

    <!-- 无法解析或无结构化消息时退回 JSON -->
    <div v-else-if="viewMode === 'structured'" class="empty-structured pa-4 text-center text-medium-emphasis rounded border">
      <span>{{ parsed.notice }}</span>
      <JsonBodyPane v-if="body" :body="body" :wrap="wrap" class="mt-2 text-left" />
    </div>

    <!-- 原始 JSON 视图 -->
    <JsonBodyPane v-else :body="body || ''" :wrap="wrap" />
  </div>
</template>

<script setup lang="ts">
// 协议解析由纯函数负责；大正文与长内容展开后才解析或渲染。
import { computed, ref, watch } from 'vue'
import { buildTraceBlocks, parseSessionBody } from '@/utils/sessionPresentation'
import ClampText from './ClampText.vue'
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

const structuredExpanded = ref(false)
const LARGE_BODY_CHARS = 64 * 1024
const needsExpansion = computed(() => props.body.length > LARGE_BODY_CHARS && !structuredExpanded.value)
const parsed = computed(() => {
  if (viewMode.value === 'raw' || needsExpansion.value) return { items: [], notice: '', mode: 'empty' as const }
  return parseSessionBody(props.body)
})
const blocks = computed(() => buildTraceBlocks(parsed.value.items, { foldAssistantHistory: props.historyInput }))

watch(() => props.body, () => {
  structuredExpanded.value = false
  copyError.value = ''
})
const canReplay = computed(() => Boolean(props.body && props.body.trim()))

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
  min-width: 0;
}

.inspector-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
}

.trace-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.trace-block {
  --trace-color: var(--v-theme-secondary);
  border-left: 3px solid rgba(var(--trace-color), 0.85);
  border-radius: 8px;
  background: rgba(var(--v-theme-surface-variant), 0.32);
  padding: 8px 12px 10px;
  min-width: 0;
}

.trace-user {
  --trace-color: var(--v-theme-primary);
}

.trace-assistant {
  --trace-color: var(--v-theme-success);
}

.trace-reasoning {
  --trace-color: var(--v-theme-info);
}

.trace-tool_call {
  --trace-color: var(--v-theme-warning);
}

.trace-tool_result {
  --trace-color: var(--v-theme-success);
}

.trace-system {
  --trace-color: var(--v-theme-secondary);
}

.trace-error {
  --trace-color: var(--v-theme-error);
}

.trace-block-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 4px;
}

.trace-label {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  font-weight: 700;
  color: rgb(var(--trace-color));
}

.trace-meta {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 11px;
  color: rgba(var(--v-theme-on-surface), 0.55);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.trace-history {
  margin: 0;
}

.trace-history > summary {
  cursor: pointer;
  font-size: 12px;
  color: rgba(var(--v-theme-on-surface), 0.7);
  padding: 2px 0;
}

.empty-structured {
  border-color: rgba(var(--v-theme-outline), 0.4);
}
</style>
