// 快捷测试的请求构造与流读取，独立于管理 API。
import { useAuthStore } from '@/stores/auth'
import type { ApiTab } from './channel-api'

export interface TestChannelContext {
  purpose?: 'quick_test'
  channelIndex?: number
  thinking?: string
  sessionId?: string
  threadId?: string
  interactionId?: string
  onInteractionId?: (_id: string) => void
  responseId?: string
  onResponseId?: (_id: string) => void
  signal?: AbortSignal
  messages?: Array<{ role: 'user' | 'assistant'; content: string }>
}

export const testChannelWithModel = async (
  apiType: ApiTab,
  channelId: string,
  model: string | undefined,
  message: string,
  onChunk: (_chunk: string) => void,
  sessionContext?: TestChannelContext
): Promise<void> => {
  if (!channelId.trim()) throw new Error('渠道缺少稳定 ID，无法执行测试')
  const channelIndex = sessionContext?.channelIndex ?? Number(channelId)
  if (!Number.isInteger(channelIndex) || channelIndex < 0) {
    throw new Error('快捷测试缺少有效的渠道索引')
  }

  const authStore = useAuthStore()
  const accessKey = authStore.apiKey?.trim() || ''
  if (!accessKey) throw new Error('缺少访问密钥，请先完成登录认证')

  const baseUrl = import.meta.env.PROD ? '' : import.meta.env.VITE_BACKEND_URL || ''
  const generateUUID = () => crypto.randomUUID()
  const sessionId = sessionContext?.sessionId || generateUUID()
  const threadId = sessionContext?.threadId || `thread-${generateUUID()}`
  const selectedModel = model?.trim() || ''
  if (!selectedModel) throw new Error('请选择或输入用于快捷测试的模型')
  const testPurpose = sessionContext?.purpose || 'quick_test'
  const metadata = {
    channel_index: channelIndex,
    purpose: testPurpose,
    user_id: sessionId,
    session_id: sessionId,
    thread_id: threadId
  }

  let endpoint = ''
  let body: Record<string, unknown> = {}
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    'x-api-key': accessKey,
    'x-proxy-purpose': testPurpose
  }

  switch (apiType) {
    case 'messages':
      endpoint = '/v1/messages'
      body = {
        model: selectedModel,
        max_tokens: 1024,
        messages: [{ role: 'user', content: message }],
        metadata,
        stream: true
      }
      break
    case 'responses':
      endpoint = '/v1/responses'
      body = { model: selectedModel, input: message, metadata, stream: true }
      if (sessionContext?.responseId) body.previous_response_id = sessionContext.responseId
      break
    case 'gemini':
      endpoint = `/v1beta/models/${encodeURIComponent(selectedModel.replace(/^models\//, ''))}:streamGenerateContent`
      body = { contents: [{ role: 'user', parts: [{ text: message }] }], metadata }
      if (sessionContext?.interactionId) body.previous_interaction_id = sessionContext.interactionId
      break
    case 'chat':
      endpoint = '/v1/chat/completions'
      body = {
        model: selectedModel,
        messages: [{ role: 'user', content: message }],
        metadata,
        user: sessionId,
        stream: true
      }
      break
    case 'images':
      endpoint = '/v1/images/generations'
      body = { model: selectedModel, prompt: message, metadata, n: 1, response_format: 'url' }
      break
    case 'embeddings':
      endpoint = '/v1/embeddings'
      body = { model: selectedModel, input: message }
      break
    case 'decisions':
      endpoint = '/v1/decisions'
      body = {
        model: selectedModel,
        state: { task: message },
        questions: {
          result: {
            type: 'noul',
            instructions: message,
            criteria: { true: '是', false: '否' }
          }
        }
      }
      break
  }

  const response = await fetch(`${baseUrl}${endpoint}`, {
    method: 'POST',
    headers,
    body: JSON.stringify(body),
    signal: sessionContext?.signal
  })
  if (!response.ok) {
    const text = await response.text()
    let detail = ''
    try {
      const parsed = JSON.parse(text)
      detail = parsed.error?.message || parsed.error || parsed.message || ''
    } catch {
      detail = text
    }
    throw new Error(detail || `请求失败: ${response.status} ${response.statusText}`)
  }

  if (apiType === 'images') {
    const result = await response.json()
    // images 生成分支：每项可能是 URL、修订提示词或 base64 数据。
    const imageItems: Array<{ url?: string; revised_prompt?: string; b64_json?: string }> = Array.isArray(
      result.data
    )
      ? result.data
      : []
    const outputs = imageItems
      .map(item => item.url || item.revised_prompt || (item.b64_json ? '[base64 image data]' : ''))
      .filter(Boolean)
    onChunk(outputs.length > 0 ? outputs.join('\n') : JSON.stringify(result, null, 2))
    return
  }

  if (apiType === 'embeddings' || apiType === 'decisions') {
    // 两个透传协议固定返回 JSON，不提供 SSE 通道。
    const result = await response.json()
    onChunk(JSON.stringify(result, null, 2))
    return
  }

  if (!response.body) throw new Error('上游响应缺少响应体')
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  const processLine = (line: string) => {
    if (!line.trim() || !line.startsWith('data:')) return
    const data = line.slice(5).trim()
    if (data === '[DONE]') return
    // SSE 事件载荷：各协议字段并集，按 apiType 取各自关心的字段。
    let parsed: {
      type?: string
      delta?: { text?: string }
      response?: { id?: string }
      id?: string
      candidates?: Array<{ content?: { parts?: Array<{ text?: string }> } }>
      choices?: Array<{ delta?: { content?: string } }>
      completion?: string
    }
    try {
      parsed = JSON.parse(data)
    } catch {
      return
    }
    if (apiType === 'responses' && parsed.type === 'response.completed' && parsed.response?.id) {
      sessionContext?.onResponseId?.(parsed.response.id)
      sessionContext?.onInteractionId?.(parsed.response.id)
    }
    if (apiType === 'gemini' && parsed.id) sessionContext?.onInteractionId?.(parsed.id)
    const content =
      apiType === 'messages'
        ? parsed.delta?.text
        : apiType === 'responses'
          ? parsed.type === 'response.output_text.delta'
            ? parsed.delta
            : parsed.completion
          : apiType === 'gemini'
            ? parsed.candidates?.[0]?.content?.parts?.[0]?.text
            : parsed.choices?.[0]?.delta?.content
    if (typeof content === 'string' && content) onChunk(content)
  }

  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''
      lines.forEach(processLine)
    }
    buffer += decoder.decode()
    if (buffer) processLine(buffer)
  } finally {
    reader.releaseLock()
  }
}

/**
 * 测试渠道连通性（旧版接口，使用默认模型，支持多轮会话）
 * 委托给 testChannelWithModel 实现
 */
export const testChannel = async (
  apiType: ApiTab,
  channelId: string,
  message: string,
  onChunk: (_chunk: string) => void,
  sessionContext?: TestChannelContext
): Promise<void> => {
  return testChannelWithModel(apiType, channelId, undefined, message, onChunk, sessionContext)
}
