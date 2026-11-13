<template>
  <section aria-label="日志存储状态">
    <v-alert v-if="logs.storageError" type="error" variant="tonal" density="compact">{{ logs.storageError }}</v-alert>
    <template v-if="logs.storage">
      <v-alert v-if="logs.storage.bodyCapturePaused" type="warning" variant="tonal" density="compact">日志预算已触发，新请求仅记录元数据；占用降至预算的 90% 后恢复。可在运行日志搜索 Logger-Budget 查看切换记录。</v-alert>
      <v-alert v-if="logs.storage.writeError || logs.storage.maintenance.cleanupError || logs.storage.maintenance.budgetError" type="error" variant="tonal" density="compact" class="mt-2">{{ [logs.storage.writeError, logs.storage.maintenance.cleanupError, logs.storage.maintenance.budgetError].filter(Boolean).join('；') }}</v-alert>
      <v-alert v-if="logs.storage.queue?.rejectedWrites" type="warning" variant="tonal" density="compact" class="mt-2">本次启动有 {{ logs.storage.queue.rejectedWrites }} 条日志未被接收，相关调用的记录可能不完整。最近一次：{{ logs.storage.queue.lastRejectedError }}<span v-if="logs.storage.queue.lastRejectedAt">（{{ formatDateTime(logs.storage.queue.lastRejectedAt) }}）</span></v-alert>
      <details class="storage-details">
        <summary>日志占用 {{ mib(logs.storage.usedBytes) }}<span v-if="logs.storage.policy.budgetMB"> / {{ logs.storage.policy.budgetMB }} MiB</span> · 存储与清理状态</summary>
        <p>已占用页 {{ mib(logs.storage.usedBytes) }} · 可复用页 {{ mib(logs.storage.freeBytes) }} · 主库文件 {{ mib(logs.storage.databaseBytes) }} · WAL {{ mib(logs.storage.walBytes) }}</p>
        <p>运行日志 {{ logs.storage.policy.runtimeDays }} 天 · 调用 {{ logs.storage.policy.callDays }} 天 · 正文 {{ logs.storage.policy.bodyDays }} 天</p>
        <template v-if="logs.storage.queue">
          <p>待落库 {{ logs.storage.queue.pendingEntries }} / {{ logs.storage.queue.capacityEntries }} 条（含处理中批次） · 缓冲 {{ mib(logs.storage.queue.pendingBytes) }} / {{ mib(logs.storage.queue.capacityBytes) }} · 本次启动已接收 {{ logs.storage.queue.acceptedWrites }} 条</p>
          <p>批次写入失败 {{ logs.storage.queue.writeFailures }} 次 · 恢复 {{ logs.storage.queue.writeRecoveries }} 次<span v-if="logs.storage.queue.lastRecoveryAt"> · 最近恢复 {{ formatDateTime(logs.storage.queue.lastRecoveryAt) }}</span></p>
          <p>日志入队最多等待 250 ms，拥堵或存储故障会明确拒绝新记录并计数；已接收记录继续重试落库。这里的“已接收”不等于已落库。</p>
        </template>
        <p>最近清理：{{ logs.storage.maintenance.lastCleanupAt ? formatDateTime(logs.storage.maintenance.lastCleanupAt) : '尚未完成' }} · 本次启动累计删除 {{ logs.storage.maintenance.deletedRows }} 行、移除 {{ logs.storage.maintenance.expiredBodies }} 条正文</p>
        <p v-if="logs.storage.maintenance.cleanupDeferred">当前写入积压，自动清理暂缓，待写入回落后继续。</p>
        <p>主库文件包含空闲页，WAL 可复用；自动清理不会执行 VACUUM，文件大小可能不会立即下降。</p>
        <p v-for="event in logs.storage.budgetEvents" :key="event.timestamp">{{ formatDateTime(event.timestamp) }} · {{ event.paused ? '正文采集降为元数据' : '预算限制解除，按采集配置记录' }} · {{ mib(event.usedBytes) }}</p>
        <v-btn size="small" variant="text" :loading="logs.storageLoading" @click="logs.loadStorageStatus()">刷新存储状态</v-btn>
      </details>
    </template>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted } from 'vue'
import { useLogsStore } from '@/stores/logs'
import { formatDateTime } from '../utils/datetime'

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
.storage-details { color: rgba(var(--v-theme-on-surface), 0.75); font-size: 13px; margin: 8px 0 16px; }
summary { cursor: pointer; padding: 6px 0; }
p { margin: 6px 0; }
</style>
