<template>
  <span v-if="store.syncError && (store.timer?.mode || store.timer?.waitStarted)" class="sr-only" role="status">{{ store.syncError }}</span>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, watch } from 'vue'
import { useToast } from '@/shared/composables/useToast'
import { useUsageJournal } from '@/features/usage-journal/store'
import { useForest } from './store'

const store = useForest()
const journal = useUsageJournal()
const { showToast } = useToast()
const shown = new Set<string>()
let active = true
let inFlight = false
let wake = 0
const idleInterval = 60_000

function schedule() {
  window.clearTimeout(wake)
  if (!active || !journal.status?.enabledAt) return
  const t = store.timer
  const now = t ? t.serverNow + Math.max(0, Date.now() - store.receivedAt) : 0
  const next = [t?.mode === 'focus' || t?.mode === 'rest' ? t.due : 0, t?.waitDue ?? 0]
    .filter(due => due > now)
  const delay = next.length ? Math.max(2000, Math.min(...next) - now) : idleInterval
  wake = window.setTimeout(poll, Math.min(idleInterval, delay))
}

async function notify(key: string, message: string) {
  if (shown.has(key)) return
  shown.add(key)
  const notifyOnce = () => {
    if (!active) return
    const record = 'api-proxy-forest-reminders'
    try {
      const keys = (window.localStorage.getItem(record) ?? '').split(',')
      if (keys.includes(key)) return
      window.localStorage.setItem(record, [...keys.filter(Boolean).slice(-31), key].join(','))
    } catch {
      // 当前页面仍可提示，只有跨页去重依赖浏览器存储。
    }
    showToast(message, 'info', { source: '个人森林', key, level: 'notice' })
    if (window.Notification?.permission === 'granted') {
      try { new window.Notification(message, { tag: key }) }
      catch { /* 站内提醒已投递。 */ }
    }
  }
  if (window.navigator.locks) await window.navigator.locks.request(key, notifyOnce)
  else notifyOnce()
}
async function poll() {
  if (!active || inFlight || !journal.status?.enabledAt) return
  inFlight = true
  try {
    await store.refreshTimer()
    const t = store.timer
    if (!active || !t) return
    if (t.due && t.due <= t.serverNow) {
      if (t.mode === 'focus' || t.mode === 'focus-due') await notify(`focus:${t.due}`, '这一轮专注已到期')
      if (t.mode === 'rest') await notify(`rest:${t.due}`, '休息时间已到，可以确认完成')
    }
    if (t.waitDue && t.waitDue <= t.serverNow) await notify(`wait:${t.waitDue}`, '到回看 Agent 任务的时间了')
  } catch {
    // 计时同步错误由 Store 显示，下次轮询会重新读取。
  } finally {
    inFlight = false
    schedule()
  }
}

async function resume() {
  if (!journal.status?.enabledAt) {
    try { await journal.loadStatus() }
    catch { return } // 用量手账页面负责显示账本不可用。
  }
  await poll()
}

watch(() => journal.status?.enabledAt, enabled => {
  if (enabled) void poll()
  else window.clearTimeout(wake)
})
watch(() => store.timer?.revision, schedule)
onMounted(() => {
  void resume()
  window.addEventListener('focus', resume)
})
onUnmounted(() => {
  active = false
  window.clearTimeout(wake)
  window.removeEventListener('focus', resume)
})
</script>
