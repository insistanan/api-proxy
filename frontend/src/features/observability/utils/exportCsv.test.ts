import { describe, expect, it } from 'vitest'
import { createCsv } from '@/features/observability/utils/exportCsv'

describe('createCsv', () => {
  it('转义逗号、引号与换行，并保留空单元格', () => {
    expect(createCsv(['名称', '说明'], [['渠道,一', '他说"你好"'], [null, '第一行\n第二行']])).toBe(
      '名称,说明\r\n"渠道,一","他说""你好"""\r\n,"第一行\n第二行"'
    )
  })

  it('只 neutralize 文本公式，不破坏数值', () => {
    expect(createCsv(['值'], [['=HYPERLINK("bad")'], ['  @cmd'], [-12], [0]])).toBe("值\r\n\"'=HYPERLINK(\"\"bad\"\")\"\r\n'  @cmd\r\n-12\r\n0")
  })
})
