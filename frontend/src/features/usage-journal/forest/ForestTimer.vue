<template>
  <section class="panel-surface p-4" aria-labelledby="forest-timer-title">
    <h3 id="forest-timer-title" class="font-semibold">专注与交接</h3>
    <p v-if="syncError" role="status" class="mt-2 text-xs text-destructive">{{ syncError }}</p>
    <div v-if="timer" class="mt-3 space-y-3">
      <div class="flex flex-wrap items-baseline justify-between gap-2">
        <span class="text-sm text-muted-foreground">{{ state }}</span>
        <strong v-if="timer.mode && timer.mode !== 'focus-due'" class="font-mono text-2xl tabular-nums">{{ clock(remaining) }}</strong>
      </div>
      <div v-if="timer.waitStarted" class="rounded-control bg-muted px-3 py-2 text-sm">
        <p>Agent 等待 {{ clock(now - timer.waitStarted) }}</p>
        <p v-if="timer.waitDue" class="text-xs text-muted-foreground">{{ timer.waitDue <= now ? '已到回看时间' : `距回看还有 ${clock(timer.waitDue - now)}` }}</p>
        <UiButton class="mt-1" variant="text" size="small" :disabled="busy" @click="send('end-wait')">结束等待</UiButton>
      </div>
      <div v-if="timer.mode !== 'rest'" class="flex flex-wrap gap-2">
        <UiButton v-if="timer.mode === 'paused'" color="primary" size="small" :disabled="busy" @click="send('resume')">继续专注</UiButton>
        <UiButton v-else-if="timer.mode !== 'focus' || expired" color="primary" size="small" :disabled="busy" @click="send('focus', focusMinutes)">专注 {{ focusMinutes }} 分钟</UiButton>
        <UiButton v-if="timer.mode === 'focus' && !expired" variant="outlined" size="small" :disabled="busy" @click="send('pause')">暂停</UiButton>
        <UiButton variant="outlined" size="small" :disabled="busy" @click="send('rest', restMinutes)">休息 {{ restMinutes }} 分钟</UiButton>
      </div>
      <div class="flex flex-wrap gap-2">
        <UiButton v-if="!timer.waitStarted" variant="text" size="small" :disabled="busy" @click="send('handoff', reviewMinutes)">交给 Agent</UiButton>
        <UiButton v-if="timer.mode === 'rest' && expired" color="primary" size="small" :disabled="busy" @click="send('confirm-rest')">确认完成休息</UiButton>
        <UiButton v-if="timer.mode" variant="text" size="small" :disabled="busy" @click="send('end')">{{ timer.mode === 'rest' ? '取消休息' : '结束专注' }}</UiButton>
      </div>
      <details class="text-sm">
        <summary class="cursor-pointer text-muted-foreground">时长与提醒</summary>
        <div class="mt-2 flex gap-2">
          <UiButton variant="outlined" size="small" @click="preset(25, 5)">25 / 5</UiButton>
          <UiButton variant="outlined" size="small" @click="preset(50, 10)">50 / 10</UiButton>
        </div>
        <div class="mt-2 grid grid-cols-3 gap-2">
          <label class="text-xs">专注分钟<input v-model.number="focusMinutes" type="number" class="field-surface mt-1 w-full p-1.5" min="1" max="240" /></label>
          <label class="text-xs">休息分钟<input v-model.number="restMinutes" type="number" class="field-surface mt-1 w-full p-1.5" min="1" max="240" /></label>
          <label class="text-xs">回看分钟<input v-model.number="reviewMinutes" type="number" class="field-surface mt-1 w-full p-1.5" min="0" max="240" /></label>
        </div>
        <p class="mt-1 text-xs text-muted-foreground">回看设为 0 表示只记录等待。</p>
        <UiButton class="mt-2" variant="text" size="small" @click="enableDesktop">允许桌面提醒</UiButton>
        <p v-if="desktop" class="text-xs text-muted-foreground">{{ desktop }}</p>
      </details>
      <p v-if="error" class="text-sm text-destructive" role="alert">{{ error }}</p>
      <p v-if="preferenceError" class="text-xs text-destructive" role="status">{{ preferenceError }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import UiButton from '@/shared/ui/button/Button.vue'
import { ApiError } from '@/shared/services/http'
import { useForest } from './store'

const forest = useForest()
const { timer, receivedAt, syncError } = storeToRefs(forest)
const emit = defineEmits<{ changed: [] }>()
const tick = ref(Date.now())
const busy = ref(false)
const error = ref('')
const preferenceError = ref('')
const desktop = ref('')
const focusMinutes = ref(50)
const restMinutes = ref(10)
const reviewMinutes = ref(15)
const preferenceKey = 'api-proxy-forest-timer-preferences'
let interval: number
const now = computed(() => timer.value ? timer.value.serverNow + tick.value - receivedAt.value : tick.value)
const remaining = computed(() => timer.value?.mode === 'paused' ? timer.value.left : Math.max(0, (timer.value?.due ?? 0) - now.value))
const expired = computed(() => remaining.value === 0)
const state = computed(() => {
  switch (timer.value?.mode) {
    case 'focus': return expired.value ? '本轮专注到期' : '专注中'
    case 'focus-due': return '本轮专注已完成'
    case 'paused': return '专注已暂停'
    case 'rest': return expired.value ? '等待确认休息' : '休息中'
    default: return '当前未计时'
  }
})

function clock(ms: number) {
  const seconds = Math.max(0, Math.ceil(ms / 1000))
  return `${Math.floor(seconds / 60).toString().padStart(2, '0')}:${(seconds % 60).toString().padStart(2, '0')}`
}
function preset(focus: number, rest: number) { focusMinutes.value = focus; restMinutes.value = rest }

async function send(action: string, minutes = 0) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    if (!Number.isInteger(minutes) || minutes < (action === 'focus' || action === 'rest' ? 1 : 0) || minutes > 240) throw new Error('请输入有效的计时分钟数')
    await forest.command(action, minutes)
    emit('changed')
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
    if (cause instanceof ApiError && cause.status === 409) {
      try { await forest.refreshTimer() }
      catch (refreshError) {
        error.value += ` 读取最新计时失败。 ${refreshError instanceof Error ? refreshError.message : String(refreshError)}`
      }
    }
  } finally {
    tick.value = Date.now()
    busy.value = false
  }
}
async function enableDesktop() {
  const NotificationAPI = window.Notification
  if (!NotificationAPI) { desktop.value = '当前浏览器不支持桌面提醒'; return }
  try {
    const permission = await NotificationAPI.requestPermission()
    desktop.value = permission === 'granted' ? '已允许。关闭所有管理台页面后，打开时可查看到期状态。' : '桌面提醒未获授权，仍会使用站内提醒。'
  } catch (cause) {
    desktop.value = cause instanceof Error ? cause.message : String(cause)
  }
}
function loadPreferences() {
  try {
    const raw = window.localStorage.getItem(preferenceKey)
    if (!raw) return
    const { focus, rest, review } = JSON.parse(raw)
    if (!validMinutes(focus, 1) || !validMinutes(rest, 1) || !validMinutes(review, 0)) throw new Error('保存的时长无效')
    preset(focus, rest)
    reviewMinutes.value = review
  } catch (cause) {
    preferenceError.value = `读取时长偏好失败。 ${cause instanceof Error ? cause.message : String(cause)}`
  }
}

function validMinutes(value: unknown, minimum: number): value is number {
  return typeof value === 'number' && Number.isInteger(value) && value >= minimum && value <= 240
}

watch([focusMinutes, restMinutes, reviewMinutes], ([focus, rest, review]) => {
  if (!validMinutes(focus, 1) || !validMinutes(rest, 1) || !validMinutes(review, 0)) return
  try {
    window.localStorage.setItem(preferenceKey, JSON.stringify({ focus, rest, review }))
    preferenceError.value = ''
  } catch (cause) {
    preferenceError.value = `保存时长偏好失败。 ${cause instanceof Error ? cause.message : String(cause)}`
  }
})
onMounted(() => {
  loadPreferences()
  interval = window.setInterval(() => { tick.value = Date.now() }, 500)
})
onUnmounted(() => window.clearInterval(interval))
</script>
