<template>
  <div class="rounded-control border border-border p-4">
    <div class="flex items-center justify-between gap-4">
      <div>
        <div class="text-base font-medium">缓存保活</div>
        <p class="mt-1 text-sm text-muted-foreground">有会话 ID 的对话空闲一分钟后准备保活，每次基于最后一次真实请求发送。</p>
      </div>
      <UiSwitch v-model="draft.enabled" aria-label="开启缓存保活" />
    </div>
    <div v-if="draft.enabled" class="mt-4 grid grid-cols-1 gap-4 md:grid-cols-2">
      <UiAlert type="info" class="md:col-span-2">
        心跳是付费请求，长时间保活不一定省钱。同一渠道的不同会话独立计时、计数；真实请求只重置所属会话的保活周期。配置修改或服务重启后，等待新的真实请求。
      </UiAlert>
      <p class="text-xs leading-5 text-muted-foreground md:col-span-2">{{ protocolHint }}</p>
      <UiInput
        v-model="draft.intervalSeconds" type="number" min="180" max="3540" step="1" label="心跳间隔（秒）"
        hint="最少 180 秒；默认 280 秒，即 4 分 40 秒。已知 5 分钟缓存窗口需小于 285 秒，更长间隔需要更长缓存窗口。" />
      <UiInput
        v-model="draft.maxAttempts" type="number" min="0" max="10000" step="1" label="最多发送次数"
        hint="每个会话每轮独立计算。0 表示只按持续时间限制；与时间上限任一达到即停止。Gemini 显式缓存按检查及续期轮次计算。" />
      <UiInput
        v-model="draft.maxIdleMinutes" type="number" min="0" max="1440" step="1" label="最长持续时间（分钟）"
        hint="从真实请求结束开始计算，心跳不会延长此期限；0 表示只按次数限制。" />
      <UiInput
        v-model="draft.minCacheHitPercent" type="number" min="1" max="100" step="1" label="最低命中保持比例（%）"
        hint="相对最新真实请求比较命中率和缓存 Token 数。例如设为 90%，原覆盖率为 60%，心跳需达到 54% 且保留至少 90% 的缓存量。" />
      <UiInput
        v-model="draft.maxOutputTokens" type="number" min="1" max="4096" step="1" label="单次输出上限（Token）"
        hint="包含思考消耗。原请求的思考预算若不小于此值，将跳过本轮保活。" />
      <UiTextarea
        v-model="draft.prompt" label="心跳内容" :rows="2" class="md:col-span-2"
        hint="最多 2048 字节。回复仅用于检查用量，不进入真实对话；可改为“仅回复 OK。”减少输出。" />
      <UiAlert v-if="validationError" type="error" class="md:col-span-2" role="alert">{{ validationError }}</UiAlert>
      <p class="text-xs leading-5 text-muted-foreground md:col-span-2">
        每个会话只保留最新候选，正文不落盘。全局最多 512 个会话、64 MiB 快照预算，原始正文最多 2 MiB。首次心跳等待真实请求结束并空闲满设定间隔，后续从心跳发送时刻计算；若已错过已知缓存窗口则停止。协议互转后沿用最终模型、凭据和缓存前缀。
      </p>
    </div>

    <div v-if="channelId" class="mt-4 border-t border-border pt-3">
      <div class="flex items-center justify-between gap-3">
        <span class="text-sm font-medium">当前保活状态</span>
        <UiButton type="button" size="small" variant="text" :loading="loading" @click="refresh">刷新</UiButton>
        <UiButton v-if="sessions.some(item => item.state !== 'stopped')" type="button" size="small" variant="text" :disabled="stopping" @click="stopSession()">停止全部</UiButton>
      </div>
      <UiAlert v-if="loadError" type="error" class="mt-2" role="alert">{{ loadError }}</UiAlert>
      <UiAlert v-if="stopError" type="error" class="mt-2" role="alert">{{ stopError }}</UiAlert>
      <UiAlert v-if="capacityRejections" type="warning" class="mt-2">
        本次服务运行期间，全局已有 {{ capacityRejections }} 次请求因会话容量已满而跳过建立保活候选。
      </UiAlert>
      <p v-if="!loadError && !loading && !sessions.length" class="mt-2 text-xs text-muted-foreground">暂无保活记录。保存配置后，需要新的带会话 ID 的成功请求。</p>
      <p v-if="sessions.length" class="mt-2 text-xs text-muted-foreground">心跳次数、下次时间和停止原因显示在这里；渠道请求日志只统计真实请求。</p>
      <div v-for="session in sessions" :key="session.id" class="mt-3 rounded-control border border-border p-3 text-xs">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <span class="break-all font-mono">{{ session.conversationId }}</span>
          <span>{{ stateNames[session.state] }}</span>
          <UiButton v-if="session.state !== 'stopped'" type="button" size="small" variant="text" :disabled="stopping" @click="stopSession(session.id)">停止</UiButton>
        </div>
        <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-muted-foreground">
          <span v-if="session.model">{{ session.model }}</span>
          <span>{{ session.entryProtocol }} → {{ session.upstreamProtocol }}</span>
          <span>{{ modeNames[session.cacheMode ?? 'prefix'] }}</span>
          <span>已处理 {{ session.attempts }} 次</span>
          <span>累计 {{ totalTokens(session).toLocaleString() }} Token</span>
          <span v-if="session.attempts && session.cacheMode !== 'explicit'">最近覆盖率 {{ session.cacheHitPercent.toFixed(1) }}%</span>
          <span v-if="session.cacheMode !== 'explicit'">原请求缓存占比 {{ session.baselineCacheHitPercent.toFixed(1) }}%</span>
          <span v-if="session.lastAt">最近处理 {{ formatDateTime(session.lastAt) }}</span>
          <span v-if="session.snapshotAt">最近记录上下文 {{ formatDateTime(session.snapshotAt) }}</span>
          <span v-if="session.expiresAt">资源到期 {{ formatDateTime(session.expiresAt) }}</span>
          <span v-if="session.nextHeartbeatAt">下次心跳 {{ formatDateTime(session.nextHeartbeatAt) }}</span>
          <span v-else-if="session.nextAt">下次检查 {{ formatDateTime(session.nextAt) }}</span>
        </div>
        <p v-if="session.reason" class="mt-2 text-muted-foreground">{{ session.reason }}</p>
      </div>
      <p v-if="sessions.length" class="mt-2 text-xs text-muted-foreground">每 10 秒更新。停止记录最多保留一小时；开启用量手账后，保活推理用量也会入账。停止不会撤销已经发出的请求或缩短已有缓存期限。</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import UiSwitch from '@/shared/ui/check/Switch.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiTextarea from '@/shared/ui/field/Textarea.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiButton from '@/shared/ui/button/Button.vue'
