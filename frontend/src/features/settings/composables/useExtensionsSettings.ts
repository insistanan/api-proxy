// extensions 设置与 Git 同步的草稿与操作编排。
import { computed, ref, watch, type Ref } from 'vue'
import { api } from '@/api'
import type { ExtensionsGitStatus, ExtensionsGitSyncResponse } from '@/types'
import type { SettingsNotice as Notice, SettingsSection } from '@/features/settings/composables/types'

export function useExtensionsSettings(saving: Ref<boolean>, activeSection: Ref<SettingsSection>) {
  const extensionsFormRef = ref()
  const skillsDir = ref('')
  const skillsRepoUrl = ref('')
  const savedSkillsDir = ref('')
  const savedSkillsRepoUrl = ref('')
  const extensionsNotice = ref<Notice>(null)

  const gitStatus = ref<ExtensionsGitStatus | null>(null)
  const gitStatusLoading = ref(false)
  const gitStatusError = ref('')
  const gitActionLoading = ref<'init' | 'clone' | 'pull' | 'push' | null>(null)
  const gitNotice = ref<Notice>(null)
  const commitMessage = ref('')

  const extensionsDirty = computed(
    () =>
      skillsDir.value.trim() !== savedSkillsDir.value ||
      skillsRepoUrl.value.trim() !== savedSkillsRepoUrl.value
  )

  const skillsRepoUrlRule = (value: string) => {
    const raw = value.trim()
    if (!raw) return true
    if (/\s/.test(raw)) return 'Git 仓库地址不能包含空白字符'
    if (raw.startsWith('-')) return 'Git 仓库地址不能以 "-" 开头'
    if (raw.includes('://')) {
      try {
        const parsed = new URL(raw)
        const scheme = parsed.protocol.replace(/:$/, '').toLowerCase()
        if (!['https', 'http', 'ssh', 'git', 'file'].includes(scheme)) {
          return `不支持的 Git 仓库协议 ${scheme}`
        }
        if (scheme !== 'file' && (!parsed.hostname || parsed.pathname === '/')) {
          return 'Git 仓库地址必须包含主机与仓库路径'
        }
        return true
      } catch {
        return 'Git 仓库地址格式无效'
      }
    }
    const atIndex = raw.indexOf('@')
    if (atIndex > 0) {
      const rest = raw.slice(atIndex + 1)
      const colonIndex = rest.indexOf(':')
      if (colonIndex > 0 && colonIndex < rest.length - 1) return true
    }
    return '请填写 https://、ssh:// 或 git@host:owner/repo.git 格式的地址'
  }

  const loadGitStatus = async () => {
    if (gitStatusLoading.value) return
    gitStatusLoading.value = true
    gitStatusError.value = ''
    try {
      gitStatus.value = await api.getExtensionsGitStatus()
    } catch (error) {
      gitStatusError.value = error instanceof Error ? error.message : '读取 Git 状态失败'
    } finally {
      gitStatusLoading.value = false
    }
  }

  watch(
    activeSection,
    section => {
      if (section === 'extensions') {
        void loadGitStatus()
      }
    },
    { immediate: true }
  )

  const saveExtensionsSettings = async () => {
    const { valid } = await extensionsFormRef.value.validate()
    if (!valid) return
    extensionsNotice.value = null
    saving.value = true
    const dirChanged = skillsDir.value.trim() !== savedSkillsDir.value
    try {
      const settings = await api.updateSettings({
        extensions: {
          skillsDir: skillsDir.value.trim(),
          skillsRepoUrl: skillsRepoUrl.value.trim()
        }
      })
      const saved = settings.extensions ?? { skillsDir: '', skillsRepoUrl: '' }
      savedSkillsDir.value = saved.skillsDir
      savedSkillsRepoUrl.value = saved.skillsRepoUrl
      skillsDir.value = saved.skillsDir
      skillsRepoUrl.value = saved.skillsRepoUrl
      extensionsNotice.value = {
        type: 'success',
        message: dirChanged ? '扩展目录已迁移并保存' : '扩展与同步设置已保存'
      }
      await loadGitStatus()
    } catch (error) {
      extensionsNotice.value = {
        type: 'error',
        message: error instanceof Error ? error.message : '保存扩展目录设置失败'
      }
    } finally {
      saving.value = false
    }
  }

  const formatSyncSummary = (resp: ExtensionsGitSyncResponse) => {
    const parts: string[] = []
    if (resp.message) parts.push(resp.message)
    if (resp.skillsReport) {
      const { synced, removed, skipped, errors } = resp.skillsReport
      parts.push(`Skills：同步 ${synced}，清理 ${removed}，跳过 ${skipped}${errors?.length ? `，失败 ${errors.length}` : ''}`)
    }
    if (resp.mcpAgents && resp.mcpAgents.length > 0) {
      const okCount = resp.mcpAgents.filter(item => item.ok).length
      const failCount = resp.mcpAgents.length - okCount
      parts.push(`MCP：${okCount} 个智能体成功${failCount > 0 ? `，${failCount} 个失败` : ''}`)
    }
    return parts.join(' · ')
  }

  const runGitAction = async (
    action: 'init' | 'clone' | 'pull' | 'push',
    runner: () => Promise<ExtensionsGitSyncResponse>
  ) => {
    if (gitActionLoading.value) return
    gitNotice.value = null
    gitStatusError.value = ''
    gitActionLoading.value = action
    try {
      const resp = await runner()
      gitStatus.value = resp.status
      if (action === 'push') {
        commitMessage.value = ''
      }
      gitNotice.value = {
        type: 'success',
        message: formatSyncSummary(resp) || 'Git 操作完成'
      }
    } catch (error) {
      gitNotice.value = {
        type: 'error',
        message: error instanceof Error ? error.message : 'Git 操作失败'
      }
      await loadGitStatus()
    } finally {
      gitActionLoading.value = null
    }
  }

  const initGitRepo = () =>
    runGitAction('init', () => api.initExtensionsGit(skillsRepoUrl.value.trim()))

  const cloneGitRepo = () =>
    runGitAction('clone', () => api.cloneExtensionsGit(skillsRepoUrl.value.trim()))

  const pullGitRepo = () => runGitAction('pull', () => api.pullExtensionsGit())

  const pushGitRepo = () =>
    runGitAction('push', () => api.pushExtensionsGit(commitMessage.value.trim()))

  return {
    extensionsFormRef,
    skillsDir,
    skillsRepoUrl,
    savedSkillsDir,
    savedSkillsRepoUrl,
    extensionsDirty,
    extensionsNotice,
    skillsRepoUrlRule,
    saveExtensionsSettings,
    gitStatus,
    gitStatusLoading,
    gitStatusError,
    gitActionLoading,
    gitNotice,
    commitMessage,
    loadGitStatus,
    initGitRepo,
    cloneGitRepo,
    pullGitRepo,
    pushGitRepo,
    saving
  }
}
