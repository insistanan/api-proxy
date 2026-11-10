// 智能体配置 API：复用统一 HTTP 基础层，保留端点与返回契约。
import { apiRequest } from './http'
import type {
  AgentPromptAgent,
  AgentPromptFile,
  ClaudeCodeSettings,
  CodexSettings,
  DSHSettings,
  ModelsResponse,
  OpenCodeConfig,
  PiAgentDiscoverResult,
  PiAgentModelSettings,
  PiAgentModelSettingsResponse,
  PiAgentProbeResult,
  PiAgentProvider,
  PiAgentProvidersResponse,
  PiAgentStatus,
  SaveClaudeCodeSettings,
  SaveCodexModelCatalog,
  SaveCodexSettings,
  SaveDSHSettings,
  SaveOpenCodeProvider,
  ToolVersionInfo
} from '@/types'

export const clientConfigApi = {
  // ============== OpenCode 配置 API ==============
  async getOpenCodeConfig(): Promise<OpenCodeConfig> {
    return apiRequest('/settings/opencode')
  },

  async saveOpenCodeConfig(config: { providers: SaveOpenCodeProvider[]; revision: string }): Promise<{ success: boolean; path: string; revision: string }> {
    return apiRequest('/settings/opencode', {
      method: 'PUT',
      body: JSON.stringify(config)
    })
  },

  // ============== Claude Code 配置 API ==============
  async getClaudeCodeSettings(): Promise<ClaudeCodeSettings> {
    return apiRequest('/settings/claude-code')
  },

  async saveClaudeCodeSettings(settings: SaveClaudeCodeSettings): Promise<{ success: boolean; path: string }> {
    return apiRequest('/settings/claude-code', {
      method: 'PUT',
      body: JSON.stringify(settings)
    })
  },

  // ============== GPT / Codex 配置 API ==============
  async getCodexSettings(): Promise<CodexSettings> {
    return apiRequest('/settings/codex')
  },

  async saveCodexSettings(
    settings: SaveCodexSettings
  ): Promise<{ success: boolean; configPath: string; authPath: string }> {
    return apiRequest('/settings/codex', {
      method: 'PUT',
      body: JSON.stringify(settings)
    })
  },

  async saveCodexModelCatalog(
    payload: SaveCodexModelCatalog
  ): Promise<{ success: boolean; modelsPath: string; configPath: string }> {
    return apiRequest('/settings/codex/model-catalog', {
      method: 'PUT',
      body: JSON.stringify(payload)
    })
  },

  // ============== 本代理模型列表（客户端配置页用） ==============

  /**
   * 获取本代理自身暴露的模型列表（等同 /v1/models 的内容，但走 Web 鉴权）。
   * 返回条目的 owned_by 形如 "pool:messages" / "pool:messages,responses"，
   * 标识该模型属于哪些协议分组（包含同名家族别名）；"api-proxy" 表示未配置同名分组的静态别名。
   */
  async getProxyModels(): Promise<ModelsResponse> {
    return apiRequest('/proxy-models')
  },

  // ============== CLI 工具版本检测（客户端配置页用） ==============

  /**
   * 查询指定 CLI 工具（claude / codex / opencode / pi）的当前版本、
   * npm 最新版本与当前平台的安装命令。force=true 绕过后端 1 小时缓存。
   */
  async getToolVersion(tool: string, force = false): Promise<ToolVersionInfo> {
    const query = force ? '?force=true' : ''
    return apiRequest(`/tools/${encodeURIComponent(tool)}/version${query}`)
  },

  // ============== DSH 配置 API ==============
  async getDSHSettings(): Promise<DSHSettings> {
    return apiRequest('/settings/dsh')
  },

  async saveDSHSettings(settings: SaveDSHSettings): Promise<{ success: boolean; path: string; credentialsPath: string }> {
    return apiRequest('/settings/dsh', {
      method: 'PUT',
      body: JSON.stringify(settings)
    })
  },

  // ============== 智能体全局提示词 API（客户端配置页"全局提示词"卡片） ==============

  /** 读取指定智能体的全局提示词文件（文件不存在返回 exists=false，不算错误） */
  async getAgentPrompt(agent: AgentPromptAgent): Promise<AgentPromptFile> {
    return apiRequest(`/settings/agent-prompt/${encodeURIComponent(agent)}`)
  },

  /** 整体替换指定智能体的全局提示词文件；后端会先备份原文件再原子写入 */
  async saveAgentPrompt(agent: AgentPromptAgent, content: string): Promise<{ success: boolean; path: string; sizeBytes: number }> {
    return apiRequest(`/settings/agent-prompt/${encodeURIComponent(agent)}`, {
      method: 'PUT',
      body: JSON.stringify({ content })
    })
  },

  // ============== pi-agent 配置管理 API ==============
  async getPiAgentStatus(): Promise<PiAgentStatus> {
    return apiRequest('/settings/pi-agent')
  },

  async getPiAgentProviders(): Promise<PiAgentProvidersResponse> {
    return apiRequest('/settings/pi-agent/providers')
  },

  async createPiAgentProvider(payload: Record<string, unknown>): Promise<{ success: boolean; revision: string }> {
    return apiRequest('/settings/pi-agent/providers', {
      method: 'POST',
      body: JSON.stringify(payload)
    })
  },

  async updatePiAgentProvider(
    id: string,
    payload: Record<string, unknown>
  ): Promise<{ success: boolean; revision: string }> {
    return apiRequest(`/settings/pi-agent/providers/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(payload)
    })
  },

  async deletePiAgentProvider(id: string, revision: string): Promise<{ success: boolean; revision: string }> {
    return apiRequest(`/settings/pi-agent/providers/${encodeURIComponent(id)}`, {
      method: 'DELETE',
      body: JSON.stringify({ revision })
    })
  },

  async validatePiAgentProvider(payload: {
    id: string
    provider: Partial<PiAgentProvider>
  }): Promise<{ valid: boolean; errors: string[] }> {
    return apiRequest('/settings/pi-agent/validate', {
      method: 'POST',
      body: JSON.stringify(payload)
    })
  },

  async testPiAgentProvider(
    id: string,
    payload: { baseUrl?: string; apiKey?: string; api?: string }
  ): Promise<PiAgentProbeResult> {
    return apiRequest(`/settings/pi-agent/providers/${encodeURIComponent(id)}/test`, {
      method: 'POST',
      body: JSON.stringify(payload)
    })
  },

  async discoverPiAgentModels(
    id: string,
    payload: { baseUrl?: string; apiKey?: string; api?: string }
  ): Promise<PiAgentDiscoverResult> {
    return apiRequest(`/settings/pi-agent/providers/${encodeURIComponent(id)}/discover-models`, {
      method: 'POST',
      body: JSON.stringify(payload)
    })
  },

  async getPiAgentModelSettings(): Promise<PiAgentModelSettingsResponse> {
    return apiRequest('/settings/pi-agent/model-settings')
  },

  async updatePiAgentModelSettings(payload: {
    revision: string
    settings: Partial<PiAgentModelSettings>
  }): Promise<{ success: boolean; revision: string }> {
    return apiRequest('/settings/pi-agent/model-settings', {
      method: 'PATCH',
      body: JSON.stringify(payload)
    })
  },
}
