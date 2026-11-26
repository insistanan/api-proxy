// 模型列表类型：本地聚合与上游探测。

import type { Channel } from '@/types/channel'

// ============== 上游模型列表类型 ==============

export interface ModelEntry {
  id: string
  object: string
  created: number
  owned_by: string
}

export interface ModelsResponse {
  object: string
  data: ModelEntry[]
}

export interface UpstreamModelsRequest {
  baseUrl: string
  baseUrls?: string[]
  apiKey?: string
  serviceType?: string
  insecureSkipVerify?: boolean
  proxyMode?: Channel['proxyMode']
  proxyUrl?: string
}