import { apiRequest } from '@/shared/services/http'
import { formatDateTime } from '@/shared/utils/datetime'
import type { CacheKeepaliveStatus } from '@/types/channel'
import { cacheKeepaliveStateLabels as stateNames, validateCacheKeepalive, type CacheKeepaliveDraft } from '@/features/channels/utils/cacheKeepalive'

const draft = defineModel<CacheKeepaliveDraft>({ required: true })
const props = defineProps<{ channelId?: string; serviceType: string }>()
const protocolHint = computed(() => {
  switch (props.serviceType) {
    case 'claude': return 'Claude 沿用原请求缓存断点和 TTL；兼容服务未给出缓存断点时，仅按已有命中反馈保活。思考参数保持不变，输出上限不足时停止。'
    case 'gemini': return 'Gemini 隐式缓存按实际命中反馈继续，服务端不保证固定 TTL。显式 cachedContent 使用资源接口续期，不发送心跳文本；续期会产生存储费用。'
    case 'responses': return 'Responses 保留固定 previous_response_id 和缓存键，心跳不保存为新对话历史。使用可变 conversation 或服务端工具时停止。隐式缓存命中与保留期由上游决定。'
    default: return 'Chat 沿用真实请求的缓存键、推理参数和前缀。隐式缓存的命中及保留期由上游决定；观察到命中下降便停止。'
  }
})
const validationError = computed(() => validateCacheKeepalive(draft.value))
const sessions = ref<CacheKeepaliveStatus[]>([])
const capacityRejections = ref(0)
const loading = ref(false)
const loadError = ref('')
const stopError = ref('')
const stopping = ref(false)
let controller: AbortController | undefined
let stopController: AbortController | undefined
let refreshTimer: ReturnType<typeof setInterval> | undefined
const modeNames = { prefix: '缓存断点', implicit: '隐式缓存', explicit: '缓存资源续期' }

function totalTokens(session: CacheKeepaliveStatus) {
  return session.inputTokens + session.cacheReadTokens + session.cacheCreationTokens + session.outputTokens
}

async function refresh() {
  controller?.abort()
  loading.value = false
  loadError.value = ''
  if (!props.channelId) return
  const current = new AbortController()
  controller = current
  loading.value = true
  loadError.value = ''
  try {
    const result = await apiRequest<{ sessions: CacheKeepaliveStatus[]; capacityRejections: number }>(`/channels/${encodeURIComponent(props.channelId)}/cache-keepalive`, { signal: current.signal })
    if (!current.signal.aborted) {
      sessions.value = result.sessions
      capacityRejections.value = result.capacityRejections
    }
  } catch (error) {
    if (!current.signal.aborted) loadError.value = error instanceof Error ? error.message : '读取保活状态失败'
  } finally {
    if (!current.signal.aborted) loading.value = false
  }
}

async function stopSession(sessionId = '') {
  if (!props.channelId || stopping.value) return
  const current = new AbortController()
  stopController = current
  stopping.value = true
  stopError.value = ''
  try {
    await apiRequest(`/channels/${encodeURIComponent(props.channelId)}/cache-keepalive/stop`, {
      method: 'POST', signal: current.signal, body: JSON.stringify({ sessionId })
    })
    if (!current.signal.aborted) await refresh()
  } catch (error) {
    if (!current.signal.aborted) stopError.value = error instanceof Error ? error.message : '停止保活失败'
  } finally {
    if (!current.signal.aborted) stopping.value = false
  }
}

watch(() => props.channelId, () => {
  stopController?.abort()
  stopping.value = false
  stopError.value = ''
  sessions.value = []
  capacityRejections.value = 0
  void refresh()
}, { immediate: true })
onMounted(() => {
  refreshTimer = setInterval(() => {
    if (document.visibilityState === 'visible' && !loading.value && !stopping.value) void refresh()
  }, 10000)
})
onUnmounted(() => {
  controller?.abort()
  stopController?.abort()
  if (refreshTimer) clearInterval(refreshTimer)
})
</script>
