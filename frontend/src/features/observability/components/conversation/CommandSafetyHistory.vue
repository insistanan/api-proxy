<template>
  <section aria-label="命令安全记录" class="mt-4">
    <div class="flex items-center gap-2 mb-2">
      <span class="text-sm font-medium font-bold">命令安全记录（{{ total }}）</span>
      <span class="flex-1" ></span>
      <UiButton size="small" variant="text" :loading="loading" @click="loadPage(page)">刷新</UiButton>
    </div>
    <UiAlert v-if="error" type="error" variant="tonal" density="compact" class="mb-2">{{ error }}</UiAlert>
    <EmptyState v-if="!loading && !error && entries.length === 0" variant="inline" icon="mdi-shield-check" title="该对话暂无危险命令检测记录" description="出现危险命令时，检测与判定结果会记录在这里。" />
    <UiAccordion variant="accordion">
      <UiAccordionItem v-for="entry in entries" :key="entry.id">
        <summary class="cursor-pointer px-4 py-3 font-medium">
          <div class="flex flex-wrap items-center gap-2">
            <span class="text-xs">{{ formatDateTime(entry.timestamp) }}</span>
            <UiBadge size="x-small" :color="entry.blockType === 'dangerous_cmd_allow' ? 'success' : 'error'">
              {{ entry.blockType === 'dangerous_cmd_allow' ? '已放行' : '已拦截' }}
            </UiBadge>
            <span class="text-xs">
              {{ entry.aiReview ? (entry.aiReview.reused ? '复用 AI 结论' : 'AI 复核') : '未保存 AI 复核详情' }}
            </span>
          </div>
        </summary>
        <div class="px-4 pb-4">
          <template v-if="entry.aiReview">
            <div class="text-sm mb-2">
              {{ reviewVerdictLabel(entry.aiReview.verdict) }} ·
              {{ entry.aiReview.model || '未取得模型信息' }} ·
              {{ entry.aiReview.channelName || '未取得渠道信息' }} ·
              {{ entry.aiReview.durationMs }} ms
            </div>
            <p class="record-text mb-2">{{ entry.aiReview.reason }}</p>
          </template>
          <div v-if="entry.command" class="mb-2">
            <div class="text-xs text-muted-foreground">命中命令片段</div>
            <pre class="record-text">{{ entry.command }}</pre>
          </div>
          <div class="text-xs text-muted-foreground">最终处理</div>
          <JsonPreviewTrigger :data="entry.promptSnippet || ''" title="命令处理详情" :disabled="!entry.promptSnippet"><pre class="record-text mb-2">{{ entry.promptSnippet || '旧记录未保存处理详情' }}</pre></JsonPreviewTrigger>
          <div class="flex flex-wrap gap-2">
            <UiButton
              v-if="entry.aiReview?.requestId"
              size="small"
              variant="tonal"
              :to="{ path: '/logs', query: { tab: 'system', id: entry.aiReview.requestId } }"
            >查看 AI 复核日志</UiButton>
            <UiButton
              v-if="entry.requestId"
              size="small"
              variant="text"
              :to="{ path: '/logs', query: { tab: 'system', id: entry.requestId } }"
            >查看原请求</UiButton>
          </div>
        </div>
      </UiAccordionItem>
    </UiAccordion>
    <UiPagination
      v-if="total > pageSize"
      :model-value="page"
      :length="Math.ceil(total / pageSize)"
      :disabled="loading"
      :total-visible="5"
      density="compact"
      aria-label="命令安全记录分页"
      @update:model-value="loadPage"
    />
  </section>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiPagination from '@/shared/ui/data/Pagination.vue'
import UiAccordion from '@/shared/ui/disclosure/Accordion.vue'
import UiAccordionItem from '@/shared/ui/disclosure/AccordionItem.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { api, type BlockedLogEntry, type CommandAIReview } from '@/api'
import { formatDateTime } from '@/shared/utils/datetime'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'
import JsonPreviewTrigger from '@/shared/components/data/JsonPreviewTrigger.vue'

const props = defineProps<{ conversationId: string }>()
const pageSize = 20
const page = ref(1)
const total = ref(0)
const entries = ref<BlockedLogEntry[]>([])
const loading = ref(false)
const error = ref('')
let sequence = 0
let refreshTimer: ReturnType<typeof setInterval> | undefined

const reviewVerdictLabel = (verdict: CommandAIReview['verdict']) => ({
  allow: 'AI 判定安全',
  block: 'AI 判定危险',
  error: 'AI 复核失败'
})[verdict]

const loadPage = async (nextPage: number) => {
  const currentSequence = ++sequence
  loading.value = true
  error.value = ''
  try {
    const response = await api.getBlockedLogGroupEntries({
      groupKey: `conversation:${props.conversationId}`,
      category: 'dangerous_cmd',
      page: nextPage,
      pageSize
    })
    if (currentSequence !== sequence) return
    const lastPage = Math.max(1, Math.ceil(response.total / pageSize))
    if (nextPage > lastPage) {
      await loadPage(lastPage)
      return
    }
    entries.value = response.logs
    total.value = response.total
    page.value = response.page
  } catch (cause) {
    if (currentSequence !== sequence) return
    error.value = cause instanceof Error ? cause.message : '读取命令安全记录失败'
  } finally {
    if (currentSequence === sequence) loading.value = false
  }
}

watch(() => props.conversationId, () => {
  entries.value = []
  total.value = 0
  page.value = 1
  void loadPage(1)
}, { immediate: true })

onMounted(() => {
  refreshTimer = setInterval(() => {
    if (!loading.value && !document.hidden) void loadPage(page.value)
  }, 5000)
})

onUnmounted(() => {
  sequence++
  clearInterval(refreshTimer)
})
</script>

<style scoped>
@reference "@/styles/index.css";

.record-text {
  @apply whitespace-pre-wrap [overflow-wrap:anywhere] [font-size:0.8125rem];
}
</style>
