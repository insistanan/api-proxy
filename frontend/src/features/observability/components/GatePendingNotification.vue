<template>
  <teleport to="body">
    <transition-group name="gate-notif" tag="div" class="gate-notif-stack" aria-live="assertive">
      <div v-if="pollError || decisionError" key="gate-error" class="gate-notif" role="status">
        {{ decisionError || pollError }}
      </div>
      <div
        v-for="item in pendingItems"
        :key="item.id"
        class="gate-notif"
        :class="item.aiVerdict === 'allow' ? 'gate-notif--allow' : 'gate-notif--block'"
        role="alert"
      >
        <div class="gate-notif__head">
          <UiIcon size="18" :color="item.aiVerdict === 'allow' ? 'warning' : 'error'">
            {{ item.aiVerdict === 'allow' ? 'mdi-shield-check' : 'mdi-shield-alert' }}
          </UiIcon>
          <span class="gate-notif__title">
            {{ item.aiVerdict === 'allow' ? '危险命令已放行（可否决）' : '危险命令待确认' }}
          </span>
          <span class="gate-notif__meta">{{ item.apiType }} · {{ item.rule }}</span>
        </div>
        <pre class="gate-notif__command">{{ item.command }}</pre>
        <details v-if="item.executionContext">
          <summary>执行参数{{ item.toolName ? ` · ${item.toolName}` : '' }}</summary>
          <pre class="gate-notif__command">{{ item.executionContext }}</pre>
        </details>
        <div class="gate-notif__reason">决定仅适用于本会话中相同命令、工具及执行参数。已交付的内容无法撤回。</div>
        <div v-if="item.aiReason" class="gate-notif__reason">AI：{{ item.aiReason }}</div>
        <div class="gate-notif__actions">
          <template v-if="item.aiVerdict === 'block'">
            <UiButton
              size="small"
              variant="flat"
              color="success"
              :loading="decidingId === item.id"
              :disabled="decidingId !== null"
              @click="decide(item, 'allow')"
            >
              放行本命令
            </UiButton>
            <UiButton
              size="small"
              variant="tonal"
              color="error"
              :loading="decidingId === item.id"
              :disabled="decidingId !== null"
              @click="decide(item, 'block')"
            >
              拦截
            </UiButton>
          </template>
          <template v-else>
            <UiButton
              size="small"
              variant="flat"
              color="error"
              :loading="decidingId === item.id"
              :disabled="decidingId !== null"
              @click="decide(item, 'block')"
            >
              否决并拦截
            </UiButton>
          </template>
        </div>
        <UiProgress
          :model-value="progressOf(item)"
          :color="item.aiVerdict === 'allow' ? 'warning' : 'error'"
          height="3"
          rounded
          class="gate-notif__progress"
        />
      </div>
    </transition-group>
  </teleport>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiProgress from '@/shared/ui/feedback/Progress.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api, type GatePending } from '@/api'
import { useAuthStore } from '@/shared/stores/auth'

// 危险命令 AI 闸门的全局通知：轮询待确认列表，新命中时声音 + 气泡 + 浏览器系统通知。
// 缩短空闲轮询间隔，让短确认窗口尽早展示；每次只保留一个在途轮询。

const POLL_ACTIVE_MS = 500
const POLL_IDLE_MS = 1000
// 已知 pending 的 ID 集合，用于识别"新出现"的条目（触发声音/系统通知）。
const knownIds = new Set<number>()

const authStore = useAuthStore()
const pendingItems = ref<GatePending[]>([])
const decidingId = ref<number | null>(null)
const now = ref(Date.now())
const pollError = ref('')
const decisionError = ref('')
let stopped = false
let pollInFlight = false
let pendingRevision = 0
let pollController: AbortController | null = null
let decisionController: AbortController | null = null

let pollTimer: ReturnType<typeof setTimeout> | null = null
let progressTimer: ReturnType<typeof setInterval> | null = null

