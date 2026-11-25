import { apiRequest } from '@/shared/services/http'

export const FREE_POOL_ID = 'builtin-free'

export interface FreeChannelsConfig {
  kilo: {
    enabled: boolean
    maxBranchConcurrency?: number
    models: string[]
    syncedAt?: string
  }
}

export const freeChannelsApi = {
  get: () => apiRequest<FreeChannelsConfig>('/free-channels'),
  setBranchConcurrency: (maxBranchConcurrency: number) => apiRequest<FreeChannelsConfig>('/free-channels/kilo', {
    method: 'PUT', body: JSON.stringify({ maxBranchConcurrency })
  }),
  setEnabled: (enabled: boolean) => apiRequest<FreeChannelsConfig>('/free-channels/kilo', {
    method: 'PUT', body: JSON.stringify({ enabled })
  }),
  syncModels: () => apiRequest<FreeChannelsConfig>('/free-channels/kilo/models/sync', { method: 'POST' })
}
