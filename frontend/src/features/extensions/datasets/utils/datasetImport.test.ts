import { describe, expect, it } from 'vitest'
import { parseDatasetImport } from '@/features/extensions/datasets/utils/datasetImport'
import { defaultContentSafety, normalizeContentSafety } from '@/features/settings/utils/contentSafetyForm'

const dataset = () => ({ id: 'sample', name: '测试', description: '', kind: 'safety', revision: 12,
  samples: [{ id: 'one', label: 'destructive', text: '测试命令', family: 'one', source: 'test', split: 'index', negative: false, approved: true, pattern: '^测试命令$' }] })

describe('数据集与语义配置草稿', () => {
  it('合并验证导入核验、无效数据拒绝和旧配置草稿隔离', () => {
    const imported = parseDatasetImport(JSON.stringify(dataset()))
    expect(imported.revision).toBe(0)
    expect(imported.samples[0]).toMatchObject({ text: '测试命令', pattern: '^测试命令$', approved: false })

    const value = dataset()
    expect(() => parseDatasetImport(JSON.stringify({ ...value, samples: [{ ...value.samples[0], negative: 'false' }] }))).toThrow('布尔值')
    expect(() => parseDatasetImport(JSON.stringify({ ...value, samples: [{ ...value.samples[0], family: null }] }))).toThrow('family')
    expect(() => parseDatasetImport(JSON.stringify({ ...value, name: null }))).toThrow('name')

    const old = defaultContentSafety()
    delete old.dangerousCmd.semantic
    const draft = normalizeContentSafety(old)
    expect(draft.dangerousCmd.semantic).toMatchObject({ enabled: false, mode: 'observe' })
    draft.dangerousCmd.semantic!.datasetIds.push('edited')
    expect(old.dangerousCmd.semantic).toBeUndefined()
    const saved = defaultContentSafety()
    const copy = normalizeContentSafety(saved)
    copy.dangerousCmd.semantic!.datasetIds.push('new')
    copy.dangerousCmd.semantic!.embedding.model = 'changed'
    expect(saved.dangerousCmd.semantic!.datasetIds).toEqual([])
    expect(saved.dangerousCmd.semantic!.embedding.model).not.toBe('changed')
  })
})
