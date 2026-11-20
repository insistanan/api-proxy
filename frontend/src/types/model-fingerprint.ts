export interface FingerprintSample {
  index: number
  state: 'waiting' | 'complete' | 'truncated' | 'failed' | 'cancelled'
  prompt: string
  text: string
  expectedCount: number
  count: number
  durationMs: number
  responseModel?: string
  error?: string
}

export interface FingerprintAnalysis {
  decision: 'scored' | 'partial' | 'unscorable' | 'not_model'
  used_outputs: number
  prediction_name: string
  probability_status: string
  results: Array<{
    model: string
    display_name: string
    family_name: string
    score: number
    probability: number | null
  }>
  evidence: { reason: string; method?: string }
}

export interface FingerprintEvent {
  type: 'loading' | 'ready' | 'sample' | 'result' | 'error'
  target?: {
    channelId: string
    channelName: string
    protocol: string
    model: string
    urlIndex: number
    keyIndex: number
    compatibility: boolean
    effort: string
    configDigest?: string
  }
  sample?: FingerprintSample
  analysis?: FingerprintAnalysis
  dataBuilt?: string
  dataSha256?: string
  error?: string
}

export interface FingerprintTask {
  id: string
  status: 'loading' | 'running' | 'completed' | 'failed' | 'cancelled' | 'interrupted'
  target: NonNullable<FingerprintEvent['target']>
  samples: FingerprintSample[]
  analysis?: FingerprintAnalysis
  dataBuilt?: string
  dataSha256?: string
  detectorSha256?: string
  sdkVersion?: string
  error?: string
  createdAt: string
  updatedAt: string
}
