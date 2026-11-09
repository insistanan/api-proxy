import { describe, expect, it } from 'vitest'
import { parseSessionBody } from './sessionPresentation'

describe('正文展示解析', () => {
  it('保留 Responses 工具调用与结果', () => {
    const parsed = parseSessionBody(JSON.stringify({ output: [
      { type: 'function_call', call_id: 'tool', name: 'read_file', arguments: '{}' },
      { type: 'function_call_output', call_id: 'tool', output: '文件内容' }
    ] }))
    expect(parsed.items[0]!.toolCalls?.[0]?.name).toBe('read_file')
    expect(parsed.items[1]!.toolResults?.[0]?.content).toBe('文件内容')
  })

  it('支持 Gemini 响应候选内容', () => {
    const parsed = parseSessionBody(JSON.stringify({ candidates: [{ content: { role: 'model', parts: [{ text: '回复' }] } }] }))
    expect(parsed.items[0]!.role).toBe('assistant')
    expect(parsed.items[0]!.text).toBe('回复')
  })

  it('损坏 JSON 给出明确说明', () => {
    const parsed = parseSessionBody('{"input":[')
    expect(parsed.items).toEqual([])
    expect(parsed.notice).toContain('解析')
  })
})
