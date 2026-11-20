<template>
  <RequestStatsPanel
    ref="panel"
    :api-type="apiType"
    :compact="compact"
    :show-title="false"
    default-duration="6h"
    @drill="openLogs"
  />
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { RequestStatsPanel } from '@/features/observability'
import type { ApiTab } from '@/features/channels/services/channel-api'
import type { RequestStatsDrill } from '@/types/metrics'

const props = withDefaults(defineProps<{ apiType: ApiTab; compact?: boolean }>(), { compact: false })
const router = useRouter()
const panel = ref<{
  refreshData: (silent?: boolean) => Promise<void>
  startAutoRefresh: () => void
  stopAutoRefresh: () => void
} | null>(null)

function openLogs(filters: RequestStatsDrill) {
  const query: Record<string, string> = {}
  for (const [key, value] of Object.entries(filters)) {
    if (value) query[key] = String(value)
  }
  void router.push({ path: '/logs', query })
}

async function refreshData(silent = false) { await panel.value?.refreshData(silent) }
function startAutoRefresh() { panel.value?.startAutoRefresh() }
function stopAutoRefresh() { panel.value?.stopAutoRefresh() }
defineExpose({ refreshData, startAutoRefresh, stopAutoRefresh })
</script>
