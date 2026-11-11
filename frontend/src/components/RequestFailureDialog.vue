<template>
  <v-dialog v-model="visible" max-width="800" scrollable aria-labelledby="request-failure-title">
    <v-card v-if="log">
      <v-card-title id="request-failure-title" class="failure-title">
        <span>终止 / 失败原因</span>
        <v-btn icon="mdi-close" variant="text" size="small" aria-label="关闭失败详情" @click="visible = false" />
      </v-card-title>
      <v-card-subtitle class="failure-context">
        {{ log.channelName || `#${log.channelIndex}` }} · {{ log.transform || log.resolvedModel || log.model || '未知模型' }}
      </v-card-subtitle>
      <v-card-text>
        <div class="failure-type">{{ log.errorType || '未提供错误类型' }}</div>
        <pre class="failure-message" tabindex="0" aria-label="完整失败原因">{{ log.errorMessage || '此记录未提供详细原因。' }}</pre>
        <v-alert v-if="copyError" type="error" variant="tonal" density="compact" class="mt-3">{{ copyError }}</v-alert>
      </v-card-text>
      <v-card-actions class="failure-actions">
        <v-btn v-if="log.requestId" variant="text" @click="openRequest">查看请求日志</v-btn>
        <v-spacer />
        <v-btn variant="tonal" color="primary" :prepend-icon="copied ? 'mdi-check' : 'mdi-content-copy'" :disabled="!log.errorMessage" @click="copyMessage">
          {{ copied ? '已复制' : '复制完整原因' }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
// 展示列表中当前尝试的原始错误，正文与复制操作共用同一份快照。
import { computed, ref, watch } from 'vue'
import type { RequestLogEntry } from '@/types/logs'

const log = defineModel<RequestLogEntry | null>({ default: null })
const emit = defineEmits<{ locate: [requestId: string] }>()
const visible = computed({
  get: () => log.value !== null,
  set: (value: boolean) => { if (!value) log.value = null }
})
const copied = ref(false)
const copyError = ref('')

watch(log, () => {
  copied.value = false
  copyError.value = ''
})

async function copyMessage() {
  const selected = log.value
  if (!selected?.errorMessage) return
  copyError.value = ''
  copied.value = false
  try {
    await navigator.clipboard.writeText(selected.errorMessage)
    if (log.value === selected) copied.value = true
  } catch (error) {
    if (log.value === selected) copyError.value = error instanceof Error ? `复制失败：${error.message}` : '复制失败，请手动选择正文复制。'
  }
}

function openRequest() {
  if (!log.value?.requestId) return
  emit('locate', log.value.requestId)
  visible.value = false
}
</script>

<style scoped>
.failure-title { display: flex; align-items: center; justify-content: space-between; gap: 12px; white-space: normal; }
.failure-context { white-space: normal; overflow-wrap: anywhere; }
.failure-type { margin-bottom: 12px; font-weight: 600; overflow-wrap: anywhere; }
.failure-message { margin: 0; padding: 16px; border: 1px solid rgba(var(--v-theme-outline), 0.24); border-radius: var(--cut-md); background: rgb(var(--v-theme-background)); color: rgb(var(--v-theme-on-surface)); white-space: pre-wrap; overflow-wrap: anywhere; font-size: 0.8125rem; line-height: 1.7; }
.failure-actions { padding: 12px 20px 16px; flex-wrap: wrap; gap: 8px; }
</style>
