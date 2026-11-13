import { apiRequest } from '@/shared/services/http'
import type { Dataset, DatasetSummary, EmbeddingProfile, Evaluation, IndexStatus, SearchResult } from '@/types'

export { defaultEmbeddingProfile, defaultSemanticSafety } from '@/features/extensions/datasets/utils/semanticDefaults'
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
