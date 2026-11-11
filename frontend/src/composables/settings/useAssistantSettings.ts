// assistant 设置的草稿与操作编排；草稿仅供本设置页使用，保存以服务端响应回填。
import { computed, ref, type Ref } from 'vue'
import { api } from '@/services/api'
import type { AssistantStatusResponse } from '@/types'
import type { SettingsNotice as Notice } from './types'

export function useAssistantSettings(saving: Ref<boolean>) {
  const assistantStatus = ref<AssistantStatusResponse | null>(null)
  const assistantStatusError = ref('')
  const assistantModelsError = ref('')
  const assistantModelsLoading = ref(false)

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

  const assistantChannelItems = computed<Array<AssistantChannelOption>>(() => {
    const items = [{ title: '自动选择', value: '' }, ...(assistantStatus.value?.channels || []).map(channel => ({
      title: `${assistantKindLabel(channel.kind)} · ${channel.name}`,
      value: channel.id
    }))]
    if (assistantChannel.value && !items.some(item => item.value === assistantChannel.value)) {
      items.push({ title: '当前指定渠道（可用状态未确认）', value: assistantChannel.value })
    }
    return items
  })

  // 助手模型分组候选：四协议分组（messages → responses → chat → gemini），值取分组的
  // 匹配词（即请求的模型 ID）。跨协议重复的匹配词路由等价，按协议优先级去重。
  // 各协议独立加载，失败项在选择器旁明确展示，并保留当前草稿值。
  type AssistantModelOption = { title: string; value: string }

  const AUTO_ASSISTANT_MODEL: AssistantModelOption = { title: '自动（兜底分组）', value: '' }
  const assistantModelOptions = ref<AssistantModelOption[]>([AUTO_ASSISTANT_MODEL])
  const assistantModelItems = computed(() => {
    const items = assistantModelOptions.value
    return assistantModel.value && !items.some(item => item.value === assistantModel.value)
      ? [...items, { title: `当前模型分组：${assistantModel.value}（未在候选中找到）`, value: assistantModel.value }]
      : items
  })

  const loadAssistantModelOptions = async () => {
    assistantModelsLoading.value = true
    assistantModelsError.value = ''
    try {
      const tabs = ['messages', 'responses', 'chat', 'gemini'] as const
      const results = await Promise.allSettled(tabs.map(tab => api.getChannelPools(tab)))
      const items: AssistantModelOption[] = [AUTO_ASSISTANT_MODEL]
      const seen = new Set<string>([''])
      const errors: string[] = []
      tabs.forEach((tab, index) => {
        const result = results[index]
        if (result.status !== 'fulfilled') {
          errors.push(`${assistantKindLabels[tab]}：${result.reason instanceof Error ? result.reason.message : '加载失败'}`)
          return
        }
        for (const pool of result.value.pools || []) {
          const matcher = pool.modelMatcher?.trim()
          if (!matcher || seen.has(matcher)) continue
          seen.add(matcher)
          items.push({ title: `${assistantKindLabels[tab]} · ${pool.name}（匹配: ${matcher}）`, value: matcher })
        }
      })
      assistantModelOptions.value = items
      assistantModelsError.value = errors.join('；')
    } catch (error) {
      assistantModelsError.value = error instanceof Error ? error.message : '加载助手模型分组失败'
    } finally {
      assistantModelsLoading.value = false
    }
  }

  const loadAssistantStatus = async () => {
    try {
      assistantStatus.value = await api.getAssistantStatus()
      assistantStatusError.value = ''
    } catch (error) {
      assistantStatusError.value = error instanceof Error ? error.message : '读取助手状态失败'
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
      if (!settings.assistant) throw new Error('服务端未返回助手设置，请刷新确认保存状态')
      const saved = settings.assistant.model
      savedAssistantModel.value = saved
      assistantModel.value = saved
      const savedChannel = settings.assistant.channelId
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
    assistantStatusError,
    assistantModelsError,
    assistantModelsLoading,
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
