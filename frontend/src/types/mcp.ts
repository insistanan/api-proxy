// MCP 配置分发类型：统一服务器列表，以及各智能体配置文件的读取与写入结果。

/** 传输方式：stdio 使用 command/args/env，http 使用 url/headers。 */
export type McpTransport = 'stdio' | 'http'

/** 支持分发 MCP 配置的智能体标识。 */
export type McpAgentKey = 'claude-code' | 'codex' | 'opencode' | 'pi' | 'dsh'

export interface McpConnection {
  transport: McpTransport
  command?: string
  args?: string[]
  env?: Record<string, string>
  url?: string
  headers?: Record<string, string>
}

export interface McpServer extends McpConnection {
  name: string
  agents: McpAgentKey[]
  agentConfigs?: Partial<Record<McpAgentKey, McpConnection>>
}

/** 智能体配置文件状态；path 解析失败时 path 为空，error 说明原因。 */
export interface AgentStatus {
  key: McpAgentKey
  name: string
  path: string
  exists: boolean
  count: number
  active: number
  revision: string
  error?: string
}

/** 从应用配置读取的条目；不支持的条目保留名称和错误原因，不允许导入。 */
export interface DetectedMcpServer {
  name: string
  agent: McpAgentKey
  enabled: boolean
  server?: McpServer
  error?: string
  warning?: string
}

export interface McpListResponse {
  revision: string
  servers: McpServer[]
  agents: AgentStatus[]
  detected: DetectedMcpServer[]
}

export interface McpSaveRequest {
  revision: string
  servers: McpServer[]
  adoptions?: McpAdoption[]
}

/** 直接编辑或删除应用条目时，携带原配置与版本以防覆盖外部修改。 */
export interface McpAdoption {
  agent: McpAgentKey
  agentRevision: string
  server: McpServer
  replacement: string
}

export interface McpToggleRequest {
  revision: string
  agentRevision: string
  agent: McpAgentKey
  server: McpServer
  enabled: boolean
}

/** 单个智能体配置的写入结果；ok 为 false 时列表已保存，但该智能体配置文件未更新。 */
export interface AgentResult {
  key: McpAgentKey
  name: string
  path: string
  ok: boolean
  error?: string
}

export interface McpSaveResponse {
  success: boolean
  revision: string
  agents: AgentResult[]
}
