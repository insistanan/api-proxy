// 数据集 feature 的服务出口。纯默认配置请从 ./config 导入，避免加载网络模块。
import type { Dataset, DatasetSummary } from '@/types'

export { defaultEmbeddingProfile, defaultSemanticSafety } from '@/features/extensions/datasets/config'

// 延迟加载 API，避免内容安全表单的纯默认值导入触发鉴权存储初始化。
export const datasetsApi = {
  list: (): Promise<DatasetSummary[]> => import('@/features/extensions/datasets/services/datasets').then(({ datasetsApi: api }) => api.list()),
  builtin: (): Promise<Dataset[]> => import('@/features/extensions/datasets/services/datasets').then(({ datasetsApi: api }) => api.builtin()),
  save: (dataset: Dataset): Promise<Dataset> => import('@/features/extensions/datasets/services/datasets').then(({ datasetsApi: api }) => api.save(dataset))
}
