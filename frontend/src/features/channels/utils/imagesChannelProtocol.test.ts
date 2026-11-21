import { describe, expect, it } from 'vitest'
import { ref } from 'vue'
import { useChannelEditor, defaultChannelForm } from '@/features/channels/composables/useChannelEditor'
import { imagesExpectedRequestUrl, validateImagesOutputSettings } from '@/features/channels/utils/imagesChannelProtocol'
import { parseQuickInput } from '@/features/channels/utils/quickInputParser'

describe('Images 渠道协议管理', () => {
  it('合并验证协议草稿、端点预览、完整地址和 GIF 参数校验', () => {
    const type = ref('messages')
    const editor = useChannelEditor(type)
    type.value = 'images'
    editor.resetForm()
    expect(editor.form.serviceType).toBe('openai')
    editor.form.responsesModel = 'gpt-4.1'
    editor.resetForm()
    expect(editor.form.responsesModel).toBe('')

    expect(imagesExpectedRequestUrl('https://api.example.com/', 'openai')).toBe('https://api.example.com/v1/images/generations')
    expect(imagesExpectedRequestUrl('https://api.example.com/v1/', 'responses')).toBe('https://api.example.com/v1/responses')
    expect(imagesExpectedRequestUrl('https://api.example.com/proxy#', 'responses')).toBe('https://api.example.com/proxy/responses')
    expect(imagesExpectedRequestUrl('https://api.example.com', 'claude')).toBe('')

    const url = 'https://api.example.com/images/generations/?size=1024%20px&mode=fast'
    expect(imagesExpectedRequestUrl(url, 'openai', true)).toBe(url)
    expect(imagesExpectedRequestUrl(url, 'responses', true)).toBe(url)
    const quick = parseQuickInput(`name: 图片渠道\nBASE_URL="${url}"\nsk-test-key`, { useFullUrl: true })
    expect(quick).toMatchObject({ detectedBaseUrl: url, detectedServiceType: 'openai', detectedApiKeys: ['sk-test-key'], detectedChannelName: '图片渠道' })
    expect(parseQuickInput('https://api.example.com/render/', { useFullUrl: true }).detectedBaseUrl).toBe('https://api.example.com/render/')

    const form = defaultChannelForm('images')
    form.imagesOutputFormat = 'gif'
    expect(validateImagesOutputSettings(form)).toBe('')
    form.imagesFrameCount = '2.5'
    expect(validateImagesOutputSettings(form)).toContain('帧数')
    form.imagesFrameCount = null
    form.imagesFrameDurationMs = 3000
    expect(validateImagesOutputSettings(form)).toBe('')
    form.serviceType = 'responses'
    expect(validateImagesOutputSettings(form)).toContain('20–2000')
    form.imagesFrameDurationMs = ''
    expect(validateImagesOutputSettings(form)).toBe('')
  })
})
