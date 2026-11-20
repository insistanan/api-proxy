// 表单提交契约：草稿隔离、地址规范化与五协议字段转换。
import { describe, expect, it } from 'vitest'
import { buildChannelPayload, defaultChannelForm, useChannelEditor } from '@/features/channels/composables/useChannelEditor'
import type { Channel } from '@/api'

describe('复制渠道草稿', () => {
  const source: Channel = {
    id: 'source-id', name: '供应商A', index: 3, poolId: 'source-pool', status: 'disabled',
    serviceType: 'openai', baseUrl: 'https://api.example.com/v1', baseUrls: ['https://api.example.com/v1', 'https://backup.example.com/v1'],
    apiKeys: ['sk-fixture-key'], proxyMode: 'custom', proxyUrl: 'http://localhost:8080',
    modelMapping: { '*': ['local-model', 'external-model'], sonnet: ['local-model'] },
    modelMappingChannels: { '*': { 'external-model': 'other-channel' } }, reasoningEffort: 'high',
    visionLayerEnabled: true, visionLayerChannelId: 'vision-channel'
  }

  it('同协议复制保留配置，默认进入兜底分组，并隔离原渠道数据', () => {
    const editor = useChannelEditor('messages')
    editor.loadChannelCopy(source, 'messages')
    expect(editor.form).toMatchObject({
      name: '供应商A (副本)', poolId: 'default', status: 'active', reasoningEffort: 'high',
      apiKeys: source.apiKeys, modelMapping: source.modelMapping, modelMappingChannels: source.modelMappingChannels
    })
    editor.form.apiKeys.push('sk-new-key')
    editor.form.modelMapping['*'].push('new-model')
    editor.form.modelMappingChannels['*']['external-model'] = 'new-channel'
    expect(source.apiKeys).toEqual(['sk-fixture-key'])
    expect(source.modelMapping?.['*']).toEqual(['local-model', 'external-model'])
    expect(source.modelMappingChannels?.['*']['external-model']).toBe('other-channel')
    expect(editor.buildPayload(false)).not.toHaveProperty('id')
    expect(editor.buildPayload(false)).not.toHaveProperty('status')
  })

  it('跨协议复制保留供应商连接、代理与本地模型，清理原协议的渠道引用', () => {
    const editor = useChannelEditor('responses')
    editor.loadChannelCopy(source, 'messages')
    expect(editor.form).toMatchObject({
      baseUrls: source.baseUrls, apiKeys: source.apiKeys, proxyMode: 'custom', proxyUrl: source.proxyUrl,
      poolId: 'default', visionCapable: true, visionLayerEnabled: false, visionLayerChannelId: '',
      reasoningEffort: 'inherit', modelMappingChannels: {}, modelMapping: { '*': ['local-model'], sonnet: ['local-model'] }
    })
  })

  it.each(['images', 'embeddings', 'decisions'] as const)('复制到 %s 时使用该协议的模型与专属配置', protocol => {
    const editor = useChannelEditor(protocol)
    editor.loadChannelCopy(source, 'messages')
    expect(editor.form.modelMapping).toEqual({})
    expect(editor.form.modelMappingChannels).toEqual({})
    expect(editor.form.visionLayerEnabled).toBe(false)
    expect(editor.form.embeddingsDimensions).toBe(1024)
    expect(editor.form.responsesModel).toBe('')
  })

  it('Chat 默认模型与唯一的本地通配映射可以相互转换', () => {
    const chat = useChannelEditor('chat')
    chat.loadChannelCopy(source, 'messages')
    expect(chat.form.defaultModel).toBe('local-model')
    const messages = useChannelEditor('messages')
    messages.loadChannelCopy({ ...source, modelMapping: {}, modelMappingChannels: {}, defaultModel: 'chat-model' }, 'chat')
    expect(messages.form.modelMapping).toEqual({ '*': ['chat-model'] })
    chat.loadChannelCopy({ ...source, modelMapping: { '*': ['a', 'b'] }, modelMappingChannels: {} }, 'messages')
    expect(chat.form.defaultModel).toBe('')
  })
})

