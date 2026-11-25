import { describe, expect, it } from 'vitest'
import { buildCacheKeepalive, defaultCacheKeepalive, supportsCacheKeepalive, validateCacheKeepalive } from '@/features/channels/utils/cacheKeepalive'
import { buildChannelPayload, defaultChannelForm, useChannelEditor } from '@/features/channels/composables/useChannelEditor'
import type { Channel } from '@/types/channel'

describe('缓存保活配置', () => {
  it.each([179, '', null, 180.5])('拒绝无效间隔 %s', intervalSeconds => {
    expect(validateCacheKeepalive({ ...defaultCacheKeepalive(), enabled: true, intervalSeconds })).not.toBe('')
  })

  it('允许三分钟间隔，禁止无限次数和无限时间同时启用', () => {
    const draft = { ...defaultCacheKeepalive(), enabled: true, intervalSeconds: 180 }
    expect(validateCacheKeepalive(draft)).toBe('')
    expect(validateCacheKeepalive({ ...draft, maxAttempts: 0, maxIdleMinutes: 0 })).not.toBe('')
  })

  it('关闭或切换到不支持的协议时不启用保活', () => {
    const draft = { ...defaultCacheKeepalive(), enabled: true }
    expect(buildCacheKeepalive(draft, false).enabled).toBe(false)
    const form = defaultChannelForm('images')
    form.serviceType = 'openai'
    form.cacheKeepalive = draft
    expect(buildChannelPayload(form, 'images', true).cacheKeepalive?.enabled).toBe(false)
  })

  it('关闭后保留已配置的间隔、次数和心跳内容', () => {
    const draft = { ...defaultCacheKeepalive(), enabled: false, intervalSeconds: '240', maxAttempts: 100, prompt: '仅回复 OK。' }
    const saved = buildCacheKeepalive(draft, true)
    expect(saved).toMatchObject({ enabled: false, intervalSeconds: 240, maxAttempts: 100, prompt: '仅回复 OK。' })
    expect(() => buildCacheKeepalive({ ...draft, intervalSeconds: '' }, true)).toThrow('未完成的数字')
  })

  it.each([
    ['messages', 'claude'], ['messages', 'responses'], ['messages', 'openai'], ['messages', 'gemini'],
    ['responses', 'claude'], ['responses', 'responses'], ['responses', 'openai'], ['responses', 'gemini'],
    ['chat', 'openai'], ['gemini', 'gemini'], ['gemini', 'claude'], ['gemini', 'openai']
  ])('支持现有对话转换 %s → %s', (kind, service) => {
    expect(supportsCacheKeepalive(kind, service)).toBe(true)
  })

  it('编辑草稿不改变已保存配置，载入旧渠道时保持关闭', () => {
    const channel: Channel = { index: 0, name: '测试', baseUrl: 'https://example.test', serviceType: 'claude', apiKeys: ['key'],
      cacheKeepalive: { ...defaultCacheKeepalive(), enabled: true } }
    const editor = useChannelEditor('messages')
    editor.loadChannelData(channel)
    editor.form.cacheKeepalive.prompt = '仅回复 OK。'
    expect(channel.cacheKeepalive?.prompt).toBe('接下来应该怎么做？')
    editor.loadChannelData({ ...channel, cacheKeepalive: undefined })
    expect(editor.form.cacheKeepalive.enabled).toBe(false)
  })
})
