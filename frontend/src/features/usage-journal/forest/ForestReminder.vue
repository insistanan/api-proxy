<template>
  <span v-if="store.syncError || store.newsError" class="sr-only" role="status">
    {{ store.syncError || store.newsError }}
  </span>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, watch } from 'vue'
import { useToast } from '@/shared/composables/useToast'
import { useNotificationStore } from '@/shared/stores/notifications'
import { useUsageJournal } from '@/features/usage-journal/store'
import type { ForestEvent } from '@/types/forest'
import { effectItems, forestApi, useForest } from '@/features/usage-journal/forest/store'

const store = useForest()
const journal = useUsageJournal()
const { showToast } = useToast()
const notifications = useNotificationStore()
const shown = new Set<string>()
let active = true
let inFlight = false
let wake = 0
const idleInterval = 30_000
let moreNews = false
let visitPending = true
const cursorKey = 'api-proxy-forest-news'

function hasDamage(event: ForestEvent) {
  return (
    event.effects.damaged > 0 ||
    event.effects.destroyed > 0 ||
    (event.id.startsWith('absence:') && effectItems(event.effects).some(item => item.loss))
  )
}

async function syncNews() {
  const raw = window.localStorage.getItem(cursorKey)
  const saved: { instance: string; cursor: number } = raw ? JSON.parse(raw) : { instance: '', cursor: 0 }
  if (typeof saved.instance !== 'string' || !Number.isSafeInteger(saved.cursor) || saved.cursor < 0)
    throw new Error('森林消息的本地阅读位置无效')
  let feed = await forestApi.events(saved.cursor)
  if (feed.instance !== saved.instance && saved.cursor) feed = await forestApi.events()
  if (!active) return
  for (const event of feed.events) {
    const achievement = event.id.startsWith('achievement:')
    const effects = effectItems(event.effects)
    const loss = hasDamage(event)
    const summary = effects.map(item => item.text).join(' · ')
    notifications.addMessage(
      `${event.title}\n${event.story}${summary ? `\n${summary}` : ''}`,
      loss ? 'warning' : achievement || effects.length ? 'success' : 'info',
      {
        source: achievement ? '森林成就' : '林间来信',
        level: loss ? 'warning' : 'notice',
        key: `forest:${feed.instance}:${event.id}`,
        createdAt: event.at,
        route: `/usage-journal?tab=${achievement ? 'achievements' : 'forest'}&event=${encodeURIComponent(event.id)}`
      }
    )
  }
  if (notifications.storageError) throw new Error(notifications.storageError)
  if (feed.events.length) await store.load()
  window.localStorage.setItem(cursorKey, JSON.stringify({ instance: feed.instance, cursor: feed.cursor }))
  moreNews = feed.more
  if (feed.events.length) {
    const achievements = feed.events.filter(event => event.id.startsWith('achievement:'))
    const losses = feed.events.filter(hasDamage)
    const message = losses.length
      ? '林间有树木或材料受损，去看看来信'
      : achievements.length === 1
        ? achievements[0].title
        : achievements.length
          ? `解锁了 ${achievements.length} 枚新徽章`
          : `收到 ${feed.events.length} 封林间来信`
    showToast(message, losses.length ? 'warning' : achievements.length ? 'success' : 'info')
  }
}

function schedule() {
  window.clearTimeout(wake)
  if (!active || !journal.status?.enabledAt) return
  const t = store.timer
  const now = t ? t.serverNow + Math.max(0, Date.now() - store.receivedAt) : 0
  const next = [t?.mode === 'focus' || t?.mode === 'rest' ? t.due : 0, t?.waitDue ?? 0].filter(due => due > now)
  const delay = next.length ? Math.max(2000, Math.min(...next) - now) : idleInterval
  wake = window.setTimeout(poll, moreNews ? 2000 : Math.min(idleInterval, delay))
}

async function notify(key: string, message: string) {
  if (shown.has(key)) return
  const notifyOnce = () => {
    if (!active) return
    const record = 'api-proxy-forest-reminders'
    const keys = (window.localStorage.getItem(record) ?? '').split(',')
    if (keys.includes(key)) {
      shown.add(key)
      return
    }
    notifications.addMessage(message, 'info', {
      source: '森林时钟',
      key,
      level: 'notice',
      route: '/usage-journal?tab=forest&timer=1'
    })
    if (notifications.storageError) throw new Error(notifications.storageError)
    window.localStorage.setItem(record, [...keys.filter(Boolean).slice(-31), key].join(','))
    shown.add(key)
    showToast(message, 'info')
  }
  if (window.navigator.locks) await window.navigator.locks.request(key, notifyOnce)
  else notifyOnce()
}
async function poll() {
  if (!active || inFlight || !journal.status?.enabledAt) return
  inFlight = true
  try {
    if (visitPending) {
      await forestApi.visit()
      visitPending = false
    }
    await store.refreshTimer()
    try {
      if (window.navigator.locks) await window.navigator.locks.request('api-proxy-forest-news', syncNews)
      else await syncNews()
      store.newsError = ''
    } catch (cause) {
      store.newsError = `林间来信暂未同步。${cause instanceof Error ? cause.message : String(cause)}`
    }
    const t = store.timer
    if (!active || !t) return
    if (t.due && t.due <= t.serverNow) {
      if (t.mode === 'focus' || t.mode === 'focus-due') await notify(`focus:${t.due}`, '这一轮专注已到期')
      if (t.mode === 'rest') await notify(`rest:${t.due}`, '休息时间已到，可以确认完成')
    }
    if (t.waitDue && t.waitDue <= t.serverNow) await notify(`wait:${t.waitDue}`, '到回看 Agent 任务的时间了')
  } catch (cause) {
    if (!store.syncError)
      store.newsError = `森林提醒暂未送达。${cause instanceof Error ? cause.message : String(cause)}`
  } finally {
    inFlight = false
    schedule()
  }
}

async function resume() {
  visitPending = true
  if (!journal.status?.enabledAt) {
    try {
      await journal.loadStatus()
    } catch (cause) {
      store.newsError = `用量状态读取失败。${cause instanceof Error ? cause.message : String(cause)}`
      return
    }
  }
  if (!journal.status?.enabledAt) return
  await poll()
}

watch(
  () => journal.status?.enabledAt,
  enabled => {
    if (enabled) void poll()
    else window.clearTimeout(wake)
  }
)
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
