<template>
  <v-card elevation="0" class="settings-card agent-config-panel tool-install-banner mb-5">
    <div class="agent-config-panel-header">
      <div>
        <div class="agent-config-panel-title d-flex align-center ga-2">
          {{ info?.displayName || '工具' }} 安装与版本
          <v-chip v-if="statusBadge" :color="statusBadge.color" size="x-small" label>
            <v-icon start size="12">{{ statusBadge.icon }}</v-icon>
            {{ statusBadge.text }}
          </v-chip>
        </div>
        <div class="agent-config-panel-subtitle">
          {{ checking ? '正在检测…' : `检测于 ${detectedAtText}` }}
          <template v-if="!checking && latestFromCacheNote">（1 小时内复用上次检测结果）</template>
        </div>
      </div>
      <v-btn variant="text" prepend-icon="mdi-refresh" :loading="checking" @click="check(true)">重新检测</v-btn>
    </div>
    <v-divider />
    <v-card-text class="pa-5">
      <div v-if="loadError" class="text-caption text-error mb-2">{{ loadError }}</div>
      <v-progress-linear v-if="checking && !info" indeterminate color="primary" />
      <template v-if="info">
        <div class="d-flex flex-wrap align-center ga-2 mb-4">
          <span class="text-body-2 text-medium-emphasis">当前版本</span>
          <code class="tool-install-banner__version">{{ info.current || (info.currentError ? '未知' : '未安装') }}</code>
          <v-icon v-if="info.current" icon="mdi-arrow-right" size="16" />
          <span class="text-body-2 text-medium-emphasis">最新版本</span>
          <code class="tool-install-banner__version">{{ info.latest || '未知' }}</code>
        </div>

        <div v-if="info.currentError" class="text-caption text-error mb-2">{{ info.currentError }}</div>
        <div v-if="info.latestError" class="text-caption text-error mb-2">{{ info.latestError }}</div>
        <div v-if="copyError" class="text-caption text-error mb-2">{{ copyError }}</div>

        <div class="tool-install-banner__cmd">
          <code>{{ info.installCmd }}</code>
          <v-tooltip location="top" :text="copied ? '已复制' : '复制命令'">
            <template #activator="{ props: activatorProps }">
              <v-btn
                v-bind="activatorProps"
                icon
                size="small"
                variant="text"
                :color="copied ? 'success' : undefined"
                @click="copyInstallCmd"
              >
                <v-icon>{{ copied ? 'mdi-check' : 'mdi-content-copy' }}</v-icon>
              </v-btn>
            </template>
          </v-tooltip>
        </div>
      </template>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
// CLI 工具安装横幅：客户端配置页（GPT / Claude Code / OpenCode / pi）顶部展示
// 工具的当前版本、npm 最新版本与当前平台的安装命令。
// 后端成功结果缓存 1 小时，失败结果缓存 1 分钟；"重新检测"绕过缓存。
// 版本对比用偏序比较：仅 latest 严格大于 current 才提示有更新，
// 本地装了抢先/预发布版（current >= latest）时显示"已最新"而非误报。
import { computed, onMounted, ref } from 'vue'
import { api, type ToolVersionInfo } from '@/services/api'

const props = defineProps<{
  /** 工具标识，须在后端白名单内：claude / codex / opencode / pi */
  tool: string
}>()

const info = ref<ToolVersionInfo | null>(null)
const checking = ref(false)
const copied = ref(false)
const loadError = ref('')
const copyError = ref('')

/** 解析 x.y.z[-pre] 为可比较的三段数字 + 预发布段；解析失败返回 null */
const parseSemver = (version: string): { core: number[]; pre: string } | null => {
  const match = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([\dA-Za-z-]+(?:\.[\dA-Za-z-]+)*))?(?:\+[\dA-Za-z-]+(?:\.[\dA-Za-z-]+)*)?$/.exec(version.trim())
  if (!match) return null
  const core = [Number(match[1]), Number(match[2]), Number(match[3])]
  const pre = match[4] ?? ''
  if (!core.every(Number.isSafeInteger) || pre.split('.').some(part => /^0\d+$/.test(part))) return null
  return { core, pre }
}

