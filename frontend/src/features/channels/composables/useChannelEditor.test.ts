// 表单提交契约：草稿隔离、地址规范化与协议字段转换。
import { describe, expect, it } from 'vitest'
import { buildChannelPayload, defaultChannelForm, useChannelEditor } from '@/features/channels/composables/useChannelEditor'
import type { Channel } from '@/api'

const source: Channel = {
  id: 'source-id', name: '供应商A', index: 3, poolId: 'source-pool', status: 'disabled', serviceType: 'openai',
  baseUrl: 'https://api.example.com/v1', baseUrls: ['https://api.example.com/v1', 'https://backup.example.com/v1'], apiKeys: ['sk-fixture-key'],
  proxyMode: 'custom', proxyUrl: 'http://localhost:8080', modelMapping: { '*': ['local-model', 'external-model'], sonnet: ['local-model'] },
  modelMappingChannels: { '*': { 'external-model': 'other-channel' } }, reasoningEffort: 'high', visionLayerEnabled: true, visionLayerChannelId: 'vision-channel'
}

describe('渠道编辑器草稿与提交', () => {
  it('分支并发默认 3，编辑和复制保留已配置上限', () => {
    const form = defaultChannelForm('messages')
    expect(form.maxBranchConcurrency).toBe(3)
    expect(buildChannelPayload(form, 'messages', false).maxBranchConcurrency).toBe(3)
    const editor = useChannelEditor('messages')
    editor.loadChannelCopy({ ...source, maxBranchConcurrency: 7 }, 'messages')
    expect(editor.form.maxBranchConcurrency).toBe(7)
    editor.form.maxBranchConcurrency = '9'
    expect(editor.buildPayload(false).maxBranchConcurrency).toBe(9)
    for (const invalid of [0, -1, 1.5, '', null, NaN, Infinity]) {
      editor.form.maxBranchConcurrency = invalid
      expect(() => editor.buildPayload(false)).toThrow('大于零的整数')
    }
  })

  it('复制渠道时隔离原数据，并按目标协议清理专属引用', () => {
    const editor = useChannelEditor('messages')
    editor.loadChannelCopy(source, 'messages')
    expect(editor.form).toMatchObject({ name: '供应商A (副本)', poolId: 'default', status: 'active', reasoningEffort: 'high', apiKeys: source.apiKeys, modelMapping: source.modelMapping, modelMappingChannels: source.modelMappingChannels })
    editor.form.apiKeys.push('sk-new-key')
    editor.form.modelMapping['*'].push('new-model')
    editor.form.modelMappingChannels['*']['external-model'] = 'new-channel'
    expect(source.apiKeys).toEqual(['sk-fixture-key'])
    expect(source.modelMapping?.['*']).toEqual(['local-model', 'external-model'])
    expect(source.modelMappingChannels?.['*']['external-model']).toBe('other-channel')
    expect(editor.buildPayload(false)).not.toHaveProperty('id')
    expect(editor.buildPayload(false)).not.toHaveProperty('status')

    const crossProtocol = useChannelEditor('responses')
    crossProtocol.loadChannelCopy(source, 'messages')
    expect(crossProtocol.form).toMatchObject({
      baseUrls: source.baseUrls, apiKeys: source.apiKeys, proxyMode: 'custom', proxyUrl: source.proxyUrl, poolId: 'default',
      visionCapable: true, visionLayerEnabled: false, visionLayerChannelId: '', reasoningEffort: 'inherit', modelMappingChannels: {},
      modelMapping: { '*': ['local-model'], sonnet: ['local-model'] }
    })
  })

  it('按协议转换模型字段并为 Images、Embeddings 和 Decisions 清空不适用映射', () => {
    for (const protocol of ['images', 'embeddings', 'decisions'] as const) {
      const editor = useChannelEditor(protocol)
      editor.loadChannelCopy(source, 'messages')
      expect(editor.form.modelMapping).toEqual({})
      expect(editor.form.modelMappingChannels).toEqual({})
      expect(editor.form.visionLayerEnabled).toBe(false)
      expect(editor.form.embeddingsDimensions).toBe(1024)
      expect(editor.form.responsesModel).toBe('')
    }
    const chat = useChannelEditor('chat')
    chat.loadChannelCopy(source, 'messages')
    expect(chat.form.defaultModel).toBe('local-model')
    const messages = useChannelEditor('messages')
    messages.loadChannelCopy({ ...source, modelMapping: {}, modelMappingChannels: {}, defaultModel: 'chat-model' }, 'chat')
    expect(messages.form.modelMapping).toEqual({ '*': ['chat-model'] })
    chat.loadChannelCopy({ ...source, modelMapping: { '*': ['a', 'b'] }, modelMappingChannels: {} }, 'messages')
    expect(chat.form.defaultModel).toBe('')
  })

  it('保留对话思考策略和输出设置，编辑重置不污染原渠道', () => {
    const editor = useChannelEditor('messages')
    const channel: Channel = { name: '思考渠道', serviceType: 'gemini', baseUrl: '', apiKeys: [], index: 0, reasoningEffort: 'max', reasoningOutputMode: 'hide', parseEmbeddedReasoning: true, requireReasoningContent: true }
    expect(editor.form.reasoningOutputMode).toBe('preserve')
    editor.loadChannelData(channel)
    expect(editor.buildPayload(true)).toMatchObject({ reasoningEffort: 'max', reasoningOutputMode: 'hide', parseEmbeddedReasoning: true })
    expect(editor.buildPayload(true)).not.toHaveProperty('requireReasoningContent')
    editor.form.reasoningEffort = 'none'
    expect(editor.buildPayload(true).reasoningEffort).toBe('none')
    editor.form.reasoningEffort = 'auto'
    expect(editor.buildPayload(true).reasoningEffort).toBe('auto')
    editor.form.reasoningOutputMode = 'preserve'
    editor.form.parseEmbeddedReasoning = false
    expect(channel.reasoningEffort).toBe('max')
    expect(channel.reasoningOutputMode).toBe('hide')
    expect(channel.requireReasoningContent).toBe(true)
    expect(editor.buildPayload(true)).toMatchObject({ reasoningOutputMode: 'preserve', parseEmbeddedReasoning: false })
    editor.resetForm()
    expect(editor.buildPayload(true)).toMatchObject({ reasoningEffort: 'inherit', reasoningOutputMode: 'preserve', parseEmbeddedReasoning: false })
    expect(editor.buildPayload(false)).not.toHaveProperty('requireReasoningContent')
    for (const protocol of ['images', 'embeddings', 'decisions'] as const) {
      const form = defaultChannelForm(protocol)
      form.reasoningEffort = 'high'
      expect(buildChannelPayload(form, protocol, false).reasoningEffort).toBe('inherit')
    }
  })

  it('新建和旧渠道默认自动补全，完整地址模式保留端点差异', () => {
    const editor = useChannelEditor('images')
    const channel: Channel = { name: '完整地址渠道', serviceType: 'openai', index: 0, baseUrl: 'https://images.example/render/?size=1024%20px', apiKeys: ['fixture-key'], useFullUrl: true }
    expect(editor.form.useFullUrl).toBe(false)
    editor.loadChannelData(channel)
    expect(editor.buildPayload(true)).toMatchObject({ useFullUrl: true, baseUrl: channel.baseUrl })
    editor.form.useFullUrl = false
    expect(editor.buildPayload(true).useFullUrl).toBe(false)
    expect(channel.useFullUrl).toBe(true)
    editor.loadChannelData({ ...channel, useFullUrl: undefined })
    expect(editor.form.useFullUrl).toBe(false)
    editor.loadChannelData(channel)
    editor.resetForm()
    expect(editor.form.useFullUrl).toBe(false)

    const form = defaultChannelForm('images')
    Object.assign(form, { useFullUrl: true, baseUrls: [' https://images.example/render/ ', 'https://images.example/render', 'https://images.example/render/', 'https://backup.example/generate?mode=fast&size=1024%20px'] })
    expect(buildChannelPayload(form, 'images', true).baseUrls).toEqual([
      'https://images.example/render/', 'https://images.example/render', 'https://backup.example/generate?mode=fast&size=1024%20px'
    ])
  })

  it('深拷贝编辑数据并规范化提交字段和渠道状态', () => {
    const channel: Channel = {
      name: '原渠道', serviceType: 'openai', index: 0, baseUrl: 'https://a.example', baseUrls: ['https://a.example', 'https://b.example'],
      apiKeys: ['key'], status: 'suspended', modelMapping: { source: ['target'] }, modelMappingChannels: { source: { target: 'id-1' } },
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

    const form = defaultChannelForm('messages')
    Object.assign(form, {
      name: '  渠道  ', serviceType: 'claude', website: '  ', proxyMode: 'custom', proxyUrl: ' http://proxy.example ',
      baseUrls: [' https://a.example/ ', 'https://a.example#/', '', 'https://b.example///#'], apiKeys: [' ', ' key-with-spaces ', 'key'],
      modelMapping: { ' source ': [' target ', 'target', ''], gone: [' '], ' ': ['unused'] },
      modelMappingChannels: { source: { target: ' id-1 ', removed: 'id-2' }, gone: { stale: 'id-3' } }
    })
    const payload = buildChannelPayload(form, 'messages', false)
    expect(payload).toMatchObject({
      name: '渠道', website: '', proxyUrl: 'http://proxy.example', baseUrl: 'https://a.example', baseUrls: ['https://a.example', 'https://b.example'],
      apiKeys: [' key-with-spaces ', 'key'], modelMapping: { source: ['target'] }, modelMappingChannels: { source: { target: 'id-1' } }
    })
    expect(form.modelMapping[' source ']).toEqual([' target ', 'target', ''])
    expect(payload).not.toHaveProperty('status')

    const single = defaultChannelForm('responses')
    Object.assign(single, { baseUrl: ' https://single.example/# ', status: 'suspended', proxyUrl: 'http://stale.example' })
    expect(buildChannelPayload(single, 'responses', true)).toMatchObject({ baseUrl: 'https://single.example', proxyUrl: '', status: 'suspended', visionCapable: true })
    expect(buildChannelPayload(single, 'responses', true)).not.toHaveProperty('baseUrls')
    expect(buildChannelPayload(single, 'responses', false)).not.toHaveProperty('status')
  })

  it('Images 规范化主模型与 GIF 输出设置，并保留历史帧参数', () => {
    const form = defaultChannelForm('images')
    Object.assign(form, {
      serviceType: 'responses', responsesModel: ' gpt-main ', defaultModel: 'unused', imagesForceNonStream: true,
      lowQuality: true, visionCapable: true, excludeFromConversation: true, visionLayerEnabled: true, visionLayerChannelId: 'id-1',
      visionLayerModel: 'vision', injectDummyThoughtSignature: true, stripThoughtSignature: true, disablePromptCacheKey: true,
      modelMapping: { image: ['image-target'] }
    })
    expect(buildChannelPayload(form, 'images', true, { size: '"1024x1024"' })).toMatchObject({
      responsesModel: 'gpt-main', defaultModel: '', imagesForceNonStream: true, imagesExtraParams: { size: '"1024x1024"' },
      lowQuality: false, visionCapable: false, excludeFromConversation: false, visionLayerEnabled: false, visionLayerChannelId: '',
      visionLayerModel: '', injectDummyThoughtSignature: false, stripThoughtSignature: false, disablePromptCacheKey: false,
      modelMapping: { image: ['image-target'] }
    })

    const editor = useChannelEditor('images')
    const gif: Channel = { name: 'GIF 渠道', serviceType: 'responses', index: 0, baseUrl: 'https://api.example', apiKeys: ['fixture'], imagesOutputFormat: 'gif', imagesFrameCount: 9, imagesFrameDurationMs: 200 }
    editor.loadChannelData(gif)
    editor.form.imagesFrameCount = '12'
    expect(editor.buildPayload(true)).toMatchObject({ imagesOutputFormat: 'gif', imagesFrameCount: 12, imagesFrameDurationMs: 200 })
    expect(gif.imagesFrameCount).toBe(9)
    editor.form.imagesOutputFormat = 'png'
    expect(editor.buildPayload(true)).toMatchObject({ imagesOutputFormat: 'png', imagesFrameCount: 0, imagesFrameDurationMs: 0 })
    editor.form.imagesOutputFormat = ''
    expect(editor.buildPayload(true)).toMatchObject({ imagesOutputFormat: '', imagesFrameCount: 0, imagesFrameDurationMs: 0 })
    editor.resetForm()
    expect(editor.form.imagesOutputFormat).toBe('')
    for (const [frameCount, frameDurationMs] of [[12, 100], [-1, 10]]) {
      editor.loadChannelData({ name: 'GIF 渠道', serviceType: 'responses', index: 0, baseUrl: '', apiKeys: [], imagesOutputFormat: 'gif', imagesFrameCount: frameCount, imagesFrameDurationMs: frameDurationMs })
      expect(editor.form.imagesFrameCount).toBe(frameCount)
      expect(editor.form.imagesFrameDurationMs).toBe(frameDurationMs)
    }
  })
})
