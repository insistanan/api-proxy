<template>
  <div class="version-anchor">
    <button
      v-if="versionInfo.currentVersion"
      type="button"
      class="version-badge"
      data-header-hit
      :class="{ 'version-clickable': canOpenRelease || versionInfo.status === 'error', 'version-update': versionInfo.status === 'update-available' }"
      :aria-label="downloadHint"
      :title="downloadHint"
      @click="downloadLatest"
    >
      <UiIcon v-if="statusIcon" size="14" class="mr-1" :color="statusIcon.color">{{ statusIcon.icon }}</UiIcon>
      <span>{{ versionInfo.currentVersion }}</span>
      <template v-if="versionInfo.status === 'update-available' && versionInfo.latestVersion">
        <span class="version-latest mx-1">→</span>
        <span class="version-latest">{{ versionInfo.latestVersion }}</span>
      </template>
    </button>

    <transition name="update-bubble">
      <div v-if="bubbleVisible && versionInfo.latestVersion" class="update-bubble" role="status">
        <button type="button" class="update-bubble__close" aria-label="关闭提醒" @click="hideBubble">
          <UiIcon size="14">mdi-close</UiIcon>
        </button>
        <div class="update-bubble__row update-bubble__title">
          <UiIcon size="15" color="warning">mdi-arrow-up-bold-circle-outline</UiIcon>
          发现新版本
        </div>
        <div class="update-bubble__row update-bubble__versions">
          <span>{{ versionInfo.currentVersion }}</span>
          <UiIcon size="13">mdi-arrow-right</UiIcon>
          <span class="version-latest">{{ versionInfo.latestVersion }}</span>
        </div>
        <p class="text-xs">{{ downloadHint }}</p>
        <UiButton size="x-small" variant="flat" color="primary" :disabled="!canDownload" @click="downloadLatest">下载 {{ downloadTarget?.label }}</UiButton>
        <UiButton size="x-small" variant="text" @click="openRelease">更新说明</UiButton>
      </div>
    </transition>
  </div>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { fetchHealth } from '@/shared/services/health'
import { compareVersions, releaseDownload, versionService } from '@/shared/services/version'
import { useToast } from '@/shared/composables/useToast'
import { useSystemStore } from '@/shared/stores/system'
import { useNotificationStore } from '@/shared/stores/notifications'

const systemStore = useSystemStore()
const notifications = useNotificationStore()
const { showToast } = useToast()
const downloadTarget = ref<ReturnType<typeof releaseDownload>>()
const platformError = ref('')
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
const canDownload = computed(() => canOpenRelease.value && !!downloadTarget.value && !!versionInfo.value.assets?.includes(downloadTarget.value.asset) && !!versionInfo.value.latestVersion && compareVersions(versionInfo.value.currentVersion, versionInfo.value.latestVersion) <= 0)
const downloadHint = computed(() => {
  if (versionInfo.value.status === 'error') return `${versionInfo.value.error || '版本检查失败'}；点击重试`
  if (platformError.value) return platformError.value
  if (!downloadTarget.value || versionInfo.value.status === 'checking') return '正在读取网关平台与最新版本'
  if (!versionInfo.value.assets?.includes(downloadTarget.value.asset)) return `最新正式版尚无 ${downloadTarget.value.label} 附件，请查看更新说明`
  if (!canDownload.value) return '当前网关版本领先于最新正式版，点击查看更新说明'
  return `下载最新正式版 · 网关平台 ${downloadTarget.value.label}；文件保存到浏览器所在电脑，需自行替换并重启网关`
})
function downloadLatest() {
  hideBubble()
  if (versionInfo.value.status === 'error') { versionService.clearCache(); void checkVersion(); return }
  if (canDownload.value && downloadTarget.value) window.location.assign(downloadTarget.value.url)
  else if (canOpenRelease.value) { showToast(downloadHint.value, 'info'); openRelease() }
}

const openRelease = () => {
  if (canOpenRelease.value) window.open(versionInfo.value.releaseUrl!, '_blank', 'noopener,noreferrer')
}

// 版本检查独立于认证，失败只影响徽标状态，不打断页面。
const checkVersion = async () => {
  try {
    const health = await fetchHealth()
    const currentVersion = health.version?.version || ''
    downloadTarget.value = undefined
    platformError.value = ''
    try {
      if (!health.platform) throw new Error('当前后端未返回平台信息，请从更新说明选择安装包')
      downloadTarget.value = releaseDownload(health.platform)
    } catch (error) { platformError.value = error instanceof Error ? error.message : String(error) }
    if (!currentVersion) {
      systemStore.setVersionInfo({ ...versionInfo.value, status: 'error', error: '当前网关未返回版本号' })
      return
    }
    versionService.setCurrentVersion(currentVersion)
    systemStore.setCurrentVersion(currentVersion)
    systemStore.setVersionInfo(await versionService.checkForUpdates())
  } catch (error) {
    console.warn('Version check failed:', error)
    systemStore.setVersionInfo({ ...versionInfo.value, status: 'error', error: error instanceof Error ? error.message : String(error) })
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
  notifications.addMessage(`发现新版本：${versionInfo.value.currentVersion} → ${versionInfo.value.latestVersion}`, 'info', { level: 'notice', source: '版本更新', key: `version:${versionInfo.value.latestVersion}`, link: versionInfo.value.releaseUrl ?? undefined })
  clearTimeout(hideTimer)
  hideTimer = setTimeout(hideBubble, BUBBLE_VISIBLE_MS)
}

watch(() => versionInfo.value.status, maybeShowBubble)

onMounted(() => {
  void checkVersion()
  maybeShowBubble()
  remindTimer = setInterval(() => { void checkVersion(); maybeShowBubble() }, BUBBLE_REMIND_INTERVAL_MS)
})
onBeforeUnmount(() => {
  clearInterval(remindTimer)
  clearTimeout(hideTimer)
})
</script>

<style scoped>
@reference "@/styles/index.css";

.version-anchor { @apply relative shrink-0 mr-1.5; }
.version-badge { @apply flex items-center [padding:4px_8px] [font-size:0.75rem] tabular-nums rounded-badge [border:1px_solid_color-mix(in_srgb,_var(--outline)_25%,_transparent)] [background:color-mix(in_srgb,_var(--surface-variant)_30%,_transparent)] cursor-default; }
.version-clickable { @apply cursor-pointer; }
.version-clickable:hover { @apply border-primary; }
.version-update { @apply [border-color:color-mix(in_srgb,_var(--warning)_50%,_transparent)]; }
.version-latest { @apply text-warning; }
.update-bubble { @apply menu-surface fixed [top:calc(var(--app-header-height,_56px)_+_12px)] right-4 [z-index:1200] w-55 p-4 rounded-card [border:1px_solid_color-mix(in_srgb,_var(--outline)_30%,_transparent)] shadow-popover; }
.update-bubble__close { @apply absolute top-1 right-1 p-1.5 rounded-badge; }
.update-bubble__close:hover { @apply [background:color-mix(in_srgb,_var(--foreground)_6%,_transparent)]; }
.update-bubble__row { @apply flex items-center gap-1.5 [font-size:0.8125rem] mb-2.5; }
.update-bubble__title { @apply [font-weight:650]; }
.update-bubble__versions { @apply font-mono; }
.update-bubble-enter-active, .update-bubble-leave-active { @apply [transition:opacity_160ms_ease,_transform_160ms_ease]; }
.update-bubble-enter-from, .update-bubble-leave-to { @apply opacity-0 [transform:translateY(-4px)]; }
@media (max-width: 600px) { .version-anchor { @apply hidden; } }
</style>
