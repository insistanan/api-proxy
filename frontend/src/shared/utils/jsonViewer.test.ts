import { describe, expect, it } from 'vitest'
import { JSON_MAX_DEPTH, JSON_MAX_ROWS, jsonLength, jsonText, jsonTreeRows, jsonType } from '@/shared/utils/jsonViewer'

describe('JSON 结构树', () => {
  const value = { 'a/b~c': [{ enabled: true }, null], text: '中文' }
  it('对象与数组逐级展开，路径转义不会产生节点冲突', () => {
    const opened = new Set(['$', '$/a~1b~0c', '$/a~1b~0c/0'])
    const rows = jsonTreeRows(value, opened, false, '').rows
    expect(rows.map(row => row.path)).toEqual(['$', '$/a~1b~0c', '$/a~1b~0c/0', '$/a~1b~0c/0/enabled', '$/a~1b~0c/1', '$/text'])
    expect(rows.find(row => row.key === 'enabled')?.type).toBe('boolean')
    expect(jsonLength(value)).toBe(2)
    expect(jsonLength(value['a/b~c'])).toBe(2)
    expect(jsonType(null)).toBe('null')
  })

  it('全部折叠只显示根节点；全部展开仍能独立折叠子树', () => {
    expect(jsonTreeRows(value, new Set(), false, '').rows).toHaveLength(1)
    expect(jsonTreeRows(value, new Set(), true, '').rows).toHaveLength(6)
    expect(jsonTreeRows(value, new Set(['$/a~1b~0c']), true, '').rows.map(row => row.key)).toEqual(['$', 'a/b~c', 'text'])
  })

  it('搜索键名或值会保留完整祖先路径，并隐藏无关分支', () => {
    expect(jsonTreeRows(value, new Set(), true, 'TRUE').rows.map(row => row.key)).toEqual(['$', 'a/b~c', '0', 'enabled'])
    expect(jsonTreeRows(value, new Set(), true, '中文').rows.map(row => row.key)).toEqual(['$', 'text'])
    expect(jsonTreeRows(value, new Set(['$/a~1b~0c']), true, 'TRUE').rows.map(row => row.key)).toEqual(['$', 'a/b~c'])
    expect(jsonTreeRows(value, new Set(), false, '不存在').rows).toHaveLength(0)
  })

  it('很深的 JSON 使用迭代遍历，超多节点有明确显示预算', () => {
    let deep: unknown = 1
    for (let depth = 0; depth < 300; depth++) deep = { nested: deep }
    const first = jsonTreeRows(deep, new Set(), true, '')
    expect(first.rows).toHaveLength(JSON_MAX_DEPTH + 1)
    expect(first.limited).toBe(true)
    const next = jsonTreeRows(first.rows[first.rows.length - 1]!.value, new Set(), true, '')
    const last = jsonTreeRows(next.rows[next.rows.length - 1]!.value, new Set(), true, '')
    expect(last.rows[last.rows.length - 1]?.value).toBe(1)
    const result = jsonTreeRows(Array.from({ length: 10000 }, (_, i) => i), new Set(), true, '')
    expect(result.rows).toHaveLength(JSON_MAX_ROWS)
    expect(result.limited).toBe(true)
  })

  it('保留不合法 JSON 原文，不以默认对象掩盖无法序列化的输入', () => {
    expect(jsonText('{broken')).toBe('{broken')
    expect(jsonText({ ok: true })).toBe('{\n  "ok": true\n}')
    const circular: Record<string, unknown> = {}; circular.self = circular
    expect(() => jsonText(circular)).toThrow()
    expect(() => jsonText(undefined)).toThrow('不能序列化')
  })
})
