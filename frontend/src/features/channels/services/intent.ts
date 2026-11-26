import { apiRequest, ApiError } from '@/shared/services/http'
import type { ChannelSelectionOption } from '@/types/channel-selection'

export const INTENT_PROTOCOLS = ['messages', 'responses', 'chat', 'gemini'] as const
export type IntentProtocol = (typeof INTENT_PROTOCOLS)[number]
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
  recognitionMode?: 'builtin' | 'semantic' | 'assistant' | 'hybrid'
  hybridMatchers?: ('builtin' | 'semantic' | 'assistant')[]
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
export interface IntentGroupCapability {
  poolId: string
  tasks: string[]
  tiers: number[]
  cost: number
}
export interface IntentTask {
  id: string
  name: string
  description: string
}
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
  stages?: { stage: string; matched: boolean; task?: string }[]
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
  semantic?: {
    datasetId: string
    revision: number
    sampleId: string
    label: string
    negative: boolean
    score: number
  }[]
  threshold?: number
  switchMargin?: number
}
export interface IntentDecision {
  routing?: {
    requestedModel: string
    originalPools: string[]
    selectedPools: string[]
    candidates: { channel: string; model: string }[]
  }
  trace?: IntentTrace
  task?: string
  poolId: string
  routeId: string
  reason: string
  score: number
  tier: number
  durationMs: number
}