/** 返回 current 与 latest 的比较结果；无法比较时显式返回 null。 */
const compareVersions = (current: string, latest: string): number | null => {
  const currentParsed = parseSemver(current)
  const latestParsed = parseSemver(latest)
  if (!currentParsed || !latestParsed) return null
  for (let i = 0; i < 3; i++) {
    if (latestParsed.core[i] !== currentParsed.core[i]) {
      return currentParsed.core[i] < latestParsed.core[i] ? -1 : 1
    }
  }
  if (currentParsed.pre === latestParsed.pre) return 0
  if (!currentParsed.pre) return 1
  if (!latestParsed.pre) return -1
  const currentParts = currentParsed.pre.split('.')
  const latestParts = latestParsed.pre.split('.')
  for (let i = 0; i < Math.max(currentParts.length, latestParts.length); i++) {
    const left = currentParts[i]
    const right = latestParts[i]
    if (left === right) continue
    if (left === undefined) return -1
    if (right === undefined) return 1
    const leftNumeric = /^\d+$/.test(left)
    const rightNumeric = /^\d+$/.test(right)
    if (leftNumeric !== rightNumeric) return leftNumeric ? -1 : 1
    if (leftNumeric && left.length !== right.length) return left.length < right.length ? -1 : 1
    return left < right ? -1 : 1
  }
  return 0
}

const statusBadge = computed(() => {
  if (checking.value) return { color: 'info', icon: 'mdi-refresh', text: '检测中' }
  if (loadError.value) return { color: 'error', icon: 'mdi-alert-circle-outline', text: '检测失败' }
  if (!info.value) return null
  if (info.value.currentError) {
    return { color: 'error', icon: 'mdi-alert-circle-outline', text: '检测失败' }
  }
  if (!info.value.current) {
    return { color: 'warning', icon: 'mdi-download', text: '未安装' }
  }
  if (info.value.latestError) {
    return { color: 'warning', icon: 'mdi-alert-circle-outline', text: '更新检查失败' }
  }
  if (!info.value.latest) {
    return { color: 'warning', icon: 'mdi-alert-circle-outline', text: '最新版本未知' }
  }
  const comparison = compareVersions(info.value.current, info.value.latest)
  if (comparison === null) {
    return { color: 'warning', icon: 'mdi-alert-circle-outline', text: '版本格式异常' }
  }
  if (comparison < 0) {
    return { color: 'warning', icon: 'mdi-arrow-up-bold', text: '有更新' }
  }
  return { color: 'success', icon: 'mdi-check-circle', text: '已最新' }
})

const detectedAtText = computed(() => {
  if (!info.value?.detectedAt) return '未知'
  return new Date(info.value.detectedAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
})

// 后端缓存返回的 detectedAt 距今超过 1 分钟即视为复用（TTL 是 1 小时，
// 刚检测过的结果不显示该提示）。仅用于文案，不影响逻辑。
const latestFromCacheNote = computed(() => {
  if (!info.value?.detectedAt || info.value.currentError || info.value.latestError || loadError.value) return false
  return Date.now() - info.value.detectedAt > 60_000
})

const check = async (force: boolean) => {
  checking.value = true
  loadError.value = ''
  try {
    info.value = await api.getToolVersion(props.tool, force)
  } catch (error) {
    // 已有数据时保留旧值；错误文案由 loadError 统一展示
    loadError.value = error instanceof Error ? error.message : '检测工具版本失败'
  } finally {
    checking.value = false
  }
}

const copyInstallCmd = async () => {
  if (!info.value) return
  copyError.value = ''
  try {
    await navigator.clipboard.writeText(info.value.installCmd)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  } catch {
    // 剪贴板被浏览器策略拒绝时不静默吞掉，给出可见反馈
    copyError.value = '复制失败，请检查浏览器剪贴板权限'
  }
}

onMounted(() => check(false))
</script>

<style scoped>
.tool-install-banner__version {
  padding: 2px 8px;
  font-weight: 650;
}

.tool-install-banner__cmd {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 10px 8px 10px 14px;
  background: rgba(var(--v-theme-surface-variant), 0.45);
  border-radius: 6px;
}

.tool-install-banner__cmd code {
  overflow-wrap: anywhere;
  font-size: 0.85rem;
}
</style>
