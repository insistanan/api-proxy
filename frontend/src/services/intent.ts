import { apiRequest, ApiError } from './http'

export function intentErrorText(cause: unknown): string {
  if (cause instanceof ApiError && cause.details && typeof cause.details === 'object' && 'detail' in cause.details && typeof cause.details.detail === 'string') {
    return `${cause.message}：${cause.details.detail}`
  }
  return cause instanceof Error ? cause.message : String(cause)
}

export const INTENT_PROTOCOLS = ['messages', 'responses', 'chat', 'gemini'] as const
export type IntentProtocol = typeof INTENT_PROTOCOLS[number]
export const isIntentProtocol = (value: string): value is IntentProtocol => INTENT_PROTOCOLS.some(p => p === value)

export interface IntentRoute {
  label?: string
  id: string
  poolId: string
  description: string
  patterns: string[]
  examples: string[]
  tier: number
  cost: number
}
export interface IntentConfig {
  taskMatcher?: 'builtin' | 'semantic'
  groups?: IntentGroupCapability[]
  useFeedback?: boolean
  datasetIds?: string[]
  embeddingVersion?: string
  enabled: boolean
  strategy: 'auto' | 'regex' | 'semantic' | 'complexity' | 'adaptive'
  classifier: 'heuristic' | 'llm' | 'decisions' | 'systemone' | 'hybrid'
  classifierChannel: string
  classifierModel: string
  embeddingChannel: string
  embeddingModel: string
  embeddingSource: 'remote' | 'local'
  queryPrefix: string
  examplePrefix: string
  threshold: number
  timeoutMs: number
  minResidenceSeconds: number
  minTurns: number
  switchMargin: number
  unmatched: 'original' | 'error' | 'fallback'
  fallbackPoolId: string
  failurePolicy: 'error' | 'keep'
  observeOnly: boolean
  routes: IntentRoute[]
}
export interface IntentGroupCapability { poolId: string; tasks: string[]; tiers: number[]; cost: number }
export interface IntentTask { id: string; name: string; description: string }
export interface IntentGroupMatch {
  poolId: string
  name: string
  tasks: string[] | null
  tiers: number[] | null
  status: 'skipped' | 'disabled' | 'unconfigured' | 'excluded' | 'candidate' | 'selected'
  reason: string
  cost: number
  selectionScore?: number
  good?: number
  bad?: number
}
export interface IntentTrace {
  input: string
  truncated: boolean
  classified: boolean
  strategy: string
  taskSource?: string
  tierSource?: string
  taskEvidence?: string
  tierEvidence?: string
  suggestedPoolId?: string
  selection?: string
  error?: string
  groups: IntentGroupMatch[]
  semantic?: { datasetId: string; revision: number; sampleId: string; label: string; negative: boolean; score: number }[]
  threshold?: number
  switchMargin?: number
}
export interface IntentDecision { trace?: IntentTrace; task?: string; poolId: string; routeId: string; reason: string; score: number; tier: number; durationMs: number }

// 失败仍保留后端返回的诊断快照，不在前端重跑匹配。
export function intentPreviewFailure(cause: unknown): IntentDecision | undefined {
  if (cause instanceof ApiError && cause.code === 'INTENT_CLASSIFIER_FAILED' && cause.details && typeof cause.details === 'object' && 'decision' in cause.details) {
    return cause.details.decision as IntentDecision
  }
  return undefined
}
export interface IntentEvent {
  status?: 'pending' | 'failed' | 'delivered'
  control?: boolean
  requestId: string
  protocol: IntentProtocol
  conversationId: string
  decision: IntentDecision
  target: { poolId: string; channelId: string; model: string }
  at: string
  success: boolean
  feedback?: boolean
}
export interface LocalManifest {
  runtimeUrl: string
  runtimeSha256: string
  executable: string
  modelUrl: string
  modelSha256: string
  modelName: string
  pooling: string
}
export interface LocalStatus { installed: boolean; running: boolean; ready: boolean; busy: boolean; error: string; platform: string; manifest: LocalManifest }
export interface IntentOptions {
  tasks?: IntentTask[]
  classifiers: { kind: string; id: string; name: string; defaultModel: string; models: string[] }[]
  embeddings: { id: string; name: string }[]
}

export const intentApi = {
	buildIndex: (protocol: IntentProtocol, config: IntentConfig) => apiRequest(`/intent/${protocol}/index`, { method: 'POST', body: JSON.stringify(config) }),
  get: (protocol: IntentProtocol) => apiRequest<IntentConfig>(`/intent/${protocol}`),
  save: (protocol: IntentProtocol, config: IntentConfig) => apiRequest<IntentConfig>(`/intent/${protocol}`, { method: 'PUT', body: JSON.stringify(config) }),
  preview: (protocol: IntentProtocol, config: IntentConfig, text: string) => apiRequest<IntentDecision>(`/intent/${protocol}/preview`, { method: 'POST', body: JSON.stringify({ config, text }) }),
  events: (protocol: IntentProtocol) => apiRequest<IntentEvent[]>(`/intent/${protocol}/events`),
  feedback: (protocol: IntentProtocol, requestId: string, good: boolean) => apiRequest(`/intent/${protocol}/feedback`, { method: 'POST', body: JSON.stringify({ requestId, good }) }),
  options: () => apiRequest<IntentOptions>('/intent/options'),
  local: () => apiRequest<LocalStatus>('/intent/local'),
  install: (manifest: LocalManifest) => apiRequest<LocalStatus>('/intent/local/install', { method: 'POST', body: JSON.stringify(manifest) }),
  localAction: (action: 'start' | 'stop') => apiRequest<LocalStatus>(`/intent/local/${action}`, { method: 'POST' })
}
