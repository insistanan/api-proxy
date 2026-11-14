<template>
  <AppDialog v-model="visible" :title="conversation.name ? `${conversation.name} · 上下文与轨迹` : '上下文与轨迹'" size="xl">
    <div class="d-flex align-center flex-wrap ga-2 mb-4">
      <code class="trace-key">{{ groupKey }}</code>
      <v-spacer />
      <v-btn variant="tonal" size="small" prepend-icon="mdi-refresh" :loading="logs.roundsLoading" @click="refresh">刷新</v-btn>
    </div>
    <v-alert v-if="logs.roundsError" type="error" variant="tonal" class="mb-4">{{ logs.roundsError }}</v-alert>
    <ContextTrend :group-key="groupKey" :version="String(version)" @locate="locate" />
    <EmptyState v-if="!logs.roundsLoading && !logs.roundsError && !logs.requests.length" variant="inline" icon="mdi-history" title="暂无会话日志" description="该对话尚未保存日志，或日志已按保留策略清理。对话信息仍可正常查看和命名。" />
    <ConversationLogTimeline v-else :group-key="groupKey" :requests="logs.requests" :highlight-request-id="highlightRequestId" @inspect="inspect" />
    <AppDialog v-model="detailOpen" title="请求流量详情" size="xl">
      <RequestLogDetail v-if="detailOpen" :request-id="inspectedRequestId" @locate="inspect" />
    </AppDialog>
  </AppDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import type { ConversationEntry } from '@/types/conversation'
import AppDialog from '@/shared/components/common/AppDialog.vue'
import EmptyState from '@/shared/components/common/EmptyState.vue'
import ContextTrend from '@/features/observability/components/context/ContextTrend.vue'
import ConversationLogTimeline from '@/features/observability/components/conversation/ConversationLogTimeline.vue'
import RequestLogDetail from '@/features/observability/components/log-panels/RequestLogDetail.vue'
import { provideLogSession } from '@/features/observability/composables/useLogSession'

const visible = defineModel<boolean>({ required: true })
const props = defineProps<{ conversation: ConversationEntry }>()
const logs = provideLogSession()
const groupKey = computed(() => `${props.conversation.apiKind.toLowerCase()}:${props.conversation.id}`)
const version = ref(0)
const highlightRequestId = ref('')
const detailOpen = ref(false)
const inspectedRequestId = ref('')

async function refresh() {
  logs.selectedKey = groupKey.value
  await logs.loadRounds({ latest: true })
  version.value++
}
async function locate(requestId: string) {
  logs.selectedKey = groupKey.value
  if (!logs.requests.some(request => request.requestId === requestId)) await logs.loadRounds({ anchor: requestId })
  highlightRequestId.value = requestId
}
function inspect(requestId: string) {
  inspectedRequestId.value = requestId
  detailOpen.value = true
}
watch(() => logs.requests, requests => {
  const latest = requests[requests.length - 1]
  logs.selectedConversation = latest ? { key: groupKey.value, latest, requestCount: requests.length } : null
})
watch(groupKey, () => {
  logs.reset()
  highlightRequestId.value = ''
  detailOpen.value = false
  void refresh()
}, { immediate: true })
onBeforeUnmount(() => logs.reset())
</script>

<style scoped>
.trace-key { overflow-wrap: anywhere; }
</style>
