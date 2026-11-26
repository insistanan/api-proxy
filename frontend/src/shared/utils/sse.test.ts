import { describe, expect, it, vi } from 'vitest'
import { readSSEStream } from '@/shared/utils/sse'

describe('SSE 事件与资源生命周期', () => {
  it('支持多 data 行以及逐字节拆开的中文和 CRLF', async () => {
    const bytes = new TextEncoder().encode(': heartbeat\r\ndata: {"text":\r\ndata: "中文"}\r\n\r\ndata: [DONE]\r\n\r\n')
    const response = new Response(new ReadableStream<Uint8Array>({
      start(controller) {
        for (const byte of bytes) controller.enqueue(new Uint8Array([byte]))
        controller.close()
      }
    }))
    const receive = vi.fn()
    await expect(readSSEStream(response, receive)).resolves.toEqual({ doneMarker: true })
    expect(receive).toHaveBeenCalledExactlyOnceWith({ text: '中文' })
    expect(response.body?.locked).toBe(false)
  })

  it('取消会唤醒挂起的读取并以取消失败结束', async () => {
    const cancel = vi.fn()
    const response = new Response(new ReadableStream({ cancel }))
    const controller = new AbortController()
    const pending = readSSEStream(response, vi.fn(), { signal: controller.signal })
    const rejected = expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    controller.abort()
    await rejected
    expect(cancel).toHaveBeenCalledTimes(1)
    expect(response.body?.locked).toBe(false)
  })

  it.each(['data: {"text":"partial"}\n', 'data: bad-json\n\n'])('异常事件不能作为成功关闭：%s', async body => {
    await expect(readSSEStream(new Response(body), vi.fn())).rejects.toThrow()
  })

  it('解析与取消同时失败时保留两个原因，并释放读取锁', async () => {
    const cleanupError = new Error('取消失败')
    const response = new Response(new ReadableStream({
      start(controller) { controller.enqueue(new TextEncoder().encode('data: bad-json\n\n')) },
      cancel() { return Promise.reject(cleanupError) }
    }))
    await expect(readSSEStream(response, vi.fn())).rejects.toMatchObject({
      cause: expect.objectContaining({ message: 'SSE 事件不是有效的 JSON' }), cleanupError
    })
    expect(response.body?.locked).toBe(false)
  })
})
