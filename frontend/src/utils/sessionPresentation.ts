// 五协议正文的展示解析；不参与转发或会话身份判断。
// 正文分三类处理：完整 JSON 按消息结构解析，纯文本（如流式合成结果）按文本轨迹展示，
// 疑似截断的 JSON 只给一行说明，不再把 JSON.parse 的原始报错抛给界面。
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

export type SessionBodyMode = 'json' | 'text' | 'invalid' | 'unknown' | 'empty'

export interface ParsedSessionBody {
  items: ParsedItem[]
  notice: string
  mode: SessionBodyMode
}

export type TraceBlockKind = 'user' | 'assistant' | 'reasoning' | 'tool_call' | 'tool_result' | 'system'

/** 一条按时间顺序展示的轨迹块，对应参照图里的 user / assistant.reasoning / assistant.tool_calls / tool（结果）/ assistant.content。 */
export interface TraceBlock {
  kind: TraceBlockKind
  label: string
  text: string
  meta?: string
  isError?: boolean
  /** 客户端请求体里携带的历史助手回复，默认折叠。 */
  muted?: boolean
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === 'object' && !Array.isArray(value)

const readString = (value: unknown): string => typeof value === 'string' ? value : ''

const appendLine = (previous: string | undefined, next: string): string => {
  if (!next) return previous || ''
  return previous ? `${previous}\n${next}` : next
}

const stringifyContent = (value: unknown): string => {
  if (typeof value === 'string') return value
  if (value == null) return ''
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

/** 文本正文或内容块数组取纯文本，避免把结构化数组整体 JSON 化。 */
const readContentText = (value: unknown): string => {
  if (typeof value === 'string') return value
  if (Array.isArray(value)) {
    return value
      .map(part => isRecord(part) ? readString(part.text) || readString(part.content) : readString(part))
      .filter(Boolean)
      .join('\n')
  }
  return stringifyContent(value)
}

/** 工具结果优先按纯文本块拼接，混合多模态内容仍保留完整 JSON。 */
const stringifyToolContent = (value: unknown): string => {
  if (typeof value === 'string') return value
  if (Array.isArray(value) && value.length > 0) {
    const texts = value.map(part => isRecord(part) && readString(part.type) === 'text' ? readString(part.text) : '')
    if (texts.every(Boolean)) return texts.join('\n')
  }
  return stringifyContent(value)
}

function parseClaudeMessages(raw: unknown[]): ParsedItem[] {
  const items: ParsedItem[] = []
  for (const entry of raw) {
    if (!isRecord(entry)) continue
    const item: ParsedItem = { role: readString(entry.role) || 'user' }
    if (typeof entry.content === 'string') {
      item.text = entry.content
    } else if (Array.isArray(entry.content)) {
      for (const block of entry.content) {
        if (!isRecord(block)) continue
        const type = readString(block.type)
        if (type === 'text') {
          item.text = appendLine(item.text, readString(block.text))
        } else if (type === 'thinking' || type === 'redacted_thinking') {
          item.thinking = appendLine(item.thinking, readString(block.thinking) || '[redacted thinking]')
        } else if (type === 'tool_use') {
          if (!item.toolCalls) item.toolCalls = []
          item.toolCalls.push({ id: readString(block.id), name: readString(block.name) || 'tool', args: block.input })
        } else if (type === 'tool_result') {
          if (!item.toolResults) item.toolResults = []
          item.toolResults.push({ toolUseId: readString(block.tool_use_id), content: stringifyToolContent(block.content), isError: Boolean(block.is_error) })
        }
      }
    }
    if (Array.isArray(entry.tool_calls)) {
      item.toolCalls = entry.tool_calls.filter(isRecord).map(call => {
        const fn = isRecord(call.function) ? call.function : undefined
        return { id: readString(call.id), name: readString(fn?.name) || 'function', args: fn?.arguments }
      })
    }
    if (item.role === 'tool') {
      // 工具角色统一走 toolResults，避免正文与结果块重复展示。
      item.text = undefined
      item.toolResults = [{ toolUseId: readString(entry.tool_call_id), content: readContentText(entry.content) }]
    }
    items.push(item)
  }
  return items
}

function parseChatCompletion(data: Record<string, unknown>): ParsedItem[] {
  const first = Array.isArray(data.choices) && isRecord(data.choices[0]) ? data.choices[0] : undefined
  const message = first && isRecord(first.message) ? first.message : undefined
  if (!message) return []
  const item: ParsedItem = { role: readString(message.role) || 'assistant', text: readContentText(message.content) }
  const reasoning = readString(message.reasoning_content)
  if (reasoning) item.thinking = reasoning
  if (Array.isArray(message.tool_calls)) {
    item.toolCalls = message.tool_calls.filter(isRecord).map(call => {
      const fn = isRecord(call.function) ? call.function : undefined
      return { id: readString(call.id), name: readString(fn?.name) || 'function', args: fn?.arguments }
    })
  }
  return [item]
}

function parseResponsesItems(raw: unknown[]): ParsedItem[] {
  const items: ParsedItem[] = []
  for (const entry of raw) {
    if (typeof entry === 'string') {
      items.push({ role: 'user', text: entry })
      continue
    }
    if (!isRecord(entry)) continue
    const type = readString(entry.type)
    // Responses 的 reasoning / custom_tool_call / custom_tool_call_output 是 GPT-5.1 Codex 等
    // 客户端的常用条目，必须显式识别，否则整条 input 会被判成“没有可识别的消息结构”。
    if (type.includes('reasoning') || type === 'thinking') {
      const parts: unknown[] = Array.isArray(entry.summary) ? entry.summary : Array.isArray(entry.content) ? entry.content : []
      const text = parts
        .map(part => isRecord(part) ? readString(part.text) || readString(part.summary_text) : readString(part))
        .filter(Boolean)
        .join('\n') || readString(entry.text)
      if (text) items.push({ role: 'assistant', thinking: text })
      continue
    }
    if (type === 'function_call' || type === 'custom_tool_call' || type === 'local_shell_call') {
      items.push({
        role: 'assistant',
        toolCalls: [{
          id: readString(entry.call_id) || readString(entry.id),
          name: readString(entry.name) || type,
          args: entry.arguments ?? entry.input ?? entry.action ?? {}
        }]
      })
      continue
    }
    if (type === 'function_call_output' || type === 'custom_tool_call_output' || type.endsWith('_output')) {
      items.push({
        role: 'tool',
        toolResults: [{
          toolUseId: readString(entry.call_id),
          content: stringifyToolContent(entry.output ?? entry.content),
          isError: Boolean(entry.is_error)
        }]
      })
      continue
    }
    if (readString(entry.role)) {
      items.push({ role: readString(entry.role), text: readContentText(entry.content) })
    }
  }
  return items
}

function parseGeminiContents(raw: unknown[]): ParsedItem[] {
  const items: ParsedItem[] = []
  for (const entry of raw) {
    if (!isRecord(entry)) continue
    const item: ParsedItem = { role: readString(entry.role) === 'model' ? 'assistant' : 'user' }
    if (Array.isArray(entry.parts)) {
      for (const part of entry.parts) {
        if (!isRecord(part)) continue
        if (part.thought) {
          item.thinking = appendLine(item.thinking, readString(part.text))
        } else if (readString(part.text)) {
          item.text = appendLine(item.text, readString(part.text))
        } else if (isRecord(part.functionCall)) {
          if (!item.toolCalls) item.toolCalls = []
          item.toolCalls.push({ name: readString(part.functionCall.name) || 'function', args: part.functionCall.args })
        } else if (isRecord(part.functionResponse)) {
          if (!item.toolResults) item.toolResults = []
          item.toolResults.push({ toolUseId: readString(part.functionResponse.name), content: stringifyContent(part.functionResponse.response) })
        }
      }
    }
    items.push(item)
  }
  return items
}

function parseSessionItems(data: unknown): ParsedItem[] {
  if (!isRecord(data)) return []
  if (Array.isArray(data.messages)) return parseClaudeMessages(data.messages)
  if (Array.isArray(data.content) && typeof data.role === 'string') return parseClaudeMessages([data])
  if (Array.isArray(data.choices)) return parseChatCompletion(data)
  const responseItems = Array.isArray(data.input) ? data.input
    : Array.isArray(data.output) ? data.output
      : typeof data.input === 'string' ? [{ type: 'message', role: 'user', content: data.input }] : undefined
  if (responseItems) return parseResponsesItems(responseItems)
  const geminiContents = Array.isArray(data.contents) ? data.contents
    : Array.isArray(data.candidates)
      ? data.candidates
        .map(candidate => isRecord(candidate) ? candidate.content : undefined)
        .filter((value): value is Record<string, unknown> => isRecord(value))
      : undefined
  if (geminiContents) return parseGeminiContents(geminiContents)
  return []
}

// 流式合成器把工具调用追加成 “Tool Call: name(args) [ID: x]” 行，解析成独立块以便按轨迹展示。
const TOOL_CALL_PATTERN = /^Tool Call: (.+?)\((.+)\)(?: \[ID: (.*)\])?\s*$/

function parseSynthesizedText(body: string): ParsedItem[] {
  const items: ParsedItem[] = []
  let buffer: string[] = []
  const flush = () => {
    const text = buffer.join('\n').trim()
    if (text) items.push({ role: 'text', text })
    buffer = []
  }
  for (const line of body.split(/\r?\n/)) {
    const match = TOOL_CALL_PATTERN.exec(line)
    if (!match) {
      buffer.push(line)
      continue
    }
    flush()
    const rawArgs = match[2] || ''
    let args: unknown = rawArgs
    try { args = JSON.parse(rawArgs) } catch { args = rawArgs }
    items.push({ role: 'assistant', toolCalls: [{ id: match[3] || undefined, name: (match[1] || 'tool').trim(), args }] })
  }
  flush()
  return items
}

export function parseSessionBody(body: string): ParsedSessionBody {
  const trimmed = (body || '').trim()
  if (!trimmed) return { items: [], notice: '此阶段没有正文。', mode: 'empty' }
  const looksJson = trimmed.startsWith('{') || trimmed.startsWith('[')
  try {
    const items = parseSessionItems(JSON.parse(trimmed))
    return items.length
      ? { items, notice: '', mode: 'json' }
      : { items, notice: '此正文没有可识别的消息结构，可展开原文查看。', mode: 'unknown' }
  } catch {
    if (looksJson) {
      return { items: [], notice: '正文解析失败：不是完整 JSON（可能被截断），可展开原文查看。', mode: 'invalid' }
    }
    const items = parseSynthesizedText(trimmed)
    return { items, notice: '', mode: 'text' }
  }
}

const compactArgs = (args: unknown): string => {
  if (args == null) return ''
  if (typeof args === 'string') {
    try { return JSON.stringify(JSON.parse(args)) } catch { return args }
  }
  try { return JSON.stringify(args) ?? '' } catch { return String(args) }
}

/** 把消息结构展开成按时间顺序的轨迹块：思考 → 工具调用 → 工具结果 → 文本。 */
export function buildTraceBlocks(items: ParsedItem[], options: { foldAssistantHistory?: boolean } = {}): TraceBlock[] {
  const blocks: TraceBlock[] = []
  for (const item of items) {
    const role = (item.role || 'assistant').toLowerCase()
    if (item.thinking) {
      blocks.push({ kind: 'reasoning', label: '助手思考', text: item.thinking })
    }
    for (const call of item.toolCalls || []) {
      blocks.push({
        kind: 'tool_call',
        label: '工具调用',
        text: `${call.name}(${compactArgs(call.args)})`,
        meta: call.id
      })
    }
    for (const result of item.toolResults || []) {
      blocks.push({
        kind: 'tool_result',
        label: '工具结果',
        text: result.content || '（空结果）',
        meta: result.toolUseId,
        isError: result.isError
      })
    }
    if (item.text) {
      const kind: TraceBlockKind = role === 'user' ? 'user' : role === 'system' ? 'system' : role === 'tool' ? 'tool_result' : 'assistant'
      const label = kind === 'user' ? '用户输入' : kind === 'system' ? '系统消息' : kind === 'tool_result' ? '工具结果' : role === 'text' ? '文本内容' : '助手回复'
      blocks.push({ kind, label, text: item.text, muted: options.foldAssistantHistory && role === 'assistant' })
    }
  }
  return blocks
}
