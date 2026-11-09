// 五协议正文的展示解析；不参与转发或会话身份判断，解析失败保留明确说明。
export interface ParsedToolCall {
  id?: string
  name: string
  args: unknown
}

export interface ParsedToolResult {
  toolUseId?: string
  content: string
  isError?: boolean
}

export interface ParsedItem {
  role: string
  name?: string
  text?: string
  thinking?: string
  toolCalls?: ParsedToolCall[]
  toolResults?: ParsedToolResult[]
}

function parseSessionItems(body: string): ParsedItem[] {
  if (!body) return []
  try {
    const data = JSON.parse(body)
    const items: ParsedItem[] = []

    // 1. Anthropic Claude 格式 (messages / content)
    const claudeMessages = Array.isArray(data.messages) ? data.messages : Array.isArray(data.content) && data.role ? [data] : undefined
    if (claudeMessages) {
      for (const m of claudeMessages) {
        const item: ParsedItem = { role: m.role || 'user' }
        if (typeof m.content === 'string') {
          item.text = m.content
        } else if (Array.isArray(m.content)) {
          for (const block of m.content) {
            if (block.type === 'text') {
              item.text = (item.text ? item.text + '\n' : '') + (block.text || '')
            } else if (block.type === 'thinking' || block.type === 'redacted_thinking') {
              item.thinking = (item.thinking ? item.thinking + '\n' : '') + (block.thinking || '[redacted thinking]')
            } else if (block.type === 'tool_use') {
              if (!item.toolCalls) item.toolCalls = []
              item.toolCalls.push({ id: block.id, name: block.name, args: block.input })
            } else if (block.type === 'tool_result') {
              if (!item.toolResults) item.toolResults = []
              const textContent = typeof block.content === 'string' ? block.content : JSON.stringify(block.content, null, 2)
              item.toolResults.push({ toolUseId: block.tool_use_id, content: textContent, isError: block.is_error })
            }
          }
        }
        if (Array.isArray(m.tool_calls)) {
          item.toolCalls = m.tool_calls.map((call: { id?: string; function?: { name?: string; arguments?: unknown } }) => ({ id: call.id, name: call.function?.name || 'function', args: call.function?.arguments }))
        }
        if (m.role === 'tool') item.toolResults = [{ toolUseId: m.tool_call_id, content: typeof m.content === 'string' ? m.content : JSON.stringify(m.content) }]
        items.push(item)
      }
      return items
    }

    // 2. OpenAI Chat Completions 格式
    if (Array.isArray(data.choices) && data.choices[0]?.message) {
      const msg = data.choices[0].message
      const item: ParsedItem = {
        role: msg.role || 'assistant',
        text: msg.content || '',
        thinking: msg.reasoning_content || ''
      }
      if (Array.isArray(msg.tool_calls)) {
        item.toolCalls = msg.tool_calls.map((tc: { id?: string; function?: { name?: string; arguments?: unknown } }) => ({
          id: tc.id,
          name: tc.function?.name || 'function',
          args: tc.function?.arguments
        }))
      }
      items.push(item)
      return items
    }

    // 3. OpenAI Responses 格式 (input 列表)
    const responseItems = Array.isArray(data.input) ? data.input : Array.isArray(data.output) ? data.output : typeof data.input === 'string' ? [{ type: 'message', role: 'user', content: [{ text: data.input }] }] : undefined
    if (responseItems) {
      for (const entry of responseItems) {
        if (entry.type === 'message' || (!entry.type && entry.role)) {
          const item: ParsedItem = { role: entry.role || 'user' }
          if (Array.isArray(entry.content)) {
            item.text = entry.content.map((c: { text?: string }) => c.text || '').filter(Boolean).join('\n')
          } else if (typeof entry.content === 'string') {
            item.text = entry.content
          }
          items.push(item)
        } else if (entry.type === 'function_call') {
          items.push({
            role: 'assistant',
            toolCalls: [{ id: entry.call_id, name: entry.name, args: entry.arguments }]
          })
        } else if (entry.type === 'function_call_output') {
          items.push({
            role: 'tool',
            toolResults: [{ toolUseId: entry.call_id, content: entry.output }]
          })
        }
      }
      return items
    }

    // 4. Gemini 格式 (contents)
    const geminiContents = Array.isArray(data.contents) ? data.contents : Array.isArray(data.candidates) ? data.candidates.map((candidate: { content?: unknown }) => candidate.content).filter(Boolean) : undefined
    if (geminiContents) {
      for (const c of geminiContents) {
        const item: ParsedItem = { role: c.role === 'model' ? 'assistant' : 'user' }
        if (Array.isArray(c.parts)) {
          for (const p of c.parts) {
            if (p.thought) {
              item.thinking = (item.thinking ? item.thinking + '\n' : '') + (p.text || '')
            } else if (p.text) {
              item.text = (item.text ? item.text + '\n' : '') + p.text
            } else if (p.functionCall) {
              if (!item.toolCalls) item.toolCalls = []
              item.toolCalls.push({ name: p.functionCall.name, args: p.functionCall.args })
            } else if (p.functionResponse) {
              if (!item.toolResults) item.toolResults = []
              item.toolResults.push({ toolUseId: p.functionResponse.name, content: JSON.stringify(p.functionResponse.response, null, 2) })
            }
          }
        }
        items.push(item)
      }
      return items
    }

    return []
  } catch (error) {
    throw new Error(error instanceof Error ? `解析结构化正文失败：${error.message}` : '解析结构化正文失败')
  }
}

export function parseSessionBody(body: string): { items: ParsedItem[]; notice: string } {
  if (!body.trim()) return { items: [], notice: '此阶段没有正文。' }
  try {
    const items = parseSessionItems(body)
    return { items, notice: items.length ? '' : '此正文没有可识别的消息结构，可展开原文查看。' }
  } catch (error) {
    return { items: [], notice: error instanceof Error ? error.message : '正文无法解析，可展开原文查看。' }
  }
}
