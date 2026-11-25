<template>
  <UiTooltip location="bottom" :open-delay="150" @update:open="handleOpenChange">
    <template #activator="{ props: tooltipProps }">
      <button
        v-bind="tooltipProps"
        type="button"
        class="inline-flex size-7 shrink-0 items-center justify-center rounded-control text-destructive hover:bg-destructive/10 focus-visible:outline-2 focus-visible:outline-ring"
        aria-label="正在保活，查看各会话的上下文和心跳时间"
        @click.stop
      >
        <UiIcon size="18" class="keepalive-heart" aria-hidden="true">mdi-heart-pulse</UiIcon>
      </button>
    </template>
    <div class="w-80 max-w-full">
      <div class="flex items-center justify-between gap-3 font-medium">
        <span>缓存保活</span>
        <span v-if="loading" class="text-muted-foreground">读取中…</span>
      </div>
      <div class="text-muted-foreground">
        每 {{ policy.intervalSeconds }} 秒
        <span v-if="policy.maxAttempts"> · 每会话最多 {{ policy.maxAttempts }} 次</span>
        <span v-if="policy.maxIdleMinutes"> · 最长 {{ policy.maxIdleMinutes }} 分钟</span>
      </div>
      <p v-if="error" class="mt-2 text-destructive" role="alert">{{ error }}</p>
      <template v-else>
        <p v-if="loaded && !sessions.length" class="mt-2 text-muted-foreground">暂无保活会话，等待新的带会话 ID 的成功请求。</p>
        <div class="max-h-80 overflow-y-auto overscroll-contain">
          <div v-for="session in sessions" :key="session.id" class="mt-2 border-t border-border pt-2">
            <div class="flex items-start justify-between gap-3">
              <span class="break-all font-mono">{{ session.conversationId }}</span>
              <span class="shrink-0">{{ cacheKeepaliveStateLabels[session.state] }}</span>
            </div>
            <div v-if="session.model" class="break-all text-muted-foreground">{{ session.model }}</div>
            <dl class="mt-1 grid grid-cols-[auto_1fr] gap-x-3">
              <dt class="text-muted-foreground">最近记录上下文</dt>
              <dd>{{ session.snapshotAt ? formatDateTime(session.snapshotAt) : '尚未记录' }}</dd>
              <dt class="text-muted-foreground">下次心跳</dt>
              <dd>{{ nextHeartbeatText(session) }}</dd>
              <template v-if="session.lastAt">
                <dt class="text-muted-foreground">上次心跳</dt>
                <dd>{{ formatDateTime(session.lastAt) }}</dd>
              </template>
              <dt class="text-muted-foreground">本轮次数</dt>
              <dd>{{ session.attempts }}<span v-if="policy.maxAttempts"> / {{ policy.maxAttempts }}</span> 次</dd>
            </dl>
            <p v-if="session.reason" class="mt-1 text-warning">{{ session.reason }}</p>
          </div>
        </div>
      </template>
      <p class="mt-2 text-muted-foreground">查看期间每 10 秒刷新；各会话独立计时、计数。</p>
    </div>
  </UiTooltip>
</template>

<script setup lang="ts">
import { onUnmounted, ref } from 'vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiTooltip from '@/shared/ui/overlay/Tooltip.vue'
import { apiRequest } from '@/shared/services/http'
import { formatDateTime } from '@/shared/utils/datetime'
import { cacheKeepaliveStateLabels } from '@/features/channels/utils/cacheKeepalive'
import type { CacheKeepaliveConfig, CacheKeepaliveStatus } from '@/types/channel'

const props = defineProps<{ channelId: string; policy: CacheKeepaliveConfig }>()
const sessions = ref<CacheKeepaliveStatus[]>([])
const visible = ref(false)
const loading = ref(false)
const loaded = ref(false)
const error = ref('')
let controller: AbortController | undefined
let refreshTimer: ReturnType<typeof setTimeout> | undefined

function nextHeartbeatText(session: CacheKeepaliveStatus): string {
  if (session.nextHeartbeatAt) return formatDateTime(session.nextHeartbeatAt)
  if (session.state === 'stopped') return '已停止'
  if (session.state === 'sending') return '正在发送'
  if (session.state === 'active') return '真实请求结束后安排'
  if (session.state === 'waiting' || session.state === 'preparing') return '保存上下文后确认'
  return '未安排，等待检查期限或缓存窗口'
}

function stopRefresh() {
  clearTimeout(refreshTimer)
  refreshTimer = undefined
  controller?.abort()
  controller = undefined
  loading.value = false
}

function handleOpenChange(open: boolean) {
  visible.value = open
  stopRefresh()
  if (open) void refresh()
}

async function refresh() {
  if (!visible.value) return
  if (document.hidden) {
    refreshTimer = setTimeout(refresh, 10000)
    return
  }
  const current = new AbortController()
  controller = current
  loading.value = true
  error.value = ''
  let timedOut = false
  const timeout = setTimeout(() => { timedOut = true; current.abort() }, 15000)
  try {
    const result = await apiRequest<{ sessions: CacheKeepaliveStatus[] }>(
      `/channels/${encodeURIComponent(props.channelId)}/cache-keepalive`, { signal: current.signal }
    )
    if (controller !== current) return
    sessions.value = result.sessions
    loaded.value = true
  } catch (cause) {
    if (controller !== current) return
    error.value = timedOut ? '读取保活状态超时' : cause instanceof Error ? cause.message : '读取保活状态失败'
  } finally {
    clearTimeout(timeout)
    if (controller === current) {
      controller = undefined
      loading.value = false
      if (visible.value) refreshTimer = setTimeout(refresh, 10000)
    }
  }
}

onUnmounted(() => {
  visible.value = false
  stopRefresh()
})
</script>

<style scoped>
.keepalive-heart { animation: keepalive-beat 1.6s ease-in-out infinite; transform-origin: center; }
@keyframes keepalive-beat {
  0%, 40%, 100% { transform: scale(1); }
  12%, 28% { transform: scale(1.18); }
  20% { transform: scale(1.04); }
}
@media (prefers-reduced-motion: reduce) {
  .keepalive-heart { animation: none; }
}
</style>
