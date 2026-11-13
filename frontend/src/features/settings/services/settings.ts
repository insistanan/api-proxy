// 网关设置 API：复用统一 HTTP 基础层，保留端点与返回契约。
import { apiRequest } from '@/shared/services/http'
import type { AppSettings, ClientDisguiseStatus } from '@/types'
import type { ApiTab } from '@/types'

export const settingsApi = {
  // 获取网关设置
  async getSettings(): Promise<AppSettings> {
    return apiRequest('/settings')
  },

  async updateSettings(settings: Partial<AppSettings>): Promise<AppSettings> {
    return apiRequest('/settings', {
      method: 'PUT',
      body: JSON.stringify(settings)
    })
  },

  // 获取 Fuzzy 模式状态
  async getFuzzyMode(): Promise<{ fuzzyModeEnabled: boolean; fuzzyModeByProtocol: Record<ApiTab, boolean> }> {
    return apiRequest('/settings/fuzzy-mode')
  },

  // 设置 Fuzzy 模式状态
  async setFuzzyMode(protocol: ApiTab, enabled: boolean): Promise<void> {
    await apiRequest('/settings/fuzzy-mode', {
      method: 'PUT',
      body: JSON.stringify({ protocol, enabled })
    })
  },

  // 获取 Messages / Responses 客户端伪装状态
  async getClientDisguise(): Promise<ClientDisguiseStatus> {
    return apiRequest('/settings/client-disguise')
  },

  async refreshClientDisguise(): Promise<{ queued: boolean }> {
    return apiRequest('/settings/client-disguise/sync', { method: 'POST' })
  },

  // 设置指定协议的客户端伪装状态
  async setClientDisguise(protocol: 'messages' | 'responses', enabled: boolean): Promise<void> {
    await apiRequest('/settings/client-disguise', {
      method: 'PUT',
      body: JSON.stringify({ protocol, enabled })
    })
  },
}