const isAuthenticated = computed(() => authStore.isAuthenticated)

const poll = async () => {
  if (stopped || pollInFlight) return
  if (!isAuthenticated.value) {
    pendingItems.value = []
    knownIds.clear()
    pollError.value = ''
    schedule(POLL_IDLE_MS)
    return
  }
  pollInFlight = true
  const revision = pendingRevision
  const controller = new AbortController()
  pollController = controller
  const timeout = setTimeout(() => controller.abort(), 4000)
  try {
    const response = await api.getGatePending(controller.signal)
    if (stopped || revision !== pendingRevision) return
    if (!Array.isArray(response.pending)) throw new Error('命令审核通知返回格式无效')
    const fresh = response.pending
    const arrivals = fresh.filter(item => !knownIds.has(item.id))
    knownIds.clear()
    for (const item of fresh) knownIds.add(item.id)
    if (arrivals.length > 0) {
      playAlertSound()
      for (const item of arrivals.slice(0, 3)) desktopNotify(item)
    }
    pendingItems.value = fresh
    pollError.value = ''
  } catch (err) {
    if (!stopped && revision === pendingRevision) {
      pollError.value = controller.signal.aborted
        ? '命令审核通知读取超时，正在重试'
        : err instanceof Error
          ? err.message
          : '命令审核通知读取失败'
    }
  } finally {
    clearTimeout(timeout)
    pollInFlight = false
    if (pollController === controller) pollController = null
    schedule(pendingItems.value.length > 0 ? POLL_ACTIVE_MS : POLL_IDLE_MS)
  }
}

const schedule = (delay: number) => {
  if (stopped) return
  if (pollTimer !== null) clearTimeout(pollTimer)
  pollTimer = setTimeout(poll, delay)
}

const decide = async (item: GatePending, decision: 'allow' | 'block') => {
  if (decidingId.value !== null) return
  decidingId.value = item.id
  decisionError.value = ''
  pendingRevision++
  const controller = new AbortController()
  decisionController = controller
  const timeout = setTimeout(() => controller.abort(), 4000)
  try {
    await api.decideGatePending(item.id, decision, controller.signal)
    if (stopped) return
    pendingItems.value = pendingItems.value.filter(pending => pending.id !== item.id)
    knownIds.delete(item.id)
  } catch (err) {
    if (!stopped) {
      decisionError.value = controller.signal.aborted
        ? '命令决定提交超时，正在刷新确认状态'
        : err instanceof Error
          ? err.message
          : '提交命令决定失败，请重试'
    }
  } finally {
    clearTimeout(timeout)
    if (decisionController === controller) decisionController = null
    pendingRevision++
    decidingId.value = null
    schedule(0)
  }
}

const progressOf = (item: GatePending): number => {
  const expires = new Date(item.expiresAt).getTime()
  // 后端不暴露窗口起点时长，用 createdAt 与 expiresAt 反推总时长。
  const created = new Date(item.createdAt).getTime()
  const total = Math.max(expires - created, 1)
  const remain = expires - now.value
  return Math.max(0, Math.min(100, (remain / total) * 100))
}

// ----- 声音（Web Audio 合成三连音，无音频文件依赖）-----
let audioCtx: AudioContext | null = null

const playAlertSound = () => {
  try {
    if (!audioCtx) audioCtx = new AudioContext()
    if (audioCtx.state === 'suspended') {
      void audioCtx.resume()
    }
    const start = audioCtx.currentTime
    // 三连下行短音，警示感强且不刺耳。
    const tones = [880, 660, 880]
    tones.forEach((frequency, index) => {
      const oscillator = audioCtx!.createOscillator()
      const gain = audioCtx!.createGain()
      oscillator.type = 'sine'
      oscillator.frequency.value = frequency
      const toneStart = start + index * 0.18
      gain.gain.setValueAtTime(0.0001, toneStart)
      gain.gain.exponentialRampToValueAtTime(0.12, toneStart + 0.02)
      gain.gain.exponentialRampToValueAtTime(0.0001, toneStart + 0.16)
      oscillator.connect(gain).connect(audioCtx!.destination)
      oscillator.onended = () => {
        oscillator.disconnect()
        gain.disconnect()
      }
      oscillator.start(toneStart)
      oscillator.stop(toneStart + 0.18)
    })
  } catch {
    // 浏览器限制自动播放等场景静默跳过：气泡本身已是可见提醒。
  }
}

