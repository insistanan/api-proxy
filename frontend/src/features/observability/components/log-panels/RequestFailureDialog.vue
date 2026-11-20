<template>
  <AppDialog
    v-model="visible"
    title="终止 / 失败原因"
    :description="log ? (log.channelName || '#' + log.channelIndex) + ' · ' + (log.transform || log.resolvedModel || log.model || '未知模型') : ''"
    size="lg"
  >
    <div class="failure-type">{{ log?.errorType || '未提供错误类型' }}</div>
    <JsonPreviewTrigger :data="log?.errorMessage || ''" title="完整失败原因" :disabled="!log?.errorMessage"><pre class="failure-message" tabindex="0" aria-label="完整失败原因">{{ log?.errorMessage || '此记录未提供详细原因。' }}</pre></JsonPreviewTrigger>
    <UiAlert v-if="copyError" type="error" variant="tonal" density="compact" class="mt-3">{{ copyError }}</UiAlert>
    <template #actions>
      <UiButton v-if="log?.requestId" variant="text" @click="openRequest">查看请求日志</UiButton>
      <UiButton variant="tonal" color="primary" :prepend-icon="copied ? 'mdi-check' : 'mdi-content-copy'" :disabled="!log?.errorMessage" @click="copyMessage">
        {{ copied ? '已复制' : '复制完整原因' }}
      </UiButton>
    </template>
  </AppDialog>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
// 展示列表中当前尝试的原始错误，正文与复制操作共用同一份快照。
import { computed, ref, watch } from 'vue'
import type { RequestLogEntry } from '@/types/logs'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import JsonPreviewTrigger from '@/shared/components/data/JsonPreviewTrigger.vue'
import { useClipboard } from '@/shared/composables/useClipboard'

const log = defineModel<RequestLogEntry | null>({ default: null })
const emit = defineEmits<{ locate: [requestId: string] }>()
const visible = computed({
  get: () => log.value !== null,
  set: (value: boolean) => { if (!value) log.value = null }
})
const copied = ref(false)
const copyError = ref('')
const { copyText } = useClipboard()

watch(log, () => {
  copied.value = false
  copyError.value = ''
})

async function copyMessage() {
  const selected = log.value
  if (!selected?.errorMessage) return
  copyError.value = ''
  copied.value = false
  const ok = await copyText(selected.errorMessage)
  if (log.value === selected) {
    if (ok) copied.value = true
    else copyError.value = '复制失败，请手动选择正文复制。'
  }
}

function openRequest() {
  if (!log.value?.requestId) return
  emit('locate', log.value.requestId)
  visible.value = false
}
</script>

<style scoped>
@reference "@/styles/index.css";

.failure-title { @apply flex items-center justify-between gap-3 whitespace-normal; }
.failure-context { @apply whitespace-normal [overflow-wrap:anywhere]; }
.failure-type { @apply mb-3 font-semibold [overflow-wrap:anywhere]; }
.failure-message { @apply m-0 p-4 [border:1px_solid_color-mix(in_srgb,_var(--outline)_24%,_transparent)] rounded-control bg-background text-foreground whitespace-pre-wrap [overflow-wrap:anywhere] [font-size:0.8125rem] [line-height:1.7]; }
.failure-actions { @apply [padding:12px_20px_16px] flex-wrap gap-2; }
</style>
