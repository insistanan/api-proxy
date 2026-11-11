// MCP 配置分发 API：复用统一 HTTP 基础层。409 等冲突由视图根据 ApiError.status 区分处理。
import { apiRequest } from './http'
import type { McpAdoption, McpListResponse, McpSaveResponse, McpServer, McpToggleRequest } from '@/types'

export const mcpApi = {
  async toggleMcp(request: McpToggleRequest): Promise<McpSaveResponse> {
    return apiRequest('/mcp/toggle', { method: 'POST', body: JSON.stringify(request) })
  },
  async getMcp(): Promise<McpListResponse> {
    return apiRequest('/mcp')
  },

  // revision 为最近一次读取或保存返回的版本号，后端据此检测外部修改（409 revision_conflict）。
  async saveMcp(revision: string, servers: McpServer[], adoptions: McpAdoption[] = []): Promise<McpSaveResponse> {
    return apiRequest('/mcp', { method: 'PUT', body: JSON.stringify({ revision, servers, adoptions }) })
  },
}
