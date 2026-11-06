// 表单提交契约：草稿隔离、地址规范化与五协议字段转换。
import { describe, expect, it } from 'vitest'
import { ref } from 'vue'
import { buildChannelPayload, defaultChannelForm, useChannelEditor } from './useChannelEditor'
import type { Channel } from '@/services/api'

describe('渠道草稿与提交', () => {
  it('编辑和取消都不修改原渠道的数组、映射和自定义参数', () => {
    const channel: Channel = {
      name: '原渠道', serviceType: 'openai', index: 0, baseUrl: 'https://a.example',
      baseUrls: ['https://a.example', 'https://b.example'], apiKeys: ['key'], status: 'suspended',
      modelMapping: { source: ['target'] }, modelMappingChannels: { source: { target: 'id-1' } },
      imagesExtraParams: { size: '"1024x1024"' }
    }
    const original = JSON.parse(JSON.stringify(channel)) as Channel
    const editor = useChannelEditor('images')
    editor.loadChannelData(channel)
    editor.form.apiKeys[0] = '新 key'
    editor.form.baseUrls.push('https://new.example')
    editor.form.modelMapping.source.push('新目标')
    editor.form.modelMappingChannels.source.target = 'id-2'
    editor.form.imagesExtraParams[0].value = '新参数'
    editor.resetForm()
    expect(channel).toEqual(original)
    expect(editor.form.apiKeys).toEqual([])
    expect(editor.form.modelMapping).toEqual({})
  })

  it('提交去重地址、清理映射并剪除失效目标渠道，保留真实 key 字节', () => {
    const form = defaultChannelForm('messages')
    Object.assign(form, {
      name: '  渠道  ', serviceType: 'claude', website: '  ', proxyMode: 'custom', proxyUrl: ' http://proxy.example ',
      baseUrls: [' https://a.example/ ', 'https://a.example#/', '', 'https://b.example///#'],
      apiKeys: [' ', ' key-with-spaces ', 'key'],
      modelMapping: { ' source ': [' target ', 'target', ''], gone: [' '], ' ': ['unused'] },
      modelMappingChannels: { source: { target: ' id-1 ', removed: 'id-2' }, gone: { stale: 'id-3' } }
    })
    const payload = buildChannelPayload(form, 'messages', false)
    expect(payload).toMatchObject({
      name: '渠道', website: '', proxyUrl: 'http://proxy.example', baseUrl: 'https://a.example',
      baseUrls: ['https://a.example', 'https://b.example'], apiKeys: [' key-with-spaces ', 'key'],
      modelMapping: { source: ['target'] }, modelMappingChannels: { source: { target: 'id-1' } }
    })
    expect(form.modelMapping[' source ']).toEqual([' target ', 'target', ''])
    expect(payload).not.toHaveProperty('status')
  })

  it('单地址使用 baseUrl，新建不提交状态，编辑可写回 suspended 并清除旧代理', () => {
    const form = defaultChannelForm('responses')
    Object.assign(form, { baseUrl: ' https://single.example/# ', status: 'suspended', proxyUrl: 'http://stale.example' })
    const payload = buildChannelPayload(form, 'responses', true)
    expect(payload).toMatchObject({ baseUrl: 'https://single.example', proxyUrl: '', status: 'suspended', visionCapable: true })
    expect(payload).not.toHaveProperty('baseUrls')
    expect(buildChannelPayload(form, 'responses', false)).not.toHaveProperty('status')
  })

  it.each([' model ', { value: ' model ' }, null])('Chat 默认模型兼容 combobox 值 %j', value => {
    const form = defaultChannelForm('chat')
    form.defaultModel = value as unknown as string
    form.modelMapping = { source: ['target'] }
    form.modelMappingChannels = { source: { target: 'id-1' } }
    expect(buildChannelPayload(form, 'chat', true)).toMatchObject({
      defaultModel: value === null ? '' : 'model', modelMapping: {}, modelMappingChannels: {}
    })
  })

  it('Images 保留模型映射、主模型与参数，禁用不适用的文本功能', () => {
    const form = defaultChannelForm('images')
    Object.assign(form, {
      serviceType: 'responses', responsesModel: ' gpt-main ', defaultModel: 'unused', imagesForceNonStream: true,
      lowQuality: true, visionCapable: true, excludeFromConversation: true, visionLayerEnabled: true,
      visionLayerChannelId: 'id-1', visionLayerModel: 'vision', injectDummyThoughtSignature: true,
      stripThoughtSignature: true, disablePromptCacheKey: true, modelMapping: { image: ['image-target'] }
    })
    expect(buildChannelPayload(form, 'images', true, { size: '"1024x1024"' })).toMatchObject({
      responsesModel: 'gpt-main', defaultModel: '', imagesForceNonStream: true, imagesExtraParams: { size: '"1024x1024"' },
      lowQuality: false, visionCapable: false, excludeFromConversation: false, visionLayerEnabled: false,
      visionLayerChannelId: '', visionLayerModel: '', injectDummyThoughtSignature: false,
      stripThoughtSignature: false, disablePromptCacheKey: false, modelMapping: { image: ['image-target'] }
    })
  })

  it('编辑器 reset 和 buildPayload 使用当前协议，运行时状态不会写入配置', () => {
    const type = ref('responses')
    const editor = useChannelEditor(type)
    editor.loadChannelData({ name: '渠道', serviceType: 'gemini', baseUrl: '', apiKeys: [], index: 0, status: 'error' })
    expect(editor.buildPayload(true).status).toBe('active')
    type.value = 'images'
    editor.resetForm()
    expect(editor.form.serviceType).toBe('openai')
    expect(editor.buildPayload(false).imagesForceNonStream).toBe(false)
    type.value = 'gemini'
    expect(editor.buildPayload(false)).not.toHaveProperty('imagesForceNonStream')
  })
})
