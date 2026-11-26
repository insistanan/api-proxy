import { describe, expect, it, vi } from 'vitest'
import { readTranslation } from '@/features/extensions/skills/services/translation'

type Protocol = Parameters<typeof readTranslation>[1]
const json = (value: unknown) => new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } })
const stream = (events: unknown[], done = false) => new Response(
  events.map(event => `data: ${JSON.stringify(event)}\n\n`).join('') + (done ? 'data: [DONE]\n\n' : ''),
  { headers: { 'Content-Type': 'text/event-stream' } }
)
const messages = (reason: string) => ({ content: [{ type: 'text', text: '译文' }], stop_reason: reason })
const responses = (status: string) => ({ status, output: [{ type: 'message', content: [{ type: 'output_text', text: '译文' }] }] })
const chat = (reason: string) => ({ choices: [{ message: { content: '译文' }, finish_reason: reason }] })
const gemini = (reason: string) => ({ candidates: [{ content: { parts: [{ text: '译文' }] }, finishReason: reason }] })

describe('单项翻译完成契约', () => {
  const ordinary: [Protocol, unknown, unknown][] = [
    ['messages', messages('end_turn'), messages('max_tokens')],
    ['responses', responses('completed'), responses('incomplete')],
    ['chat', chat('stop'), chat('length')],
    ['gemini', gemini('STOP'), gemini('MAX_TOKENS')]
  ]
  it.each(ordinary)('%s 普通响应只接受正常终态', async (protocol, complete, truncated) => {
    await expect(readTranslation(json(complete), protocol, vi.fn())).resolves.toBe('译文')
    await expect(readTranslation(json(truncated), protocol, vi.fn())).rejects.toThrow()
  })

  const streams: [Protocol, unknown[], unknown[], boolean][] = [
    ['messages', [{ type: 'content_block_delta', delta: { type: 'text_delta', text: '译文' } }],
      [{ type: 'message_delta', delta: { stop_reason: 'end_turn' } }, { type: 'message_stop' }], false],
    ['responses', [{ type: 'response.output_text.delta', delta: '译文' }],
      [{ type: 'response.completed', response: responses('completed') }], false],
    ['chat', [{ choices: [{ delta: { content: '译文' }, finish_reason: null }] }],
      [{ choices: [{ delta: {}, finish_reason: 'stop' }] }], true],
    ['gemini', [{ candidates: [{ content: { parts: [{ text: '译文' }] } }] }],
      [{ candidates: [{ finishReason: 'STOP' }] }], false]
  ]
  it.each(streams)('%s 流式终态缺失时仅保留草稿', async (protocol, deltas, terminal, done) => {
    const update = vi.fn()
    await expect(readTranslation(stream(deltas), protocol, update)).rejects.toThrow()
    expect(update).toHaveBeenCalledWith('译文')
    await expect(readTranslation(stream([...deltas, ...terminal], done), protocol, vi.fn())).resolves.toBe('译文')
  })

  it('Responses 最终文本替换 delta，且拒绝混入拒绝块', async () => {
    const update = vi.fn()
    await expect(readTranslation(stream([
      { type: 'response.output_text.delta', delta: '译' },
      { type: 'response.completed', response: responses('completed') }
    ]), 'responses', update)).resolves.toBe('译文')
    expect(update).toHaveBeenLastCalledWith('译文')
    const refused = responses('completed')
    refused.output[0].content.push({ type: 'refusal', text: '拒绝' })
    await expect(readTranslation(json(refused), 'responses', vi.fn())).rejects.toThrow('拒绝')
  })

  it('Chat 即使 finish_reason 正常也必须收到 DONE', async () => {
    await expect(readTranslation(stream([{ choices: [{ delta: { content: '译文' }, finish_reason: 'stop' }] }]), 'chat', vi.fn())).rejects.toThrow('DONE')
  })
})
