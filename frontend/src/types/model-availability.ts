export type AvailabilityState = 'unknown' | 'healthy' | 'degraded' | 'cooling' | 'unsupported'

export interface AvailabilityCounts {
  success: number
  failure: number
  unsupported: number
  credential: number
  rateLimited: number
  ignored: number
}

export interface AvailabilityCandidate extends AvailabilityCounts {
  id: string
  label: string
  endpoint: string
  protocol: string
  state: AvailabilityState
  lastSeen: number
  lastOutcome: string
  lastReason: string
  cooldownUntil: number
  recent: AvailabilityCounts
}

export interface ModelAvailabilityRow extends AvailabilityCounts {
  channelId: string
  channelName: string
  model: string
  state: AvailabilityState
  lastSeen: number
  recent: AvailabilityCounts
  buckets: Array<AvailabilityCounts & { from: number }>
  candidates: AvailabilityCandidate[]
}

export interface ModelAvailabilityResponse {
  rows: ModelAvailabilityRow[]
  generatedAt: number
  minSamples: number
}
