// Images 渠道的上游协议定义。openai 保留已有配置值，对用户显示 Images API。
import type { Channel, ImagesOutputFormat } from '@/types/channel'

export const imagesProtocolOptions = [
  { title: 'Images API', value: 'openai' },
  { title: 'Responses API', value: 'responses' }
] as const

// 表单建议值；旧配置仍由后端保留“未设置即继承”的语义，保存 GIF 时才写入。
export const imagesGIFDefaults = { frameCount: 9, frameDurationMs: 200 } as const

export const imagesOutputFormatOptions: Array<{ title: string; value: ImagesOutputFormat }> = [
  { title: '跟随请求', value: '' },
  { title: '静态图片（PNG）', value: 'png' },
  { title: '静态图片（JPEG）', value: 'jpeg' },
  { title: '静态图片（WebP）', value: 'webp' },
  { title: 'GIF 动图', value: 'gif' },
  { title: 'SVG 矢量图', value: 'svg' }
]

function validateGIFInteger(value: unknown, name: string, minimum: number, maximum?: number): true | string {
  if (value === null || value === '') return true
  if (typeof value !== 'number' && typeof value !== 'string') return `${name}必须是整数`
  const number = Number(value)
  if (!Number.isSafeInteger(number) || number < minimum || (maximum !== undefined && number > maximum)) {
    return maximum !== undefined ? `${name}必须在 ${minimum}–${maximum} 之间` : `${name}不能小于 ${minimum}`
  }
  return true
}

export const validateImagesFrameCount = (value: unknown) => validateGIFInteger(value, 'GIF 帧数', 2, 16)
export const validateImagesFrameDuration = (value: unknown, serviceType: string) =>
  validateGIFInteger(value, 'GIF 帧时长（毫秒）', 20, serviceType === 'responses' ? 2000 : undefined)

export function validateImagesOutputSettings(settings: {
  imagesOutputFormat: string
  imagesFrameCount: unknown
  imagesFrameDurationMs: unknown
  serviceType: string
}): string {
  if (!imagesOutputFormatOptions.some(option => option.value === settings.imagesOutputFormat)) return '请选择支持的输出格式'
  if (settings.imagesOutputFormat !== 'gif') return ''
  const count = validateImagesFrameCount(settings.imagesFrameCount)
  if (count !== true) return count
  const duration = validateImagesFrameDuration(settings.imagesFrameDurationMs, settings.serviceType)
  return duration === true ? '' : duration
}

// 固定图片模型目录；上游查询只用于选择 Responses 主模型。
export const imagesModelOptions = [
  { title: 'gpt-image-2.6-sunburst', value: 'gpt-image-2.6-sunburst' },
  { title: 'gpt-image-2.6-flare', value: 'gpt-image-2.6-flare' },
  { title: 'gpt-image-2.5-sunburst', value: 'gpt-image-2.5-sunburst' },
  { title: 'gpt-image-2.5-flare', value: 'gpt-image-2.5-flare' },
  { title: 'gpt-image-2.5', value: 'gpt-image-2.5' },
  { title: 'gpt-image-2', value: 'gpt-image-2' },
  { title: 'gpt-image-1.5', value: 'gpt-image-1.5' },
  { title: 'gpt-image-1-mini', value: 'gpt-image-1-mini' },
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

export function imagesExpectedRequestUrl(rawUrl: string, serviceType: string, useFullUrl = false): string {
  if (!isImagesServiceType(serviceType)) return ''
  if (useFullUrl) return rawUrl.trim()
  let baseUrl = rawUrl.trim()
  const skipVersion = baseUrl.endsWith('#')
  baseUrl = baseUrl.replace(/[#/]+$/, '')
  if (!skipVersion && !/\/v\d+[a-z]*$/i.test(baseUrl)) baseUrl += '/v1'
  return baseUrl + (serviceType === 'responses' ? '/responses' : '/images/generations')
}