// ----- 浏览器系统通知（跨 Windows/macOS/Linux，需用户授权，默认关）-----
const desktopNotify = (item: GatePending) => {
  if (typeof Notification === 'undefined') return
  if (localStorage.getItem('gate-desktop-notify') !== 'enabled') return
  if (Notification.permission !== 'granted') return
  try {
    const title = item.aiVerdict === 'allow' ? '危险命令已放行，可否决' : '危险命令待确认'
    const body = `${item.rule}: ${item.command.slice(0, 120)}`
    const notification = new Notification(title, { body, tag: `gate-${item.id}` })
    notification.onclick = () => {
      window.focus()
      notification.close()
    }
  } catch {
    // 通知 API 不可用静默跳过。
  }
}

const refreshClock = () => {
  now.value = Date.now()
}

onMounted(() => {
  schedule(0)
  progressTimer = setInterval(refreshClock, 200)
})

onBeforeUnmount(() => {
  stopped = true
  pollController?.abort()
  decisionController?.abort()
  knownIds.clear()
  if (pollTimer !== null) clearTimeout(pollTimer)
  if (progressTimer !== null) clearInterval(progressTimer)
  if (audioCtx) {
    void audioCtx.close().catch(() => undefined)
    audioCtx = null
  }
})
</script>

<style scoped>
@reference "@/styles/index.css";

.gate-notif-stack {
  @apply fixed top-18 right-5 [z-index:2400] flex flex-col gap-3 [width:min(420px,_calc(100vw_-_40px))] pointer-events-none [max-height:calc(100vh_-_88px)] overflow-y-auto;
}

.gate-notif {
  @apply pointer-events-auto rounded-control [padding:14px_16px_12px] [border:1px_solid_var(--border)] [background:var(--surface)] shadow-popover;
}

/* AI 判危险（等待放行）用 error 色调；AI 判安全（已放行可否决）用 warning 色调 */
.gate-notif--block {
  @apply [border-left:4px_solid_var(--destructive)];
}

.gate-notif--allow {
  @apply [border-left:4px_solid_var(--warning)];
}

.gate-notif__head {
  @apply flex items-center gap-2;
}

.gate-notif__title {
  @apply font-semibold [font-size:0.95rem];
}

.gate-notif__meta {
  @apply ml-auto [font-size:0.75rem] opacity-72 whitespace-nowrap;
}

.gate-notif__command {
  @apply [margin:10px_0_8px] [padding:8px_10px] rounded-badge [background:color-mix(in_srgb,_var(--surface-variant)_50%,_transparent)] [font-family:'Cascadia_Code',_'JetBrains_Mono',_Consolas,_monospace] [font-size:0.8rem] whitespace-pre-wrap [word-break:break-all] max-h-30 overflow-y-auto;
}

.gate-notif__reason {
  @apply [font-size:0.78rem] opacity-82 mb-2;
}

.gate-notif__actions {
  @apply flex gap-2 flex-wrap;
}

.gate-notif__progress {
  @apply mt-2.5;
}

/* 进出动画 */
.gate-notif-enter-active,
.gate-notif-leave-active {
  @apply [transition:all_220ms_ease];
}

.gate-notif-enter-from,
.gate-notif-leave-to {
  @apply opacity-0 [transform:translateX(24px)];
}

@media (max-width: 600px) {
  .gate-notif-stack {
    @apply top-16 right-2 left-2 w-auto;
  }
}
</style>
