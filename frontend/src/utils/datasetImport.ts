import type { Dataset, DatasetSample } from '@/services/datasets'

// 外部文件只导入待审核草稿，文件内的批准标记不能自动启用执行安全策略。
export function parseDatasetImport(text: string): Dataset {
  const value: unknown = JSON.parse(text)
  if (!value || typeof value !== 'object') throw new Error('文件不是有效的数据集对象')
  const data = value as Record<string, unknown>
  for (const key of ['id', 'name', 'description']) {
    if (typeof data[key] !== 'string') throw new Error(`数据集缺少有效的 ${key} 字段`)
  }
  if (data.kind !== 'intent' && data.kind !== 'safety') throw new Error('数据集用途必须是 intent 或 safety')
  if (!Array.isArray(data.samples) || data.samples.length > 5000) throw new Error('样本必须是数组，且不超过 5000 条')
  const samples = data.samples.map((value: unknown): DatasetSample => {
    if (!value || typeof value !== 'object') throw new Error('样本格式无效')
    const row = value as Record<string, unknown>
    for (const key of ['id', 'label', 'text', 'family', 'source']) {
      if (typeof row[key] !== 'string' || !(row[key] as string).trim()) throw new Error(`样本缺少有效的 ${key} 字段`)
    }
    if (typeof row.negative !== 'boolean' || typeof row.approved !== 'boolean') throw new Error('样本正反例和批准标记必须是布尔值')
    if (row.split !== 'index' && row.split !== 'eval') throw new Error('样本用途必须是 index 或 eval')
    for (const key of ['tool', 'platform', 'pattern']) {
      if (row[key] !== undefined && typeof row[key] !== 'string') throw new Error(`样本 ${key} 字段无效`)
    }
    return { id: row.id as string, label: row.label as string, text: row.text as string,
      family: row.family as string, source: row.source as string, negative: row.negative,
      split: row.split, approved: false, tool: row.tool as string | undefined,
      platform: row.platform as string | undefined, pattern: row.pattern as string | undefined }
  })
  return { id: data.id as string, name: data.name as string, description: data.description as string,
    kind: data.kind, revision: 0, samples }
}
