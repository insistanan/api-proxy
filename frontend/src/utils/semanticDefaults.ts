import type { EmbeddingProfile, SemanticSafetyConfig } from '@/services/datasets'

export const defaultEmbeddingProfile = (): EmbeddingProfile => ({ source: 'local', channel: '', model: '', modelVersion: '', queryPrefix: '', examplePrefix: '', timeoutMs: 2000 })
export const defaultSemanticSafety = (): SemanticSafetyConfig => ({ enabled: false, mode: 'observe', datasetIds: [], embedding: defaultEmbeddingProfile(), threshold: 0.85, margin: 0.1, failurePolicy: 'block' })