// 失败仍保留后端返回的诊断快照，不在前端重跑匹配。
export function intentPreviewFailure(cause: unknown): IntentDecision | undefined {
  if (
    cause instanceof ApiError &&
    cause.code === 'INTENT_CLASSIFIER_FAILED' &&
    cause.details &&
    typeof cause.details === 'object' &&
    'decision' in cause.details
  ) {
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
  runtimeFormat?: 'zip' | 'tar.gz'
  runtimeMirrors?: string[]
  modelMirrors?: string[]
  runtimeUrl: string
  runtimeSha256: string
  executable: string
  modelUrl: string
  modelSha256: string
  modelName: string
  pooling: string
}
export interface LocalStatus {
  preparationId?: string
  installed: boolean
  running: boolean
  ready: boolean
  busy: boolean
  error: string
  platform: string
  installPath: string
  presetsUrl: string
  phase: string
  manifest: LocalManifest
  downloadedBytes?: number
  totalBytes?: number
  notice?: string
}
export interface IntentPreparation {
  completed: string[]
  committed: boolean
  id: string
  protocol: string
  running: boolean
  phase: string
  error: string
  config?: IntentConfig
}
export interface LocalCache {
  totalBytes: number
  unusedBytes: number
  unusedEntries: number
  removedBytes: number
  removedEntries: number
}
export interface IntentRuntimeStatus {
  state: 'disabled' | 'ready' | 'preparing' | 'error'
  reason: string
}
export interface IntentEvaluation {
  total: number
  correct: number
  unmatched: number
  wrongRoutes: number
  failures: number
  durationMs: number
  cases: {
    id: string
    expected: string
    actual: string
    poolId: string
    tier: number
    correct: boolean
    error?: string
    durationMs: number
  }[]
}
export interface LocalPreset {
  id: string
  name: string
  description: string
  sizeBytes: number
  runtimeUrl?: string
  modelUrl?: string
}
export interface LocalPresets {
  version: number
  presets: LocalPreset[]
  presetsUrl: string
  defaultPresetsUrl: string
  installPath: string
  platform: string
  error: string
  stale: boolean
}
export interface ClassifierOption extends ChannelSelectionOption {
  builtin: boolean
  sharedKey: string
  defaultModel: string
  models: string[]
}
export interface IntentOptions {
  groups?: Partial<Record<IntentProtocol, { id: string; activeChannels: number }[]>>
  tasks?: IntentTask[]
  classifiers: ClassifierOption[]
  embeddings: (Omit<ChannelSelectionOption, 'kind'> & { kind?: string })[]
}

export const intentApi = {
  localCache: () => apiRequest<LocalCache>('/intent/local/cache'),
  cleanLocalCache: () => apiRequest<LocalCache>('/intent/local/cache', { method: 'DELETE' }),
  importResource: (presetId: string, resource: 'runtime' | 'model', file: File) =>
    apiRequest<LocalStatus>(`/intent/local/import?presetId=${encodeURIComponent(presetId)}&resource=${resource}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/octet-stream' },
      body: file
    }),
  evaluate: (protocol: IntentProtocol, config: IntentConfig, datasetId: string) =>
    apiRequest<IntentEvaluation>(`/intent/${protocol}/evaluate`, {
      method: 'POST',
      body: JSON.stringify({ config, datasetId })
    }),
  correctTask: (protocol: IntentProtocol, requestId: string, task: string) =>
    apiRequest<{ datasetId: string }>(`/intent/${protocol}/correction`, {
      method: 'POST',
      body: JSON.stringify({ requestId, task })
    }),
  status: (protocol: IntentProtocol) => apiRequest<IntentRuntimeStatus>(`/intent/${protocol}/status`),
  preparation: () => apiRequest<IntentPreparation>('/intent/preparation'),
  cancelPreparation: (taskId: string) =>
    apiRequest<IntentPreparation>(`/intent/preparation?taskId=${encodeURIComponent(taskId)}`, { method: 'DELETE' }),
  prepare: (protocol: IntentProtocol, config: IntentConfig, adoptBuiltin: boolean) =>
    apiRequest<IntentPreparation>(`/intent/${protocol}/prepare`, {
      method: 'POST',
      body: JSON.stringify({ config, adoptBuiltin })
    }),
  buildIndex: (protocol: IntentProtocol, config: IntentConfig) =>
    apiRequest(`/intent/${protocol}/index`, { method: 'POST', body: JSON.stringify(config) }),
  get: (protocol: IntentProtocol) => apiRequest<IntentConfig>(`/intent/${protocol}`),
  save: (protocol: IntentProtocol, config: IntentConfig) =>
    apiRequest<IntentConfig>(`/intent/${protocol}`, { method: 'PUT', body: JSON.stringify(config) }),
  preview: (protocol: IntentProtocol, config: IntentConfig, text: string, model = '') =>
    apiRequest<IntentDecision>(`/intent/${protocol}/preview`, {
      method: 'POST',
      body: JSON.stringify({ config, text, model })
    }),
  indexStatus: (protocol: IntentProtocol, config: IntentConfig) =>
    apiRequest(`/intent/${protocol}/index-status`, { method: 'POST', body: JSON.stringify(config) }),
  events: (protocol: IntentProtocol) => apiRequest<IntentEvent[]>(`/intent/${protocol}/events`),
  feedback: (protocol: IntentProtocol, requestId: string, good: boolean) =>
    apiRequest(`/intent/${protocol}/feedback`, { method: 'POST', body: JSON.stringify({ requestId, good }) }),
  options: () => apiRequest<IntentOptions>('/intent/options'),
  local: () => apiRequest<LocalStatus>('/intent/local'),
  presets: () => apiRequest<LocalPresets>('/intent/local/presets'),
  setPresetsUrl: (url: string) =>
    apiRequest<LocalPresets>('/intent/local/presets-url', { method: 'PUT', body: JSON.stringify({ url }) }),
  installPreset: (id: string) =>
    apiRequest<LocalStatus>('/intent/local/install-preset', { method: 'POST', body: JSON.stringify({ id }) }),
  install: (manifest: LocalManifest) =>
    apiRequest<LocalStatus>('/intent/local/install', { method: 'POST', body: JSON.stringify(manifest) }),
  localAction: (action: 'start' | 'stop') => apiRequest<LocalStatus>(`/intent/local/${action}`, { method: 'POST' })
}