describe('渠道草稿与提交', () => {
  it('渠道思考覆盖可编辑、清除，并且不会改动原渠道或串入非对话协议', () => {
    const editor = useChannelEditor('messages')
    const channel: Channel = {
      name: '思考渠道',
      serviceType: 'gemini',
      baseUrl: '',
      apiKeys: [],
      index: 0,
      reasoningEffort: 'max'
    }
    editor.loadChannelData(channel)
    expect(editor.buildPayload(true).reasoningEffort).toBe('max')
    editor.form.reasoningEffort = 'none'
    expect(editor.buildPayload(true).reasoningEffort).toBe('none')
    editor.form.reasoningEffort = 'auto'
    expect(editor.buildPayload(true).reasoningEffort).toBe('auto')
    editor.loadChannelData({ ...channel, reasoningEffort: 'auto' })
    expect(editor.form.reasoningEffort).toBe('auto')
    expect(channel.reasoningEffort).toBe('max')
    editor.resetForm()
    expect(editor.buildPayload(true).reasoningEffort).toBe('inherit')
    for (const protocol of ['images', 'embeddings', 'decisions']) {
      const form = defaultChannelForm(protocol)
      form.reasoningEffort = 'high'
      expect(buildChannelPayload(form, protocol, false).reasoningEffort).toBe('inherit')
    }
  })
  it('思考输出默认保留，编辑和重置不会串改渠道配置', () => {
    const editor = useChannelEditor('messages')
    const channel: Channel = {
      name: '推理渠道', serviceType: 'openai', baseUrl: '', apiKeys: [], index: 0,
      reasoningOutputMode: 'hide', parseEmbeddedReasoning: true, requireReasoningContent: true
    }
    expect(editor.form.reasoningOutputMode).toBe('preserve')
    editor.loadChannelData(channel)
    expect(editor.buildPayload(true)).toMatchObject({
      reasoningOutputMode: 'hide', parseEmbeddedReasoning: true
    })
    // 后端自动识别的严格能力不属于编辑草稿，保存时省略，避免覆盖服务端新结果。
    expect(editor.buildPayload(true)).not.toHaveProperty('requireReasoningContent')
    expect(channel.requireReasoningContent).toBe(true)
    editor.form.reasoningOutputMode = 'preserve'
    editor.form.parseEmbeddedReasoning = false
    expect(channel.reasoningOutputMode).toBe('hide')
    expect(editor.buildPayload(true)).toMatchObject({ reasoningOutputMode: 'preserve', parseEmbeddedReasoning: false })
    editor.resetForm()
    expect(editor.buildPayload(false)).toMatchObject({
      reasoningOutputMode: 'preserve', parseEmbeddedReasoning: false
    })
    expect(editor.buildPayload(false)).not.toHaveProperty('requireReasoningContent')
  })

  it('新建和旧渠道默认自动补全，完整地址设置可保存、载入和清除', () => {
    const editor = useChannelEditor('images')
    const channel: Channel = {
      name: '完整地址渠道', serviceType: 'openai', index: 0,
      baseUrl: 'https://images.example/render/?size=1024%20px', apiKeys: ['fixture-key'], useFullUrl: true
    }
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
  })

  it('完整地址模式保留末尾斜杠、查询参数和多地址的路径差异', () => {
    const form = defaultChannelForm('images')
    Object.assign(form, {
      useFullUrl: true,
      baseUrls: [' https://images.example/render/ ', 'https://images.example/render',
        'https://images.example/render/', 'https://backup.example/generate?mode=fast&size=1024%20px']
    })
    expect(buildChannelPayload(form, 'images', true).baseUrls).toEqual([
      'https://images.example/render/', 'https://images.example/render',
      'https://backup.example/generate?mode=fast&size=1024%20px'
    ])
  })

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

  it('输出配置可载入、修改和清除，并保持草稿隔离', () => {
    const editor = useChannelEditor('images')
    const channel: Channel = {
      name: 'GIF 渠道', serviceType: 'responses', index: 0, baseUrl: 'https://api.example', apiKeys: ['fixture'],
      imagesOutputFormat: 'gif', imagesFrameCount: 9, imagesFrameDurationMs: 200
    }
    editor.loadChannelData(channel)
    editor.form.imagesFrameCount = '12'
    expect(editor.buildPayload(true)).toMatchObject({ imagesOutputFormat: 'gif', imagesFrameCount: 12, imagesFrameDurationMs: 200 })
    expect(channel.imagesFrameCount).toBe(9)
    editor.form.imagesOutputFormat = 'png'
    expect(editor.buildPayload(true)).toMatchObject({ imagesOutputFormat: 'png', imagesFrameCount: 0, imagesFrameDurationMs: 0 })
    editor.form.imagesOutputFormat = ''
    expect(editor.buildPayload(true)).toMatchObject({ imagesOutputFormat: '', imagesFrameCount: 0, imagesFrameDurationMs: 0 })
    editor.resetForm()
    expect(editor.form.imagesOutputFormat).toBe('')
  })

  it.each([[12, 100], [-1, 10]])('已配置的帧参数 %s/%s 原样载入，不被建议值覆盖', (frameCount, frameDurationMs) => {
    const editor = useChannelEditor('images')
    editor.loadChannelData({
      name: 'GIF 渠道', serviceType: 'responses', index: 0, baseUrl: '', apiKeys: [],
      imagesOutputFormat: 'gif', imagesFrameCount: frameCount, imagesFrameDurationMs: frameDurationMs
    })
    expect(editor.form.imagesFrameCount).toBe(frameCount)
    expect(editor.form.imagesFrameDurationMs).toBe(frameDurationMs)
  })
})
