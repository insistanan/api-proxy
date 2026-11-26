// 数据集与语义安全的前后端契约类型；API 实现位于 features/extensions/datasets。

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
  mode: 'intent' | 'safety_retrieval'
  total: number
  correct: number
  falsePositive: number
  falseNegative: number
  durationMs: number
  cases: { id: string; expected: string; actual: string; score: number; correct: boolean; blockEligible: boolean }[]
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
