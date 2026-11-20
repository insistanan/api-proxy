<template>
  <div class="session-inspector">
    <div v-if="!compact" class="inspector-toolbar mb-2">
      <div class="flex items-center gap-2">
        <UiToggleGroup v-model="viewMode" mandatory density="compact" variant="outlined" divided>
          <UiButton value="structured" size="small">
            对话内容
          </UiButton>
          <UiButton value="raw" size="small" prepend-icon="mdi-code-json">
            原始 JSON
          </UiButton>
        </UiToggleGroup>
      </div>
      <div class="flex items-center gap-2">
        <UiButton
          v-if="replayable"
          size="small"
          variant="tonal"
          color="primary"
          prepend-icon="mdi-replay"
          :disabled="!canReplay"
          @click="copyCurlCommand"
        >
          {{ curlCopied ? '已复制 cURL' : '复制为 cURL' }}
        </UiButton>
        <UiButton
          size="small"
          variant="tonal"
          prepend-icon="mdi-content-copy"
          @click="copyRawBody"
        >
          {{ rawCopied ? '已复制' : '复制正文' }}
        </UiButton>
      </div>
    </div>

    <UiAlert v-if="copyError" type="error" variant="tonal" density="compact" class="mb-2">{{ copyError }}</UiAlert>
    <div v-if="viewMode === 'structured' && needsExpansion" class="p-4 text-muted-foreground">
      结构化正文较大，展开后再解析与渲染。
      <UiButton size="small" variant="text" @click="structuredExpanded = true">展开内容</UiButton>
    </div>
    <!-- 轨迹视图：按 user / reasoning / tool_calls / tool 结果 / assistant 内容顺序展开 -->
    <div v-else-if="viewMode === 'structured' && blocks.length > 0" class="trace-list">
      <UiAlert v-if="parsed.notice" type="warning" variant="tonal" density="compact">{{ parsed.notice }}</UiAlert>
      <div
        v-for="(block, index) in blocks"
        :key="index"
        class="trace-block"
        :class="[`trace-${block.kind}`, { 'trace-error': block.isError }]"
      >
        <details v-if="block.muted" class="trace-history">
          <summary>{{ block.label }}（客户端携带的历史回复）</summary>
          <JsonPreviewTrigger :data="block.text" :title="block.label" :disabled="!isJsonBlock(block.text)"><ClampText :text="block.text" :max-height="220" /></JsonPreviewTrigger>
        </details>
        <template v-else>
          <div class="trace-block-head">
            <span class="trace-label">{{ block.label }}</span>
            <span v-if="block.meta" class="trace-meta">{{ block.meta }}</span>
          </div>
          <JsonPreviewTrigger :data="block.text" :title="block.label" :disabled="block.kind !== 'tool_call' && !isJsonBlock(block.text)"><ClampText :text="block.text" :max-height="block.kind === 'tool_call' ? 200 : 260" /></JsonPreviewTrigger>
        </template>
      </div>
    </div>

    <!-- 无法解析或无结构化消息时退回 JSON -->
    <div v-else-if="viewMode === 'structured'" class="rounded border">
      <EmptyState variant="inline" icon="mdi-file-cog-outline" :title="parsed.notice || '没有可展示的结构化内容'" />
      <JsonBodyPane v-if="body" :body="body" :wrap="wrap" title="请求与响应正文" :meta="url" class="px-4 pb-4 text-left" />
    </div>

    <!-- 原始 JSON 视图 -->
    <JsonBodyPane v-else :body="body || ''" :wrap="wrap" title="请求与响应正文" :meta="url" />
  </div>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiToggleGroup from '@/shared/ui/button/ToggleGroup.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
// 协议解析由纯函数负责；大正文与长内容展开后才解析或渲染。
import { computed, ref, watch } from 'vue'
import { buildTraceBlocks, parseSessionBody } from '@/features/observability/utils/sessionPresentation'
import ClampText from '@/features/observability/components/common/ClampText.vue'
import JsonBodyPane from '@/features/observability/components/common/JsonBodyPane.vue'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'
import JsonPreviewTrigger from '@/shared/components/data/JsonPreviewTrigger.vue'
import { useClipboard } from '@/shared/composables/useClipboard'

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
const isJsonBlock = (text: string) => text.trim().startsWith('{') || text.trim().startsWith('[')
const curlCopied = ref(false)
const rawCopied = ref(false)
const copyError = ref('')
const { copyText } = useClipboard()

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
  if (!await copyText(props.body)) {
    copyError.value = '复制正文失败'
    return
  }
  rawCopied.value = true
  setTimeout(() => { rawCopied.value = false }, 2000)
}

async function copyCurlCommand() {
  if (!props.body) return
  copyError.value = ''
  const url = new URL(props.url || '/v1/messages', window.location.origin).href
  const curl = `curl -X ${props.method || 'POST'} "${url}" \\\n  -H "Content-Type: application/json" \\\n  -d '${props.body.replace(/'/g, "'\\''")}'`
  if (!await copyText(curl)) {
    copyError.value = '复制 cURL 失败'
    return
  }
  curlCopied.value = true
  setTimeout(() => { curlCopied.value = false }, 2000)
}
</script>

<style scoped>
@reference "@/styles/index.css";

.session-inspector {
  @apply flex flex-col w-full min-w-0;
}

.inspector-toolbar {
  @apply flex items-center justify-between flex-wrap gap-2;
}

.trace-list {
  @apply flex flex-col gap-2.5;
}

.trace-block {
  --trace-color: var(--secondary);
  @apply [border-left:3px_solid_color-mix(in_srgb,_var(--trace-color)_85%,_transparent)] rounded-control [background:color-mix(in_srgb,_var(--surface-variant)_32%,_transparent)] [padding:8px_12px_10px] min-w-0;
}

.trace-user {
  --trace-color: var(--primary);
}

.trace-assistant {
  --trace-color: var(--success);
}

.trace-reasoning {
  --trace-color: var(--info);
}

.trace-tool_call {
  --trace-color: var(--warning);
}

.trace-tool_result {
  --trace-color: var(--success);
}

.trace-system {
  --trace-color: var(--secondary);
}

.trace-error {
  --trace-color: var(--destructive);
}

.trace-block-head {
  @apply flex items-baseline justify-between gap-3 mb-1;
}

.trace-label {
  @apply [font-family:ui-monospace,_SFMono-Regular,_Menlo,_Consolas,_monospace] [font-size:12px] font-bold [color:var(--trace-color)];
}

.trace-meta {
  @apply [font-family:ui-monospace,_SFMono-Regular,_Menlo,_Consolas,_monospace] [font-size:11px] [color:color-mix(in_srgb,_var(--foreground)_55%,_transparent)] overflow-hidden text-ellipsis whitespace-nowrap;
}

.trace-history {
  @apply m-0;
}

.trace-history > summary {
  @apply cursor-pointer [font-size:12px] [color:color-mix(in_srgb,_var(--foreground)_70%,_transparent)] [padding:2px_0];
}
</style>
