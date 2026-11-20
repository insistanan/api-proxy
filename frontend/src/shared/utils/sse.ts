/**
 * 读取 SSE (Server-Sent Events) 流
 */
export async function readSSEStream<T>(
  response: Response,
  onData: (data: T) => void,
  options?: { signal?: AbortSignal; strict?: boolean; stopWhen?: (_data: T) => boolean }
): Promise<void> {
  if (!response.body) {
    throw new Error('Response body is null')
  }

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  const processLine = (line: string): boolean => {
    const trimmed = line.trim()
    if (!trimmed.startsWith('data:')) return false
    const data = trimmed.slice(5).trim()
    if (data === '[DONE]') return true
    let parsed: T
    try {
      parsed = JSON.parse(data) as T
    } catch (cause) {
      if (options?.strict) throw cause
      return false
    }
    // 回调中的业务错误不能被误当成 JSON 解析失败而吞掉。
    onData(parsed)
    return options?.stopWhen?.(parsed) ?? false
  }

  try {
    while (true) {
      if (options?.signal?.aborted) break

      const { done, value } = await reader.read()
      if (done) break

      buffer += decoder.decode(value, { stream: true })

      const lines = buffer.split('\n')
      buffer = lines.pop() ?? ''

      for (const line of lines) if (processLine(line)) return
    }
    buffer += decoder.decode()
    if (buffer.trim()) processLine(buffer)
  } finally {
    try {
      if (options?.strict) await reader.cancel()
    } finally {
      reader.releaseLock()
    }
  }
}
