// 上游最终请求地址预览：与后端 upstreamreq.BuildUpstreamURL 的版本前缀规则保持一致。
import type { ApiTab } from '@/types/channel'

const VERSION_SUFFIX_PATTERN = /\/v\d+[a-z]*$/

export interface UpstreamEndpointRule {
  /** 自动补全时插入的版本前缀，例如 /v1、/v1beta。 */
  versionPrefix: string
  /** 版本前缀之后的接口路径。 */
  endpoint: string
}

/**
 * 入口协议在未选择上游服务类型时的默认协议，用于新建渠道的地址预览。
 * 与 useChannelModal 的默认服务类型保持一致。
 */
export function defaultUpstreamServiceType(channelType: ApiTab | string): string {
  if (channelType === 'messages') return 'claude'
  if (channelType === 'responses') return 'responses'
  if (channelType === 'gemini') return 'gemini'
  return 'openai'
}

/**
 * 按入口协议与上游服务类型推导网关实际请求的接口路径。
 * 规则与后端各协议 handler 一致：Claude 走 /messages，OpenAI 兼容走 /chat/completions，
 * Responses 走 /responses，Gemini 走 /models/{model}:generateContent。
 */
export function resolveUpstreamEndpoint(
  channelType: ApiTab | string,
  serviceType: string,
  model?: string
): UpstreamEndpointRule {
  if (channelType === 'embeddings') return { versionPrefix: '/v1', endpoint: '/embeddings' }
  if (channelType === 'decisions') return { versionPrefix: '/v1', endpoint: '/decisions' }
  if (channelType === 'images') {
    return serviceType === 'responses'
      ? { versionPrefix: '/v1', endpoint: '/responses' }
      : { versionPrefix: '/v1', endpoint: '/images/generations' }
  }

  const upstream = serviceType || defaultUpstreamServiceType(channelType)
  if (upstream === 'claude') return { versionPrefix: '/v1', endpoint: '/messages' }
  if (upstream === 'responses') return { versionPrefix: '/v1', endpoint: '/responses' }
  if (upstream === 'gemini') {
    const trimmed = (model || '').trim()
    const modelPath = trimmed.startsWith('models/') || trimmed.startsWith('tunedModels/')
      ? trimmed
      : trimmed ? `models/${trimmed}` : 'models/{模型}'
    return { versionPrefix: '/v1beta', endpoint: `/${modelPath}:generateContent` }
  }
  return { versionPrefix: '/v1', endpoint: '/chat/completions' }
}

/**
 * 拼接最终请求地址。
 * - useFullUrl 为 true 时按填写内容原样使用，不做任何补全；
 * - BaseURL 以 "#" 结尾时剥离 "#" 并跳过版本前缀；
 * - BaseURL 已带版本号段（/v1、/v1beta 等）时直接追加接口路径；
 * - 其余情况在接口路径前插入版本前缀。
 */
export function buildUpstreamPreviewUrl(
  baseUrl: string,
  versionPrefix: string,
  endpoint: string,
  useFullUrl = false
): string {
  if (useFullUrl) return baseUrl
  const skipVersionPrefix = baseUrl.endsWith('#')
  let url = skipVersionPrefix ? baseUrl.slice(0, -1) : baseUrl
  url = url.replace(/\/+$/, '')
  const path = `/${endpoint.replace(/^\/+/, '')}`
  if (skipVersionPrefix || VERSION_SUFFIX_PATTERN.test(url)) return `${url}${path}`
  return `${url}${versionPrefix}${path}`
}

export interface EndpointPreviewOptions {
  baseUrls: string[]
  channelType: ApiTab | string
  serviceType: string
  model?: string
  useFullUrl?: boolean
}

/** 为每个 BaseURL 生成最终请求地址；完整地址模式不产生预览。 */
export function buildEndpointPreviews(options: EndpointPreviewOptions): string[] {
  if (options.useFullUrl) return []
  const { versionPrefix, endpoint } = resolveUpstreamEndpoint(options.channelType, options.serviceType, options.model)
  return options.baseUrls
    .map(url => url.trim())
    .filter(Boolean)
    .map(url => buildUpstreamPreviewUrl(url, versionPrefix, endpoint))
}
