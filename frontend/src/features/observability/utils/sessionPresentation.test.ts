import { describe, expect, it } from 'vitest'
import { buildTraceBlocks, parseSessionBody } from '@/features/observability/utils/sessionPresentation'

const sse = (...events: Record<string, unknown>[]) => events.map(event => `event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`).join('')
const thinkingStart = { type: 'content_block_start', index: 0, content_block: { type: 'thinking', thinking: '' } }
const thinkingDelta = { type: 'content_block_delta', index: 0, delta: { type: 'thinking_delta', thinking: '思考内容' } }
const messageStop = { type: 'message_stop' }

describe('会话正文与流式日志解析', () => {
  it('合并验证 Responses、Gemini 和 Messages SSE 的完整与异常展示', () => {
    const responses = parseSessionBody(JSON.stringify({ output: [
      { type: 'function_call', call_id: 'tool', name: 'read_file', arguments: '{}' },
      { type: 'function_call_output', call_id: 'tool', output: '文件内容' }
    ] }))
    expect(responses.items[0]!.toolCalls?.[0]?.name).toBe('read_file')
    expect(responses.items[1]!.toolResults?.[0]?.content).toBe('文件内容')

    const gemini = parseSessionBody(JSON.stringify({ candidates: [{ content: { role: 'model', parts: [{ text: '回复' }] } }] }))
    expect(gemini.items[0]!.role).toBe('assistant')
    expect(gemini.items[0]!.text).toBe('回复')

    const thinkingOnly = parseSessionBody(sse(thinkingStart, thinkingDelta, messageStop))
    expect(thinkingOnly.mode).toBe('sse')
    expect(buildTraceBlocks(thinkingOnly.items)).toEqual([{ kind: 'reasoning', label: '助手思考', text: '思考内容' }])
    expect(thinkingOnly.notice).toContain('没有回复正文')

    const complete = parseSessionBody(sse(
      thinkingStart, thinkingDelta,
      { type: 'content_block_start', index: 1, content_block: { type: 'text', text: '你好' } },
      { type: 'content_block_delta', index: 1, delta: { type: 'text_delta', text: ' world' } },
      { type: 'content_block_start', index: 2, content_block: { type: 'tool_use', id: 'call-1', name: 'read', input: {} } },
      { type: 'content_block_delta', index: 2, delta: { type: 'input_json_delta', partial_json: '{"path":' } },
      { type: 'content_block_delta', index: 2, delta: { type: 'input_json_delta', partial_json: '"demo"}' } },
      messageStop
    ).replace(/\n/g, '\r\n'))
    expect(buildTraceBlocks(complete.items).map(block => block.kind)).toEqual(['reasoning', 'assistant', 'tool_call'])
    expect(complete.items[1]!.text).toBe('你好 world')
    expect(complete.items[2]!.toolCalls?.[0]).toEqual({ id: 'call-1', name: 'read', args: { path: 'demo' } })
    expect(complete.notice).toBe('')

    const interrupted = parseSessionBody(sse(thinkingStart, thinkingDelta, { type: 'error', error: { message: '上游中断' } }) + 'data: {"type":')
    expect(interrupted.items[0]!.thinking).toBe('思考内容')
    expect(interrupted.notice).toContain('上游中断')
    expect(interrupted.notice).toContain('无法解析')
    expect(interrupted.notice).toContain('未包含流结束事件')

    expect(parseSessionBody(sse(thinkingStart, thinkingDelta, { type: 'message_delta', delta: { stop_reason: 'max_tokens' } }, messageStop)).notice).toContain('token 上限')
    const missing = parseSessionBody(sse(thinkingDelta, messageStop))
    expect(missing.items).toEqual([])
    expect(missing.notice).toContain('缺少起始事件')
    const unknown = parseSessionBody('data: {"choices":[]}\n\ndata: [DONE]\n\n')
    expect(unknown.items).toEqual([])
    expect(unknown.notice).toContain('暂不支持')
  })
})
