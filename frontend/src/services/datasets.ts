import { apiRequest } from './http'

export interface EmbeddingProfile {
  source: 'local' | 'remote'
  channel: string
  model: string
  modelVersion: string
  queryPrefix: string
  examplePrefix: string
  timeoutMs: number
}
export interface DatasetSample {
  id: string
  label: string
  text: string
  negative: boolean
  split: 'index' | 'eval'
  family: string
  approved: boolean
  source: string
  tool?: string
  platform?: string
  pattern?: string
}
export interface Dataset {
  id: string
  name: string
  kind: 'intent' | 'safety'
  description: string
  revision: number
  samples: DatasetSample[]
}
export interface IndexStatus {
  datasetId: string
  revision: number
  profile: EmbeddingProfile
  space: string
  count: number
  dimension: number
  reused: number
  builtAt: string
}
export interface DatasetSummary {
  id: string
  name: string
  kind: Dataset['kind']
  revision: number
  count: number
  approved: number
  labels: string[]
  indexes: IndexStatus[]
}
export interface SearchResult {
  hits: { datasetId: string; revision: number; sample: DatasetSample; score: number }[]
  durationMs: number
  version: string
}
export interface Evaluation {
  total: number
  correct: number
  falsePositive: number
  falseNegative: number
  durationMs: number
  cases: { id: string; expected: string; actual: string; score: number; correct: boolean }[]
}
export interface SemanticSafetyConfig {
  enabled: boolean
  mode: 'observe' | 'review' | 'block'
  datasetIds: string[]
  embedding: EmbeddingProfile
  threshold: number
  margin: number
  failurePolicy: 'block' | 'review'
}
export { defaultEmbeddingProfile, defaultSemanticSafety } from '@/utils/semanticDefaults'
export const datasetsApi = {
  list: () => apiRequest<DatasetSummary[]>('/datasets'),
  builtin: () => apiRequest<Dataset[]>('/datasets/builtin'),
  get: (id: string) => apiRequest<Dataset>(`/datasets/${encodeURIComponent(id)}`),
  save: (dataset: Dataset) => apiRequest<Dataset>('/datasets', { method: 'POST', body: JSON.stringify(dataset) }),
  remove: (id: string, revision: number) => apiRequest(`/datasets/${encodeURIComponent(id)}`, { method: 'DELETE', body: JSON.stringify({ revision }) }),
  removeIndex: (id: string, index: IndexStatus) => apiRequest(`/datasets/${encodeURIComponent(id)}/index`, { method: 'DELETE', body: JSON.stringify(index) }),
  build: (id: string, revision: number, profile: EmbeddingProfile) => apiRequest<IndexStatus>(`/datasets/${encodeURIComponent(id)}/build`, { method: 'POST', body: JSON.stringify({ revision, profile }) }),
  search: (id: string, text: string, profile: EmbeddingProfile) => apiRequest<SearchResult>(`/datasets/${encodeURIComponent(id)}/search`, { method: 'POST', body: JSON.stringify({ text, profile }) }),
  evaluate: (id: string, profile: EmbeddingProfile, threshold: number, margin: number) => apiRequest<Evaluation>(`/datasets/${encodeURIComponent(id)}/evaluate`, { method: 'POST', body: JSON.stringify({ profile, threshold, margin }) })
}
