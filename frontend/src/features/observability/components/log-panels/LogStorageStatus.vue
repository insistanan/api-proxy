<template>
  <section aria-label="日志存储状态">
    <UiAlert v-if="logs.storageError" type="error" variant="tonal" density="compact">{{ logs.storageError }}</UiAlert>
    <template v-if="logs.storage">
      <UiAlert v-if="logs.storage.bodyCapturePaused" type="warning" variant="tonal" density="compact">日志空间达到预算，正文采集已暂停；空间释放后自动恢复。</UiAlert>
      <UiAlert v-if="logs.storage.writeError || logs.storage.maintenance.cleanupError || logs.storage.maintenance.budgetError" type="error" variant="tonal" density="compact" class="mt-2">{{ [logs.storage.writeError, logs.storage.maintenance.cleanupError, logs.storage.maintenance.budgetError].filter(Boolean).join('；') }}</UiAlert>
      <UiAlert v-if="logs.storage.queue?.rejectedWrites" type="warning" variant="tonal" density="compact" class="mt-2">本次启动有 {{ logs.storage.queue.rejectedWrites }} 条日志未被接收，相关调用的记录可能不完整。最近一次：{{ logs.storage.queue.lastRejectedError }}<span v-if="logs.storage.queue.lastRejectedAt">（{{ formatDateTime(logs.storage.queue.lastRejectedAt) }}）</span></UiAlert>
      <details class="storage-details">
        <summary>日志占用 {{ mib(logs.storage.usedBytes) }}<span v-if="logs.storage.policy.budgetMB"> / {{ logs.storage.policy.budgetMB }} MiB</span> · 存储状态</summary>
        <div class="storage-summary">
          <span>保留 {{ logs.storage.policy.runtimeDays }} / {{ logs.storage.policy.callDays }} / {{ logs.storage.policy.bodyDays }} 天 <small>运行 / 调用 / 正文</small></span>
          <span v-if="logs.storage.queue">待写 {{ logs.storage.queue.pendingEntries }} / {{ logs.storage.queue.capacityEntries }} <small>条</small></span>
          <span v-if="logs.storage.queue">写入异常 {{ logs.storage.queue.rejectedWrites + logs.storage.queue.writeFailures }} 次</span>
          <span>最近清理 {{ logs.storage.maintenance.lastCleanupAt ? formatDateTime(logs.storage.maintenance.lastCleanupAt) : '尚未完成' }}</span>
          <span v-if="logs.storage.maintenance.cleanupDeferred">清理等待写入完成</span>
        </div>
        <UiButton size="small" variant="text" :loading="logs.storageLoading" @click="logs.loadStorageStatus()">刷新存储状态</UiButton>
      </details>
    </template>
  </section>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import { onBeforeUnmount, onMounted } from 'vue'
import { useLogsStore } from '@/features/observability/stores/logs'
import { formatDateTime } from '@/shared/utils/datetime'

const logs = useLogsStore()
const mib = (bytes: number) => `${(bytes / 1048576).toFixed(1)} MiB`
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false
async function refresh() {
  if (disposed) return
  if (!document.hidden) await logs.loadStorageStatus()
  if (!disposed) timer = setTimeout(() => { void refresh() }, 30000)
}
onMounted(() => { void refresh() })
onBeforeUnmount(() => { disposed = true; if (timer) clearTimeout(timer) })
</script>

<style scoped>
@reference "@/styles/index.css";

.storage-details { @apply [color:color-mix(in_srgb,_var(--foreground)_75%,_transparent)] [font-size:13px] [margin:8px_0_16px]; }
summary { @apply cursor-pointer [padding:6px_0]; }
.storage-summary { @apply grid [grid-template-columns:repeat(auto-fit,_minmax(190px,_1fr))] gap-x-5 gap-y-1.5 [padding:4px_0_6px] text-xs; }
.storage-summary small { @apply text-muted-foreground; }
</style>
