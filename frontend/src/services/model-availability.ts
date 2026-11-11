import { apiRequest } from '@/services/http'
import type { ApiTab } from '@/services/channel-api'
import type { ModelAvailabilityResponse } from '@/types/model-availability'

export function getModelAvailability(type: ApiTab, signal: AbortSignal) {
  return apiRequest<ModelAvailabilityResponse>(`/model-availability?type=${type}`, { signal })
}
