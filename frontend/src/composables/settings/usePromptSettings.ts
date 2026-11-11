// prompts 设置的草稿与操作编排；草稿仅供本设置页使用，保存以服务端响应回填。
import { computed, ref, type Ref } from 'vue'
import { api } from '@/services/api'
import type { PromptEntry } from '@/types'
import type { SettingsNotice as Notice } from './types'

export function usePromptSettings(saving: Ref<boolean>) {
  // ===== 通用提示词（版本库：各智能体配置页"一键配置提示词"的数据源） =====

  const promptEntries = ref<PromptEntry[]>([])
  const promptActiveVersion = ref('')
  // 选中条目按对象引用而非版本号定位：重命名版本号时编辑器不会失焦丢失
  const selectedPromptEntry = ref<PromptEntry | null>(null)
  const savedPromptsSnapshot = ref('')
  const savedPromptActiveVersion = ref('')
  const promptNotice = ref<Notice>(null)

  const promptEntryTitle = (entry: PromptEntry): string => entry.version + (entry.note ? ` · ${entry.note}` : '')

  const promptsDirty = computed(() => JSON.stringify(promptEntries.value) !== savedPromptsSnapshot.value || promptActiveVersion.value !== savedPromptActiveVersion.value)

  const formatPromptTime = (millis: number): string => {
    if (!millis) return '从未保存'
    return new Date(millis).toLocaleString('zh-CN', { hour12: false })
  }

  const addPromptVersion = () => {
    const entry: PromptEntry = { version: '', note: '', content: '', updatedAt: 0 }
    promptEntries.value.push(entry)
    selectedPromptEntry.value = entry
  }

  const removePromptVersion = () => {
    const entry = selectedPromptEntry.value
    if (!entry) return
    if (!window.confirm(`删除版本「${entry.version || '（未命名）'}」？删除后不可恢复。`)) return
    promptEntries.value = promptEntries.value.filter(item => item !== entry)
    if (promptActiveVersion.value === entry.version) promptActiveVersion.value = ''
    selectedPromptEntry.value = promptEntries.value[0] ?? null
  }

  const setActivePromptVersion = () => {
    const entry = selectedPromptEntry.value
    if (entry) promptActiveVersion.value = entry.version
  }

  const validatePromptsForSave = (): string | null => {
    const seen = new Set<string>()
    for (const entry of promptEntries.value) {
      if (!entry.version) return '存在版本号为空的条目，请补全版本号'
      if (seen.has(entry.version)) return `版本号「${entry.version}」重复`
      seen.add(entry.version)
    }
    if (promptActiveVersion.value && !seen.has(promptActiveVersion.value)) {
      return `生效版本「${promptActiveVersion.value}」不存在，请重新选择`
    }
    return null
  }

  const savePromptSettings = async () => {
    promptNotice.value = null
    const validationError = validatePromptsForSave()
    if (validationError) {
      promptNotice.value = { type: 'error', message: validationError }
      return
    }
    saving.value = true
    try {
      const settings = await api.updateSettings({
        prompts: { entries: promptEntries.value, activeVersion: promptActiveVersion.value }
      })
      if (!settings.prompts) throw new Error('服务端未返回提示词设置，请刷新确认保存状态')
      // 后端保存时会归一化（trim + 刷新 UpdatedAt），以响应为准回填快照
      promptEntries.value = settings.prompts.entries
      promptActiveVersion.value = settings.prompts.activeVersion
      savedPromptActiveVersion.value = promptActiveVersion.value
      selectedPromptEntry.value =
        promptEntries.value.find(entry => entry.version === promptActiveVersion.value) ?? promptEntries.value[0] ?? null
      savedPromptsSnapshot.value = JSON.stringify(promptEntries.value)
      promptNotice.value = { type: 'success', message: '通用提示词已保存' }
    } catch (error) {
      promptNotice.value = { type: 'error', message: error instanceof Error ? error.message : '保存通用提示词失败' }
    } finally {
      saving.value = false
    }
  }

  return {
    promptEntries,
    promptActiveVersion,
    selectedPromptEntry,
    savedPromptsSnapshot,
    savedPromptActiveVersion,
    promptNotice,
    promptEntryTitle,
    promptsDirty,
    formatPromptTime,
    addPromptVersion,
    removePromptVersion,
    setActivePromptVersion,
    savePromptSettings,
    saving
  }
}
