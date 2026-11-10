// assistant 设置的草稿与操作编排；草稿仅供本设置页使用，保存以服务端响应回填。
import { computed, ref, type Ref } from 'vue'
import { api } from '@/services/api'
import type { AssistantStatusResponse } from '@/types'
import type { SettingsNotice as Notice } from './types'

export function useAssistantSettings(saving: Ref<boolean>) {
  const assistantStatus = ref<AssistantStatusResponse | null>(null)

  const assistantKindLabels: Record<string, string> = {
    messages: 'Messages',
    responses: 'Responses',
    chat: 'Chat',
    gemini: 'Gemini'
  }

  const assistantKindLabel = (kind: string) => assistantKindLabels[kind] ?? kind

  // AI 配置助手设置（模型分组路由 + 显式渠道）
  const assistantModel = ref('')
  const savedAssistantModel = ref('')
  const assistantChannel = ref('')
  const savedAssistantChannel = ref('')
  const assistantNotice = ref<Notice>(null)

  const assistantDirty = computed(
    () =>
      assistantModel.value.trim() !== savedAssistantModel.value ||
      assistantChannel.value !== savedAssistantChannel.value
  )

  // 渠道选择器候选：自动 + status 返回的四协议渠道（m → r → c → g 顺序）。
  type AssistantChannelOption = { title: string; value: string }

  const assistantChannelItems = computed<Array<AssistantChannelOption>>(() => [
    { title: '自动选择', value: '' },
    ...(assistantStatus.value?.channels || []).map(channel => ({
      title: `${assistantKindLabel(channel.kind)} · ${channel.name}`,
      value: channel.id
    }))
  ])

  // 助手模型分组候选：四协议分组（messages → responses → chat → gemini），值取分组的
  // 匹配词（即请求的模型 ID）。跨协议重复的匹配词路由等价，按协议优先级去重。
  // 拉取失败时静默降级——下拉只剩"自动"一项，主设置加载不受影响。
  type AssistantModelOption = { title: string; value: string }

  const AUTO_ASSISTANT_MODEL: AssistantModelOption = { title: '自动（兜底分组）', value: '' }
  const assistantModelItems = ref<AssistantModelOption[]>([AUTO_ASSISTANT_MODEL])

  const loadAssistantModelOptions = async () => {
    const tabs = ['messages', 'responses', 'chat', 'gemini'] as const
    const results = await Promise.allSettled(tabs.map(tab => api.getChannelPools(tab)))
    const items: AssistantModelOption[] = [AUTO_ASSISTANT_MODEL]
    const seen = new Set<string>([''])
    tabs.forEach((tab, index) => {
      const result = results[index]
      if (result.status !== 'fulfilled') return
      for (const pool of result.value.pools || []) {
        const matcher = pool.modelMatcher?.trim()
        if (!matcher || seen.has(matcher)) continue
        seen.add(matcher)
        items.push({ title: `${assistantKindLabels[tab]} · ${pool.name}（匹配: ${matcher}）`, value: matcher })
      }
    })
    assistantModelItems.value = items
  }

  const loadAssistantStatus = async () => {
    try {
      assistantStatus.value = await api.getAssistantStatus()
    } catch {
      assistantStatus.value = null
    }
  }

  const saveAssistantSettings = async () => {
    assistantNotice.value = null
    saving.value = true
    try {
      // model 与 channelId 必须同时提交，未提交字段会被设置接口按零值覆盖。
      const settings = await api.updateSettings({
        assistant: { model: assistantModel.value.trim(), channelId: assistantChannel.value }
      })
      const saved = settings.assistant?.model ?? ''
      savedAssistantModel.value = saved
      assistantModel.value = saved
      const savedChannel = settings.assistant?.channelId ?? ''
      savedAssistantChannel.value = savedChannel
      assistantChannel.value = savedChannel
      assistantNotice.value = { type: 'success', message: '助手设置已保存' }
      await loadAssistantStatus()
    } catch (error) {
      assistantNotice.value = { type: 'error', message: error instanceof Error ? error.message : '保存助手设置失败' }
    } finally {
      saving.value = false
    }
  }

  return {
    assistantStatus,
    assistantKindLabel,
    assistantModel,
    savedAssistantModel,
    assistantChannel,
    savedAssistantChannel,
    assistantNotice,
    assistantDirty,
    assistantChannelItems,
    assistantModelItems,
    loadAssistantModelOptions,
    loadAssistantStatus,
    saveAssistantSettings,
    saving
  }
}
