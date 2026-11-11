import { describe, expect, it } from 'vitest'
import { buildTraceBlocks, parseSessionBody } from './sessionPresentation'

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

const sse = (...events: Record<string, unknown>[]) => events.map(event => `event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`).join('')
const thinkingStart = { type: 'content_block_start', index: 0, content_block: { type: 'thinking', thinking: '' } }
const thinkingDelta = { type: 'content_block_delta', index: 0, delta: { type: 'thinking_delta', thinking: '思考内容' } }
const messageStop = { type: 'message_stop' }

describe('Messages 流式日志', () => {
  it('只含思考的原始 SSE 不显示成回复正文，并说明缺失正文', () => {
    const parsed = parseSessionBody(sse(thinkingStart, thinkingDelta, messageStop))
    expect(parsed.mode).toBe('sse')
    expect(buildTraceBlocks(parsed.items)).toEqual([{ kind: 'reasoning', label: '助手思考', text: '思考内容' }])
    expect(parsed.notice).toContain('没有回复正文')
  })

  it('按内容块聚合思考、文本和工具参数，保留增量中的空格', () => {
    const parsed = parseSessionBody(sse(
      thinkingStart, thinkingDelta,
      { type: 'content_block_start', index: 1, content_block: { type: 'text', text: '你好' } },
      { type: 'content_block_delta', index: 1, delta: { type: 'text_delta', text: ' world' } },
      { type: 'content_block_start', index: 2, content_block: { type: 'tool_use', id: 'call-1', name: 'read', input: {} } },
      { type: 'content_block_delta', index: 2, delta: { type: 'input_json_delta', partial_json: '{"path":' } },
      { type: 'content_block_delta', index: 2, delta: { type: 'input_json_delta', partial_json: '"demo"}' } },
      messageStop
    ).replace(/\n/g, '\r\n'))
    expect(buildTraceBlocks(parsed.items).map(block => block.kind)).toEqual(['reasoning', 'assistant', 'tool_call'])
    expect(parsed.items[1]!.text).toBe('你好 world')
    expect(parsed.items[2]!.toolCalls?.[0]).toEqual({ id: 'call-1', name: 'read', args: { path: 'demo' } })
    expect(parsed.notice).toBe('')
  })

  it('截断或错误流保留已收到的思考并给出明确提示', () => {
    const parsed = parseSessionBody(sse(thinkingStart, thinkingDelta, { type: 'error', error: { message: '上游中断' } }) + 'data: {"type":')
    expect(parsed.items[0]!.thinking).toBe('思考内容')
    expect(parsed.notice).toContain('上游中断')
    expect(parsed.notice).toContain('无法解析')
    expect(parsed.notice).toContain('未包含流结束事件')
  })

  it('不把 token 耗尽描述为正常完整回复', () => {
    const parsed = parseSessionBody(sse(thinkingStart, thinkingDelta, { type: 'message_delta', delta: { stop_reason: 'max_tokens' } }, messageStop))
    expect(parsed.notice).toContain('token 上限')
  })

  it('不把缺少块起始或未知格式的 SSE 当作纯文本回答', () => {
    const missing = parseSessionBody(sse(thinkingDelta, messageStop))
    expect(missing.items).toEqual([])
    expect(missing.notice).toContain('缺少起始事件')
    const unknown = parseSessionBody('data: {"choices":[]}\n\ndata: [DONE]\n\n')
    expect(unknown.items).toEqual([])
    expect(unknown.notice).toContain('暂不支持')
  })
})
