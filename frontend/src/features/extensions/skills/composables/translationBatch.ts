import { computed, onUnmounted, ref, watch } from 'vue'
import { skillsApi } from '@/features/extensions/skills/services/skills'
import { useToast } from '@/shared/composables/useToast'
import { useAuthStore } from '@/shared/stores/auth'
import { useNotificationStore } from '@/shared/stores/notifications'
import type { ManagedSkill, SkillTranslationTask } from '@/types'

export const translationInProgress = ref(false)
const batchTask = ref<SkillTranslationTask | null>(null)
export const batchStarting = ref(false)
export const batchStatusError = ref('')
export const batchRunning = computed(() => batchTask.value?.running ?? false)
export const batchProgress = computed(() => {
  const task = batchTask.value
  if (!task?.running) return ''
  if (!task.current) return '正在准备项目技能翻译'
  return `${task.retry ? '重试' : '首轮'} ${task.index}/${task.total}：${task.current}`
})
export const batchResult = computed(() => batchTask.value?.running ? null : batchTask.value)

export function pendingProjectTranslations(skills: readonly ManagedSkill[]) {
  const unique = new Map<string, ManagedSkill>()
  const translated = new Set<string>()
  for (const skill of skills) {
    if (skill.locationKey !== 'project' || !skill.valid) continue
    if (skill.hasTranslation) translated.add(skill.name)
    if (!unique.has(skill.name)) unique.set(skill.name, skill)
  }
  return [...unique.values()].filter(skill => !translated.has(skill.name))
}

let pollTimer: ReturnType<typeof setTimeout> | undefined
let requestRevision = 0
let completedTask = ''

function applyTask(task: SkillTranslationTask | null) {
  batchTask.value = task
  if (!task || task.running || completedTask === task.id) return
  completedTask = task.id
  const key = `skill-translation:${task.id}`
  if (useNotificationStore().messages.some(item => item.key === key)) return
  const failed = task.failures.length
  const summary = `项目技能翻译完成 · 成功 ${task.succeeded} · 失败 ${failed} · 跳过 ${task.skipped}`
  const details = task.failures.map(item => `${item.name}：${item.error}`).join('\n')
  useToast().showToast(summary, failed ? 'warning' : 'success', {
    key, details, source: '批量翻译', level: failed ? 'warning' : 'notice'
  })
}

function schedulePoll() {
  clearTimeout(pollTimer)
  if (!useAuthStore().isAuthenticated) return
  if (batchStatusError.value) pollTimer = setTimeout(refreshSkillTranslation, 10000)
  else if (batchRunning.value) pollTimer = setTimeout(refreshSkillTranslation, 2500)
}

export async function refreshSkillTranslation() {
  if (batchStarting.value || !useAuthStore().isAuthenticated) return
  clearTimeout(pollTimer)
  const revision = ++requestRevision
  try {
    const { task } = await skillsApi.getSkillTranslation()
    if (revision !== requestRevision) return
    batchStatusError.value = ''
    applyTask(task)
  } catch (error) {
    if (revision === requestRevision) {
      batchStatusError.value = error instanceof Error ? error.message : String(error)
    }
  } finally {
    if (revision === requestRevision) schedulePoll()
  }
}

export async function startSkillTranslation(channelId: string, protocol: string, model: string) {
  if (batchStarting.value || batchRunning.value || translationInProgress.value) return
  ++requestRevision
  clearTimeout(pollTimer)
  batchStarting.value = true
  try {
    const { task } = await skillsApi.startSkillTranslation({ channelId, protocol, model })
    if (!useAuthStore().isAuthenticated) return
    batchStatusError.value = ''
    applyTask(task)
    schedulePoll()
  } finally {
    batchStarting.value = false
    if (!batchRunning.value) void refreshSkillTranslation()
  }
}

export function useSkillTranslationMonitor() {
  function reset() {
    ++requestRevision
    clearTimeout(pollTimer)
    batchTask.value = null
    batchStatusError.value = ''
    completedTask = ''
  }
  watch(() => useAuthStore().isAuthenticated, authenticated => {
    if (authenticated) void refreshSkillTranslation()
    else reset()
  }, { immediate: true })
  onUnmounted(reset)
}
