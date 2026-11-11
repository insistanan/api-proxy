<template>
  <div class="version-anchor">
    <button
      v-if="versionInfo.currentVersion"
      type="button"
      class="version-badge"
      :class="{ 'version-clickable': canOpenRelease, 'version-update': versionInfo.status === 'update-available' }"
      :aria-label="`当前版本 ${versionInfo.currentVersion}，查看版本信息`"
      @click="openRelease"
    >
      <v-icon v-if="statusIcon" size="14" class="mr-1" :color="statusIcon.color">{{ statusIcon.icon }}</v-icon>
      <span>{{ versionInfo.currentVersion }}</span>
      <template v-if="versionInfo.status === 'update-available' && versionInfo.latestVersion">
        <span class="version-latest mx-1">→</span>
        <span class="version-latest">{{ versionInfo.latestVersion }}</span>
      </template>
    </button>

    <transition name="update-bubble">
      <div v-if="bubbleVisible && versionInfo.latestVersion" class="update-bubble" role="status">
        <button type="button" class="update-bubble__close" aria-label="关闭提醒" @click="hideBubble">
          <v-icon size="14">mdi-close</v-icon>
        </button>
        <div class="update-bubble__row update-bubble__title">
          <v-icon size="15" color="warning">mdi-arrow-up-bold-circle-outline</v-icon>
          发现新版本
        </div>
        <div class="update-bubble__row update-bubble__versions">
          <span>{{ versionInfo.currentVersion }}</span>
          <v-icon size="13">mdi-arrow-right</v-icon>
          <span class="version-latest">{{ versionInfo.latestVersion }}</span>
        </div>
        <v-btn size="x-small" variant="flat" color="primary" @click="openRelease">查看更新</v-btn>
      </div>
    </transition>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { fetchHealth } from '@/services/api'
import { versionService } from '@/services/version'
import { useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()
const { versionInfo } = storeToRefs(systemStore)

const statusIcon = computed(() => {
  switch (versionInfo.value.status) {
    case 'checking': return { icon: 'mdi-clock-outline', color: undefined }
    case 'latest': return { icon: 'mdi-check-circle', color: 'success' }
    case 'update-available': return { icon: 'mdi-alert', color: 'warning' }
    default: return null
  }
})

const canOpenRelease = computed(() =>
  (versionInfo.value.status === 'update-available' || versionInfo.value.status === 'latest') && !!versionInfo.value.releaseUrl
)

const openRelease = () => {
  if (canOpenRelease.value) window.open(versionInfo.value.releaseUrl!, '_blank', 'noopener,noreferrer')
}

// 版本检查独立于认证，失败只影响徽标状态，不打断页面。
const checkVersion = async () => {
  try {
    const currentVersion = (await fetchHealth()).version?.version || ''
    if (!currentVersion) {
      systemStore.setVersionInfo({ ...versionInfo.value, status: 'error' })
      return
    }
    versionService.setCurrentVersion(currentVersion)
    systemStore.setCurrentVersion(currentVersion)
    systemStore.setVersionInfo(await versionService.checkForUpdates())
  } catch (error) {
    console.warn('Version check failed:', error)
    systemStore.setVersionInfo({ ...versionInfo.value, status: 'error' })
  }
}

// 新版本提醒气泡（交互约定经用户确认）：检测到更新时显示 5 秒自动收起；用户始终未升级时
// 每隔 1 小时再次弹出。手动关闭或点击“查看更新”只结束当次，不影响下一小时的提醒。
const BUBBLE_VISIBLE_MS = 5000
const BUBBLE_REMIND_INTERVAL_MS = 60 * 60 * 1000

const bubbleVisible = ref(false)
let hideTimer: ReturnType<typeof setTimeout> | undefined
let remindTimer: ReturnType<typeof setInterval> | undefined
let lastShownAt = 0

const hideBubble = () => {
  bubbleVisible.value = false
  clearTimeout(hideTimer)
}

const maybeShowBubble = () => {
  if (versionInfo.value.status !== 'update-available') return
  if (Date.now() - lastShownAt < BUBBLE_REMIND_INTERVAL_MS) return
  lastShownAt = Date.now()
  bubbleVisible.value = true
  clearTimeout(hideTimer)
  hideTimer = setTimeout(hideBubble, BUBBLE_VISIBLE_MS)
}

watch(() => versionInfo.value.status, maybeShowBubble)

onMounted(() => {
  void checkVersion()
  maybeShowBubble()
  remindTimer = setInterval(maybeShowBubble, BUBBLE_REMIND_INTERVAL_MS)
})
onBeforeUnmount(() => {
  clearInterval(remindTimer)
  clearTimeout(hideTimer)
})
</script>

<style scoped>
.version-anchor { position: relative; flex-shrink: 0; margin-right: 6px; }
.version-badge { display: flex; align-items: center; padding: 4px 8px; font-size: 0.75rem; font-variant-numeric: tabular-nums; border-radius: 6px; border: 1px solid rgba(var(--v-theme-outline), 0.25); background: rgba(var(--v-theme-surface-variant), 0.3); cursor: default; }
.version-clickable { cursor: pointer; }
.version-clickable:hover { border-color: rgb(var(--v-theme-primary)); }
.version-update { border-color: rgba(var(--v-theme-warning), 0.5); }
.version-latest { color: rgb(var(--v-theme-warning)); }
.update-bubble { position: absolute; top: calc(100% + 12px); right: 0; z-index: 1200; width: 220px; padding: 16px; border-radius: 12px; background: rgb(var(--v-theme-surface)); border: 1px solid rgba(var(--v-theme-outline), 0.3); box-shadow: var(--shadow-2); }
.update-bubble__close { position: absolute; top: 4px; right: 4px; padding: 6px; border-radius: 6px; }
.update-bubble__close:hover { background: rgba(var(--v-theme-on-surface), 0.06); }
.update-bubble__row { display: flex; align-items: center; gap: 6px; font-size: 0.8125rem; margin-bottom: 10px; }
.update-bubble__title { font-weight: 650; }
.update-bubble__versions { font-family: var(--font-mono); }
.update-bubble-enter-active, .update-bubble-leave-active { transition: opacity 160ms ease, transform 160ms ease; }
.update-bubble-enter-from, .update-bubble-leave-to { opacity: 0; transform: translateY(-4px); }
@media (max-width: 600px) { .version-anchor { display: none; } }
</style>
