import { describe, expect, it } from 'vitest'
import { ref } from 'vue'
import { useChannelEditor } from '@/composables/useChannelEditor'
import { channelServiceLabel, imagesExpectedRequestUrl, imagesModelOptions, isImagesServiceType } from './imagesChannelProtocol'
import { defaultChannelForm } from '@/composables/useChannelEditor'
import { parseQuickInput } from './quickInputParser'

describe('Images 渠道协议管理', () => {
  it('切换分组后按当前类型重置协议和模型草稿', () => {
    const type = ref('messages')
    const editor = useChannelEditor(type)
    type.value = 'images'
    editor.resetForm()
    expect(editor.form.serviceType).toBe('openai')
    editor.form.responsesModel = 'gpt-4.1'
    editor.resetForm()
    expect(editor.form.responsesModel).toBe('')
  })
  it('只提供图片生成支持的两种上游协议', () => {
    expect(isImagesServiceType('openai')).toBe(true)
    expect(isImagesServiceType('responses')).toBe(true)
    expect(isImagesServiceType('claude')).toBe(false)
    expect(channelServiceLabel('images', 'openai')).toBe('Images API')
    expect(channelServiceLabel('images', 'responses')).toBe('Responses API')
    expect(channelServiceLabel('messages', 'openai')).toBe('openai')
    expect(imagesModelOptions.map(option => option.value)).toContain('gpt-image-2.5-sunburst')
    expect(imagesModelOptions.map(option => option.value)).toContain('gpt-image-2.5')
    expect(defaultChannelForm('images').serviceType).toBe('openai')
  })

  it('根据上游协议预览真实请求端点，保留版本前缀与井号语义', () => {
    expect(imagesExpectedRequestUrl('https://api.example.com/', 'openai')).toBe('https://api.example.com/v1/images/generations')
    expect(imagesExpectedRequestUrl('https://api.example.com/v1/', 'responses')).toBe('https://api.example.com/v1/responses')
    expect(imagesExpectedRequestUrl('https://api.example.com/proxy#', 'responses')).toBe('https://api.example.com/proxy/responses')
    expect(imagesExpectedRequestUrl('https://api.example.com', 'claude')).toBe('')
  })

  it.each(['generations', 'edits', 'variations'])('快速添加能识别 Images 的 %s 端点', endpoint => {
    const result = parseQuickInput(`https://api.example.com/v1/images/${endpoint} sk-test-key`)
    expect(result.detectedBaseUrl).toBe('https://api.example.com/v1')
    expect(result.detectedServiceType).toBe('openai')
  })
})
