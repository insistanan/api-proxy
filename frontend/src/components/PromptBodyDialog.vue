<template>
  <v-dialog :model-value="modelValue" max-width="980" persistent @update:model-value="emit('update:modelValue', $event)">
    <v-card>
      <v-card-title class="dialog-title d-flex align-center justify-space-between">
        <span>最终发送给大模型的提示词</span>
        <v-btn icon="mdi-close" size="small" variant="text" :aria-label="'关闭正文弹窗'" @click="emit('update:modelValue', false)" />
      </v-card-title>
      <v-divider />
      <v-card-text class="dialog-body">
        <div class="prompt-meta mb-3">
          <v-chip size="x-small" variant="tonal" class="font-mono">{{ requestId }}</v-chip>
          <span v-if="attemptCount > 1" class="text-caption text-medium-emphasis">
            共 {{ attemptCount }} 次上游尝试，展示最后一次
          </span>
          <v-chip v-if="truncated" size="x-small" color="warning" variant="tonal">
            正文超 1MB 已被截断
          </v-chip>
          <v-chip v-if="body" size="x-small" variant="tonal">{{ formatBytes(body.length) }}</v-chip>
        </div>
        <div v-if="loading" class="prompt-empty">
          <v-progress-circular indeterminate size="24" width="2" color="primary" />
        </div>
        <div v-else-if="error" class="prompt-empty text-error">{{ error }}</div>
        <div v-else-if="!body" class="prompt-empty">
          <span>未找到上游请求正文。确认 ENABLE_REQUEST_LOGS 未关闭，且该请求已完整落库。</span>
        </div>
        <SessionInspector v-else :key="requestId" :body="body" wrap />
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import SessionInspector from '@/components/SessionInspector.vue'
import { api } from '@/services/api'

const props = defineProps<{
  modelValue: boolean
  requestId: string
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
}>()

const loading = ref(false)
const error = ref('')
const body = ref('')
const attemptCount = ref(0)
const truncated = ref(false)

const formatBytes = (bytes: number) => {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

const load = async (requestId: string) => {
  loading.value = true
  error.value = ''
  body.value = ''
  attemptCount.value = 0
  truncated.value = false
  try {
    const detail = await api.getSystemLog(requestId)
    const upstreamRequests = detail.trafficLogs.filter(entry => entry.phase === 'upstream_request')
    attemptCount.value = upstreamRequests.length
    const latest = upstreamRequests[upstreamRequests.length - 1]
    truncated.value = Boolean(latest?.truncated)
    body.value = latest?.body || ''
  } catch (err) {
    error.value = err instanceof Error ? err.message : '加载正文失败'
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.modelValue, props.requestId],
  ([open, requestId]) => {
    if (open && requestId) {
      load(String(requestId))
    }
  },
  { immediate: true }
)
</script>

<style scoped>
.dialog-title {
  font-weight: 700;
}

.dialog-body {
  max-height: 72vh;
  overflow: auto;
}

.prompt-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.font-mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.prompt-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 160px;
  text-align: center;
  color: rgba(var(--v-theme-on-surface), 0.62);
}
</style>
