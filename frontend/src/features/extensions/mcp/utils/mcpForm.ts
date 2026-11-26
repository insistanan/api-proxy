// MCP 编辑表单的纯映射与校验，不持有页面或请求状态。
import type { McpAgentKey, McpConnection, McpServer, McpTransport } from '@/types'

export const AGENT_OPTIONS: { key: McpAgentKey; label: string }[] = [
  { key: 'claude-code', label: 'Claude Code' },
  { key: 'codex', label: 'Codex' },
  { key: 'opencode', label: 'OpenCode' },
  { key: 'pi', label: 'pi' },
  { key: 'dsh', label: 'DSH' }
]

export interface McpFormState {
  name: string
  transport: McpTransport
  command: string
  argsText: string
  envText: string
  url: string
  headersText: string
  agents: McpAgentKey[]
}

export const emptyForm = (): McpFormState => ({
  name: '', transport: 'stdio', command: '', argsText: '', envText: '', url: '', headersText: '', agents: []
})

// 用 JSON 回填已有数据，完整保留空参数、换行和首尾空格。
const recordToText = (record?: Record<string, string>) => JSON.stringify(record ?? {}, null, 2)

// 解析 KEY=VALUE 多行文本。格式错误或重复键直接抛错，由表单展示，不静默丢弃。
export const parseKeyValueLines = (text: string, label: string): Record<string, string> => {
  if (text.trim().startsWith('{')) {
    const parsed: unknown = JSON.parse(text)
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error(`${label}必须是 JSON 对象`)
    const entries = Object.entries(parsed)
    if (entries.some(([key, value]) => !key || typeof value !== 'string')) throw new Error(`${label}的键不能为空，值必须是字符串`)
    return Object.fromEntries(entries)
  }
  const entries = new Map<string, string>()
  const lines = text.split(/\r?\n/)
  for (let index = 0; index < lines.length; index++) {
    const line = lines[index]
    if (!line.trim()) continue
    const separator = line.indexOf('=')
    const key = separator > 0 ? line.slice(0, separator).trim() : ''
    if (!key) throw new Error(`${label}第 ${index + 1} 行格式应为 KEY=VALUE`)
    if (entries.has(key)) throw new Error(`${label}中的 ${key} 重复出现`)
    entries.set(key, line.slice(separator + 1))
  }
  return Object.fromEntries(entries)
}

export const parseArgs = (text: string): string[] => {
  if (text.trim().startsWith('[')) {
    const parsed: unknown = JSON.parse(text)
    if (!Array.isArray(parsed) || parsed.some(value => typeof value !== 'string')) throw new Error('参数必须是 JSON 字符串数组')
    return parsed
  }
  return text.split(/\r?\n/).filter(line => line.length > 0)
}

export const validateHttpUrl = (value: string): void => {
  let parsed: URL
  try {
    parsed = new URL(value)
  } catch {
    throw new Error('URL 格式无效，请填写完整地址，例如 https://example.com/mcp')
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new Error('URL 必须以 http:// 或 https:// 开头')
  }
}

// 统一生成提交与比较用的规范结构：字段顺序固定、空的可选字段省略、agents 按 AGENT_OPTIONS 排序。
// 切换传输方式时已清空的字段不会出现在结构中，避免残留值被提交。
export const toPayload = (server: McpServer): McpServer => {
  const agents = AGENT_OPTIONS.map(option => option.key).filter(key => server.agents.includes(key))
  const agentConfigs: McpServer['agentConfigs'] = {}
  for (const { key } of AGENT_OPTIONS) {
    const config = server.agentConfigs?.[key]
    if (config) agentConfigs[key] = connectionOf(toPayload({ ...config, name: server.name, agents: [] }))
  }
  const variants = Object.keys(agentConfigs).length ? { agentConfigs } : {}
  if (server.transport === 'stdio') {
    const args = server.args ?? []
    const env = server.env ?? {}
    return {
      name: server.name,
      transport: 'stdio',
      command: server.command,
      ...(args.length > 0 ? { args: [...args] } : {}),
      ...(Object.keys(env).length > 0 ? { env: { ...env } } : {}),
      agents,
      ...variants
    }
  }
  if (server.transport === 'http') {
    const headers = server.headers ?? {}
    return {
      name: server.name,
      transport: 'http',
      url: server.url,
      ...(Object.keys(headers).length > 0 ? { headers: { ...headers } } : {}),
      agents,
      ...variants
    }
  }
  throw new Error(`MCP ${server.name} 的传输方式无效：${String(server.transport)}`)
}

export const connectionOf = (server: McpConnection): McpConnection => ({
  transport: server.transport,
  ...(server.command !== undefined ? { command: server.command } : {}),
  ...(server.args ? { args: [...server.args] } : {}),
  ...(server.env ? { env: { ...server.env } } : {}),
  ...(server.url !== undefined ? { url: server.url } : {}),
  ...(server.headers ? { headers: { ...server.headers } } : {})
})
export const configForAgent = (server: McpServer, agent: McpAgentKey): McpServer => ({
  ...connectionOf(server.agentConfigs?.[agent] ?? server), name: server.name, agents: [...server.agents]
})

// 比较配置内容时忽略来源和可选集合的空值，并固定字典顺序，避免把相同配置误判为冲突。
export const configKey = (server: McpServer): string => JSON.stringify({
  name: server.name,
  transport: server.transport,
  command: server.command ?? '',
  args: server.args ?? [],
  env: Object.entries(server.env ?? {}).sort(([a], [b]) => a.localeCompare(b)),
  url: server.url ?? '',
  headers: Object.entries(server.headers ?? {}).sort(([a], [b]) => a.localeCompare(b))
})

// 仅折叠已拆分参数、没有 shell 元字符的 npx 包装，复杂 shell 命令仍按不同配置处理。
export const identityKey = (server: McpServer): string => {
  let command = server.command ?? ''
  let args = [...(server.args ?? [])]
  if (server.transport === 'stdio' && /^(cmd(?:\.exe)?)$/i.test(command)
    && args[0]?.toLowerCase() === '/c' && /^npx(?:\.cmd)?$/i.test(args[1] ?? '')
    && args.slice(2).every(arg => arg.length > 0 && !/[\s&|<>^%!"'()]/.test(arg))) {
    command = 'npx'
    args = args.slice(2)
  } else if (/^npx(?:\.cmd)?$/i.test(command)) command = 'npx'
  return configKey({ ...server, command, args })
}

export const formFromServer = (server: McpServer): McpFormState => ({
  name: server.name,
  transport: server.transport,
  command: server.command ?? '',
  argsText: JSON.stringify(server.args ?? [], null, 2),
  envText: recordToText(server.env),
  url: server.url ?? '',
  headersText: recordToText(server.headers),
  agents: [...server.agents]
})
