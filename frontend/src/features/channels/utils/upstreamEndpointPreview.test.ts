import { describe, expect, it } from 'vitest'
import {
  buildEndpointPreviews,
  buildUpstreamPreviewUrl,
  resolveUpstreamEndpoint
} from '@/features/channels/utils/upstreamEndpointPreview'

describe('上游最终请求地址预览', () => {
  it('按上游服务类型推导接口路径', () => {
    expect(resolveUpstreamEndpoint('messages', 'claude')).toEqual({ versionPrefix: '/v1', endpoint: '/messages' })
    expect(resolveUpstreamEndpoint('messages', 'openai')).toEqual({ versionPrefix: '/v1', endpoint: '/chat/completions' })
    expect(resolveUpstreamEndpoint('responses', 'responses')).toEqual({ versionPrefix: '/v1', endpoint: '/responses' })
    expect(resolveUpstreamEndpoint('chat', 'openai')).toEqual({ versionPrefix: '/v1', endpoint: '/chat/completions' })
    expect(resolveUpstreamEndpoint('embeddings', 'openai')).toEqual({ versionPrefix: '/v1', endpoint: '/embeddings' })
    expect(resolveUpstreamEndpoint('decisions', 'openai')).toEqual({ versionPrefix: '/v1', endpoint: '/decisions' })
    expect(resolveUpstreamEndpoint('images', 'openai')).toEqual({ versionPrefix: '/v1', endpoint: '/images/generations' })
    expect(resolveUpstreamEndpoint('images', 'responses')).toEqual({ versionPrefix: '/v1', endpoint: '/responses' })
  })

  it('未选择上游服务类型时按入口协议取默认值', () => {
    expect(resolveUpstreamEndpoint('messages', ''))
      .toEqual({ versionPrefix: '/v1', endpoint: '/messages' })
    expect(resolveUpstreamEndpoint('responses', ''))
      .toEqual({ versionPrefix: '/v1', endpoint: '/responses' })
    expect(resolveUpstreamEndpoint('gemini', '', 'gemini-2.5-flash'))
      .toEqual({ versionPrefix: '/v1beta', endpoint: '/models/gemini-2.5-flash:generateContent' })
    expect(resolveUpstreamEndpoint('chat', ''))
      .toEqual({ versionPrefix: '/v1', endpoint: '/chat/completions' })
  })

  it('Gemini 使用 /v1beta 并补齐 models 前缀', () => {
    expect(resolveUpstreamEndpoint('gemini', 'gemini', 'gemini-2.5-pro'))
      .toEqual({ versionPrefix: '/v1beta', endpoint: '/models/gemini-2.5-pro:generateContent' })
    expect(resolveUpstreamEndpoint('gemini', 'gemini', 'models/gemini-2.5-pro'))
      .toEqual({ versionPrefix: '/v1beta', endpoint: '/models/gemini-2.5-pro:generateContent' })
    expect(resolveUpstreamEndpoint('gemini', 'gemini', ''))
      .toEqual({ versionPrefix: '/v1beta', endpoint: '/models/{模型}:generateContent' })
  })

  it('无版本号时插入版本前缀，已有版本号时直接追加路径', () => {
    expect(buildUpstreamPreviewUrl('https://cdn.shuaiapi.com', '/v1', '/messages'))
      .toBe('https://cdn.shuaiapi.com/v1/messages')
    expect(buildUpstreamPreviewUrl('https://api.openai.com/v1/', '/v1', '/chat/completions'))
      .toBe('https://api.openai.com/v1/chat/completions')
    expect(buildUpstreamPreviewUrl('https://api.example.com/v1beta', '/v1', '/messages'))
      .toBe('https://api.example.com/v1beta/messages')
  })

  it('以 # 结尾时跳过版本前缀', () => {
    expect(buildUpstreamPreviewUrl('https://api.example.com/custom/#', '/v1', '/messages'))
      .toBe('https://api.example.com/custom/messages')
  })

  it('完整地址模式原样使用且不产生预览', () => {
    expect(buildUpstreamPreviewUrl('https://api.example.com/raw', '/v1', '/messages', true))
      .toBe('https://api.example.com/raw')
    expect(buildEndpointPreviews({
      baseUrls: ['https://api.example.com'], channelType: 'messages', serviceType: 'claude', useFullUrl: true
    })).toEqual([])
  })

  it('多行地址逐个拼接并忽略空行', () => {
    expect(buildEndpointPreviews({
      baseUrls: ['https://a.example.com', '  ', 'https://b.example.com/v1/'],
      channelType: 'messages',
      serviceType: 'claude'
    })).toEqual([
      'https://a.example.com/v1/messages',
      'https://b.example.com/v1/messages'
    ])
  })
})
