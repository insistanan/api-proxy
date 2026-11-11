import { describe, expect, it, vi } from 'vitest'
import { createSSRApp, nextTick, ref } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { useChannelModal } from './useChannelModal'
import { CHANNEL_PRESETS, resolveChannelPreset } from '@/utils/channelPresets'
import type { ApiTab } from '@/services/channel-api'

vi.mock('vuetify', () => ({ useTheme: () => ({ current: ref({ dark: false }) }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))

async function setup(entry: ApiTab) {
  let editor!: ReturnType<typeof useChannelModal>
  const emit = vi.fn()
  await renderToString(createSSRApp({ setup() {
    editor = useChannelModal({ show: false, channelType: entry, initialPoolId: 'target-pool' }, emit)
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

  it('不声明不存在的转换，也不把对话接口用于 Embeddings/Images', () => {
    expect(resolveChannelPreset(preset('anthropic'), 'chat')).toBeUndefined()
    expect(resolveChannelPreset(preset('deepseek'), 'embeddings')).toBeUndefined()
    expect(resolveChannelPreset(preset('openai'), 'images')).toBeUndefined()
    expect(resolveChannelPreset(preset('openai'), 'chat')?.protocol).toBe('chat')
    expect(resolveChannelPreset(preset('deepseek'), 'gemini')?.protocol).toBe('chat')
  })
})
