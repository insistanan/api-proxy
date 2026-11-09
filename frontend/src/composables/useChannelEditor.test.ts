// 表单提交契约：草稿隔离、地址规范化与五协议字段转换。
import { describe, expect, it } from 'vitest'
import { ref } from 'vue'
import { buildChannelPayload, defaultChannelForm, useChannelEditor } from './useChannelEditor'
import type { Channel } from '@/services/api'

describe('渠道草稿与提交', () => {
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

  it.each([undefined, 0])('旧渠道未设置帧参数（%s）时，选择 GIF 自动带入建议值且不改原配置', value => {
    const channel: Channel = {
      name: '旧渠道', serviceType: 'openai', index: 0, baseUrl: 'https://api.example', apiKeys: ['fixture'],
      imagesFrameCount: value, imagesFrameDurationMs: value
    }
    const editor = useChannelEditor('images')
    editor.loadChannelData(channel)
    expect(editor.buildPayload(true)).toMatchObject({ imagesOutputFormat: '', imagesFrameCount: 0, imagesFrameDurationMs: 0 })
    editor.form.imagesOutputFormat = 'gif'
    expect(editor.buildPayload(true)).toMatchObject({ imagesOutputFormat: 'gif', imagesFrameCount: 9, imagesFrameDurationMs: 200 })
    expect(channel.imagesFrameCount).toBe(value)
    expect(channel.imagesFrameDurationMs).toBe(value)
    editor.form.imagesFrameCount = ''
    editor.form.imagesFrameDurationMs = null
    expect(editor.buildPayload(true)).toMatchObject({ imagesFrameCount: 0, imagesFrameDurationMs: 0 })
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
