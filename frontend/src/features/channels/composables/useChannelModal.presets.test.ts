import { describe, expect, it, vi } from 'vitest'
import { createSSRApp, nextTick } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { useChannelModal } from '@/features/channels/composables/useChannelModal'
import { CHANNEL_PRESETS, resolveChannelPreset } from '@/features/channels/utils/channelPresets'
import type { ApiTab } from '@/features/channels/services/channel-api'

vi.mock('@/shared/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))

async function setup(entry: ApiTab, extra: Partial<Parameters<typeof useChannelModal>[0]> = {}) {
  let editor!: ReturnType<typeof useChannelModal>
  const emit = vi.fn()
  await renderToString(createSSRApp({ setup() {
    editor = useChannelModal({ show: false, channelType: entry, initialPoolId: 'target-pool', ...extra }, emit)
    return () => null
  } }))
  return { editor, emit }
}
const preset = (id: string) => {
  const found = CHANNEL_PRESETS.find(item => item.id === id)
  if (!found) throw new Error(`缺少预设 ${id}`)
  return found
}

describe('官方渠道快捷接入', () => {
  it.each([
    ['messages', 'claude', 'https://api.deepseek.com/anthropic/v1/messages'],
    ['responses', 'responses', 'https://api.deepseek.com/responses'],
    ['chat', 'openai', 'https://api.deepseek.com/chat/completions']
  ] as const)('DeepSeek 在 %s 入口保存对应的原生端点', async (entry, serviceType, url) => {
    const { editor, emit } = await setup(entry)
    editor.form.poolId = 'target-pool'
    editor.quickInput.value = 'sk-existing-fixture'
    editor.parseQuickInput()
    editor.applyProviderPreset(preset('deepseek'))
    await nextTick()
    editor.handleQuickSubmit()
    expect(emit).toHaveBeenCalledWith('save', expect.objectContaining({
      serviceType, baseUrl: url, useFullUrl: true, apiKeys: ['sk-existing-fixture'], poolId: 'target-pool'
    }), { isQuickAdd: true })
  })

  it.each([
    ['responses', 'anthropic', 'claude', 'Claude Code'],
    ['messages', 'openai', 'responses', 'Codex']
  ] as const)('%s 入口选 %s 时使用目标协议和目标身份', async (entry, vendor, upstream, identity) => {
    const { editor, emit } = await setup(entry)
    editor.quickInput.value = 'sk-existing-fixture'
    editor.parseQuickInput()
    editor.applyProviderPreset(preset(vendor))
    editor.form.clientIdentityMode = 'compatibility'
    expect(editor.quickServiceType.value).toBe(upstream)
    expect(editor.clientIdentityModeItems.value[0].title).toContain(identity)
    editor.handleQuickSubmit()
    expect(emit).toHaveBeenCalledWith('save', expect.objectContaining({
      serviceType: upstream, clientIdentityMode: 'compatibility'
    }), { isQuickAdd: true })
    editor.toggleMode()
    await nextTick()
    expect(editor.form.serviceType).toBe(upstream)
    expect(editor.supportsClientIdentity.value).toBe(true)
  })

  it('详细模式选择渠道商及模式往返保留已有密钥、分组和映射', async () => {
    const { editor } = await setup('responses')
    editor.isQuickMode.value = false
    Object.assign(editor.form, { name: '自定义名称', apiKeys: ['sk-edited-key'], poolId: 'existing-pool', modelMapping: { alias: ['actual-model'] } })
    editor.applyProviderPreset(preset('anthropic'))
    await nextTick()
    editor.toggleMode()
    editor.applyProviderPreset(preset('deepseek'))
    editor.toggleMode()
    await nextTick()
    expect(editor.form).toMatchObject({ name: '自定义名称', apiKeys: ['sk-edited-key'], poolId: 'existing-pool', modelMapping: { alias: ['actual-model'] }, serviceType: 'responses' })
  })

  it('Gemini 动态模型路径不使用固定完整 URL，继续输入 Key 后保留协议', async () => {
    const { editor, emit } = await setup('messages')
    // Messages 入口优先按厂商首选 Gemini 协议转换。
    editor.applyProviderPreset(preset('google'))
    editor.quickInput.value += '\nsk-existing-fixture'
    editor.parseQuickInput()
    editor.handleQuickSubmit()
    expect(emit).toHaveBeenCalledWith('save', expect.objectContaining({ serviceType: 'gemini', useFullUrl: false }), { isQuickAdd: true })
  })

  it('粘贴整段中文配置时按本次输入解析名称、地址和密钥', async () => {
    const { editor } = await setup('messages')
    editor.parseQuickInput('名称：商汤\nURL：https://token.sensenova.cn/v1\n密钥：sk-fixture-0123456789abcdefghij')
    expect(editor.quickInput.value).toContain('token.sensenova.cn')
    expect(editor.detectedBaseUrls.value).toEqual(['https://token.sensenova.cn/v1'])
    expect(editor.detectedApiKeys.value).toEqual(['sk-fixture-0123456789abcdefghij'])
    expect(editor.detectedChannelName.value).toBe('商汤')
  })

  it('从公用图片理解池和文本摘要池入口打开时默认进入快速模式并带上池子身份', async () => {
    const vision = await setup('messages', { show: true, draftPreset: 'vision' })
    expect(vision.editor.isQuickMode.value).toBe(true)
    expect(vision.editor.form.visionCapable).toBe(true)
    expect(vision.editor.form.excludeFromConversation).toBe(true)
    vision.editor.parseQuickInput('名称：视觉渠道\nURL：https://vision.example.com/v1\n密钥：sk-vision-fixture-1234567890')
    vision.editor.handleQuickSubmit()
    expect(vision.emit).toHaveBeenCalledWith('save', expect.objectContaining({
      name: '视觉渠道',
      visionCapable: true,
      excludeFromConversation: true
    }), { isQuickAdd: false })

    const summary = await setup('messages', { show: true, draftPreset: 'summary' })
    expect(summary.editor.isQuickMode.value).toBe(true)
    expect(summary.editor.form.compaction.summaryCapable).toBe(true)
    expect(summary.editor.form.excludeFromConversation).toBe(true)
    summary.editor.parseQuickInput('名称：便宜摘要\nURL：https://summary.example.com/v1\n密钥：sk-summary-fixture-1234567890')
    // 未填摘要模型前不允许提交
    expect(summary.editor.isQuickFormValid.value).toBe(false)
    summary.editor.form.compaction.summaryModel = 'gpt-4o-mini'
    expect(summary.editor.isQuickFormValid.value).toBe(true)
    summary.editor.handleQuickSubmit()
    expect(summary.emit).toHaveBeenCalledWith('save', expect.objectContaining({
      name: '便宜摘要',
      visionCapable: false,
      excludeFromConversation: true,
      compaction: expect.objectContaining({ summaryCapable: true, summaryPool: false, summaryModel: 'gpt-4o-mini' })
    }), { isQuickAdd: false })
  })

  it('不声明不存在的转换，也不把对话接口用于 Embeddings/Images', () => {
    expect(resolveChannelPreset(preset('anthropic'), 'chat')).toBeUndefined()
    expect(resolveChannelPreset(preset('deepseek'), 'embeddings')).toBeUndefined()
    expect(resolveChannelPreset(preset('openai'), 'images')).toBeUndefined()
    expect(resolveChannelPreset(preset('openai'), 'chat')?.protocol).toBe('chat')
    expect(resolveChannelPreset(preset('deepseek'), 'gemini')?.protocol).toBe('chat')
  })
})
