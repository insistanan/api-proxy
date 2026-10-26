// Images 渠道的上游协议定义。openai 保留已有配置值，对用户显示 Images API。
import type { Channel } from '@/types/channel'

export const imagesProtocolOptions = [
  { title: 'Images API', value: 'openai' },
  { title: 'Responses API', value: 'responses' }
] as const

// 固定图片模型目录；上游查询只用于选择 Responses 主模型。
export const imagesModelOptions = [
  { title: 'gpt-image-2.5-sunburst', value: 'gpt-image-2.5-sunburst' },
  { title: 'gpt-image-2.5-flare', value: 'gpt-image-2.5-flare' },
  { title: 'gpt-image-2.5', value: 'gpt-image-2.5' },
  { title: 'gpt-image-2', value: 'gpt-image-2' },
  { title: 'gpt-image-1.5', value: 'gpt-image-1.5' },
  { title: 'gpt-image-1-mini', value: 'gpt-image-1-mini' },
  { title: 'gpt-image-1', value: 'gpt-image-1' },
  { title: 'chatgpt-image-latest', value: 'chatgpt-image-latest' }
] as const

export function isImagesServiceType(value: string): value is 'openai' | 'responses' {
  return value === 'openai' || value === 'responses'
}

export function imagesProtocolLabel(value: string): string {
  return imagesProtocolOptions.find(option => option.value === value)?.title || `不支持的图片协议（${value || '未选择'}）`
}

export function channelServiceLabel(channelType: string, serviceType: Channel['serviceType']): string {
  return channelType === 'images' ? imagesProtocolLabel(serviceType) : serviceType
}

export function imagesExpectedRequestUrl(rawUrl: string, serviceType: string): string {
  if (!isImagesServiceType(serviceType)) return ''
  let baseUrl = rawUrl.trim()
  const skipVersion = baseUrl.endsWith('#')
  baseUrl = baseUrl.replace(/[#/]+$/, '')
  if (!skipVersion && !/\/v\d+[a-z]*$/i.test(baseUrl)) baseUrl += '/v1'
  return baseUrl + (serviceType === 'responses' ? '/responses' : '/images/generations')
}
