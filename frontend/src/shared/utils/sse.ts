/** 按空行交付完整 SSE 事件，取消与异常关闭不会伪装成正常结束。 */
export async function readSSEStream<T>(
  response: Response,
  onData: (data: T) => void,
  options?: { signal?: AbortSignal; stopWhen?: (data: T) => boolean }
): Promise<{ doneMarker: boolean }> {
  if (!response.body) throw new Error('流式响应缺少正文')
  const reader = response.body.getReader()
  const decoder = new TextDecoder('utf-8', { fatal: true })
  let buffer = ''
  let data: string[] = []
  let doneMarker = false
  const failures: unknown[] = []
  let cancellation: Promise<void> | undefined
  // 取消与正常收尾等待同一个结果，避免异步取消错误在函数返回后才到达。
  const cancel = () => cancellation ??= reader.cancel(options?.signal?.reason).catch(error => { failures.push(error) })
  const abort = () => { void cancel() }
  options?.signal?.addEventListener('abort', abort, { once: true })

  function line(value: string): boolean {
    if (value === '') {
      if (!data.length) return false
      const payload = data.join('\n')
      data = []
      if (payload === '[DONE]') { doneMarker = true; return true }
      let parsed: T
      try { parsed = JSON.parse(payload) as T }
      catch { throw new Error('SSE 事件不是有效的 JSON') }
      onData(parsed)
      return options?.stopWhen?.(parsed) ?? false
    }
    if (value.startsWith(':')) return false
    const colon = value.indexOf(':')
    const field = colon < 0 ? value : value.slice(0, colon)
    let content = colon < 0 ? '' : value.slice(colon + 1)
    if (content.startsWith(' ')) content = content.slice(1)
    if (field === 'data') data.push(content)
    return false
  }
  function consume(final = false): boolean {
    while (true) {
      const index = buffer.search(/[\r\n]/)
      if (index < 0) return false
      if (!final && buffer[index] === '\r' && index === buffer.length - 1) return false
      const width = buffer[index] === '\r' && buffer[index + 1] === '\n' ? 2 : 1
      const value = buffer.slice(0, index)
      buffer = buffer.slice(index + width)
      if (line(value)) return true
    }
  }
  try {
    options?.signal?.throwIfAborted()
    while (true) {
      const { done, value } = await reader.read()
      options?.signal?.throwIfAborted()
      if (done) {
        buffer += decoder.decode()
        const stopped = consume(true)
        if (!stopped && (buffer || data.length)) throw new Error('SSE 在完整事件结束前关闭')
        break
      }
      buffer += decoder.decode(value, { stream: true })
      if (consume()) break
    }
  } catch (error) {
    failures.unshift(error)
  } finally {
    options?.signal?.removeEventListener('abort', abort)
    try { await cancel() } finally { reader.releaseLock() }
  }
  if (failures.length > 1) throw Object.assign(new Error('读取流失败，取消流时也发生错误'), { cause: failures[0], cleanupError: failures[1] })
  if (failures.length) throw failures[0]
  return { doneMarker }
}
