import { describe, expect, it } from 'vitest'
import { parseDatasetImport } from './datasetImport'
import { defaultContentSafety, normalizeContentSafety } from './contentSafetyForm'

const dataset = () => ({ id: 'sample', name: '测试', description: '', kind: 'safety', revision: 12,
  samples: [{ id: 'one', label: 'destructive', text: '测试命令', family: 'one', source: 'test',
    split: 'index', negative: false, approved: true, pattern: '^测试命令$' }] })

describe('外部数据集导入', () => {
  it('保留文本和核验条件，但不能通过文件内批准标记直接启用样本', () => {
    const imported = parseDatasetImport(JSON.stringify(dataset()))
    expect(imported.revision).toBe(0)
    expect(imported.samples[0]).toMatchObject({ text: '测试命令', pattern: '^测试命令$', approved: false })
  })
  it('拒绝字符串布尔值和不完整样本，避免表单把无效数据解释成已启用', () => {
    const value = dataset()
    expect(() => parseDatasetImport(JSON.stringify({ ...value, samples: [{ ...value.samples[0], negative: 'false' }] }))).toThrow('布尔值')
    expect(() => parseDatasetImport(JSON.stringify({ ...value, samples: [{ ...value.samples[0], family: null }] }))).toThrow('family')
    expect(() => parseDatasetImport(JSON.stringify({ ...value, name: null }))).toThrow('name')
  })
})

describe('工具语义配置草稿', () => {
  it('旧配置加载后保持关闭，草稿修改不污染已保存设置', () => {
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
