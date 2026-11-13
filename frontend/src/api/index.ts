// API 门面：组合各功能域的 service，页面与 Store 统一从 @/api 取接口与类型。
import { channelsApi } from '@/features/channels/services/channels'
import { observabilityApi } from '@/features/observability/services/observability'
import { securityGateApi } from '@/features/observability/services/security-gate'
import { assistantApi } from '@/features/assistant/services/assistant'
import { settingsApi } from '@/features/settings/services/settings'
import { clientConfigApi } from '@/features/clients/services/client-config'
import { skillsApi } from '@/features/extensions/skills/services/skills'
import { mcpApi } from '@/features/extensions/mcp/services/mcp'
import type { ApiTab, ModelsResponse, UpstreamModelsRequest } from '@/types'
import type { ChannelApi } from '@/features/channels/services/channel-api'

export { ApiError } from '@/shared/services/http'
export { fetchHealth, type HealthResponse } from '@/shared/services/health'
export { testChannel, testChannelWithModel, type TestChannelContext } from '@/features/channels/services/channel-test'
export { API_TABS } from '@/features/channels/services/channel-api'
export type { ChannelApi } from '@/features/channels/services/channel-api'
export * from '@/types'

export const api = {
  ...channelsApi,
  ...observabilityApi,
  ...securityGateApi,
  ...assistantApi,
  ...settingsApi,
  ...clientConfigApi,
  ...skillsApi,
  ...mcpApi
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
