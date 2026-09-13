<template>
  <section aria-label="命令安全记录" class="mt-4">
    <div class="d-flex align-center ga-2 mb-2">
      <span class="text-subtitle-2 font-weight-bold">命令安全记录（{{ total }}）</span>
      <v-spacer />
      <v-btn size="small" variant="text" :loading="loading" @click="loadPage(page)">刷新</v-btn>
    </div>
    <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mb-2">{{ error }}</v-alert>
    <p v-if="!loading && !error && entries.length === 0" class="text-caption text-medium-emphasis">
      该对话暂无危险命令检测记录。
    </p>
    <v-expansion-panels variant="accordion">
      <v-expansion-panel v-for="entry in entries" :key="entry.id">
        <v-expansion-panel-title>
          <div class="d-flex flex-wrap align-center ga-2">
            <span class="text-caption">{{ new Date(entry.timestamp).toLocaleString() }}</span>
            <v-chip size="x-small" :color="entry.blockType === 'dangerous_cmd_allow' ? 'success' : 'error'">
              {{ entry.blockType === 'dangerous_cmd_allow' ? '已放行' : '已拦截' }}
            </v-chip>
            <span class="text-caption">
              {{ entry.aiReview ? (entry.aiReview.reused ? '复用 AI 结论' : 'AI 复核') : '未保存 AI 复核详情' }}
            </span>
          </div>
        </v-expansion-panel-title>
        <v-expansion-panel-text>
          <template v-if="entry.aiReview">
            <div class="text-body-2 mb-2">
              {{ reviewVerdictLabel(entry.aiReview.verdict) }} ·
              {{ entry.aiReview.model || '未取得模型信息' }} ·
              {{ entry.aiReview.channelName || '未取得渠道信息' }} ·
              {{ entry.aiReview.durationMs }} ms
            </div>
            <p class="record-text mb-2">{{ entry.aiReview.reason }}</p>
          </template>
          <div v-if="entry.command" class="mb-2">
            <div class="text-caption text-medium-emphasis">命中命令片段</div>
            <pre class="record-text">{{ entry.command }}</pre>
          </div>
          <div class="text-caption text-medium-emphasis">最终处理</div>
          <pre class="record-text mb-2">{{ entry.promptSnippet || '旧记录未保存处理详情' }}</pre>
          <div class="d-flex flex-wrap ga-2">
            <v-btn
              v-if="entry.aiReview?.requestId"
              size="small"
              variant="tonal"
              :to="{ path: '/logs', query: { tab: 'system', id: entry.aiReview.requestId } }"
            >查看 AI 复核日志</v-btn>
            <v-btn
              v-if="entry.requestId"
              size="small"
              variant="text"
              :to="{ path: '/logs', query: { tab: 'system', id: entry.requestId } }"
            >查看原请求</v-btn>
          </div>
        </v-expansion-panel-text>
      </v-expansion-panel>
    </v-expansion-panels>
    <v-pagination
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
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { api, type BlockedLogEntry, type CommandAIReview } from '@/services/api'

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
.record-text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: 0.8125rem;
}
</style>
