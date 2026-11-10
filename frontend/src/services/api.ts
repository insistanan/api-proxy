// API 门面：组合领域服务，保留现有导入路径与方法名。
import { channelsApi } from './channels'
import { observabilityApi } from './observability'
import { assistantApi } from './assistant'
import { settingsApi } from './settings'
import { clientConfigApi } from './client-config'
import { skillsApi } from './skills'
import type { ModelsResponse, UpstreamModelsRequest } from '@/types'
import type { ApiTab, ChannelApi } from './channel-api'

export { ApiError } from './http'
export { fetchHealth, type HealthResponse } from './health'
export { testChannel, testChannelWithModel, type TestChannelContext } from './channel-test'
export type { ApiTab, ChannelApi } from './channel-api'
export * from '@/types'

export const api = {
  ...channelsApi,
  ...observabilityApi,
  ...assistantApi,
  ...settingsApi,
  ...clientConfigApi,
  ...skillsApi
}

export function fetchUpstreamModels(
  baseUrl: string,
  apiKey: string = '',
  serviceType?: string,
  options: Omit<UpstreamModelsRequest, 'baseUrl' | 'apiKey' | 'serviceType'> = {}
): Promise<ModelsResponse> {
  return api.discoverUpstreamModels({
    baseUrl,
    apiKey,
    serviceType,
    ...options
  })
}

export function channelApiByType(type: ApiTab): ChannelApi {
  return api.channelApi(type)
}

export default api
