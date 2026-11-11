// 网关与集成设置 API：复用统一 HTTP 基础层，保留端点与返回契约。
import { apiRequest } from './http'
import type { AppSettings, ClientDisguiseStatus } from '@/types'
import type { ApiTab } from './channel-api'

export const settingsApi = {
  // 获取网关与集成设置
  async getSettings(): Promise<AppSettings> {
    return apiRequest('/settings')
  },

  async updateSettings(settings: Partial<AppSettings>): Promise<AppSettings> {
    return apiRequest('/settings', {
      method: 'PUT',
      body: JSON.stringify(settings)
    })
  },

  /** 弹出原生文件选择对话框选择 CC Switch 路径并保存；用户取消返回空串 */
  async pickCcsPath(): Promise<{ path: string }> {
    return apiRequest('/settings/ccs/pick-path', { method: 'POST' })
  },

  /** 以 deeplink 为参数拉起 CC Switch，由用户在其确认框中完成导入 */
  async importToCcs(url: string): Promise<{ success: boolean }> {
    return apiRequest('/settings/ccs/import', {
      method: 'POST',
      body: JSON.stringify({ url })
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
