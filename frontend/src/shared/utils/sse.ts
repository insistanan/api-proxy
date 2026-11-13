/**
 * 读取 SSE (Server-Sent Events) 流
 */
export async function readSSEStream<T>(
  response: Response,
  onData: (data: T) => void,
  options?: { signal?: AbortSignal }
): Promise<void> {
  if (!response.body) {
    throw new Error('Response body is null')
  }

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''

  try {
    while (true) {
      if (options?.signal?.aborted) break

      const { done, value } = await reader.read()
      if (done) break

      buffer += decoder.decode(value, { stream: true })

      const lines = buffer.split('\n')
      buffer = lines.pop() ?? ''

      for (const line of lines) {
        const trimmed = line.trim()
        if (!trimmed || trimmed.startsWith(':')) continue // 跳过注释行

        if (trimmed.startsWith('data: ')) {
          const data = trimmed.slice(6)
          if (data === '[DONE]') return

          try {
            const parsed = JSON.parse(data) as T
            onData(parsed)
          } catch {
            // 跳过无法解析的行
          }
        }
      }
    }
  } finally {
    reader.releaseLock()
  }
}
