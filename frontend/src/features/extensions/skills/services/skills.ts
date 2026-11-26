// 技能管理 API：复用统一 HTTP 基础层，保留端点与返回契约。
import { apiRequest } from '@/shared/services/http'
import type {
  RemoteSkillPreview,
  SkillBackup,
  SkillLanguage,
  SkillSearchResponse,
  SkillSyncReport,
  SkillTranslationTask,
  SkillsResponse
} from '@/types'

export const skillsApi = {
  // ============== 本机 Skills 管理 API ==============
  async getSkills(): Promise<SkillsResponse> {
    return apiRequest('/skills')
  },

  async getSkillContent(
    locationKey: string,
    name: string
  ): Promise<{ content: string; path: string; language: SkillLanguage }> {
    return apiRequest('/skills/content', { method: 'POST', body: JSON.stringify({ locationKey, name }) })
  },

  async getLatestSkillBackup(locationKey: string, name: string): Promise<SkillBackup> {
    return apiRequest('/skills/backup/latest', { method: 'POST', body: JSON.stringify({ locationKey, name }) })
  },

  async updateSkillNote(name: string, note: string): Promise<{ success: boolean; note: string }> {
    return apiRequest('/skills/note', { method: 'POST', body: JSON.stringify({ name, note }) })
  },

  async consolidateSkills(): Promise<{ success: boolean; total: number; imported: number; duplicates: number }> {
    return apiRequest('/skills/consolidate', { method: 'POST' })
  },

  async importSkill(
    fileName: string,
    contentBase64: string,
    targets: string[]
  ): Promise<{ success: boolean; name: string }> {
    return apiRequest('/skills/import', {
      method: 'POST',
      body: JSON.stringify({ fileName, contentBase64, targets })
    })
  },

  async searchSkills(query: string): Promise<SkillSearchResponse> {
    return apiRequest(`/skills/search?q=${encodeURIComponent(query)}`)
  },

  async inspectRemoteSkill(id: string): Promise<RemoteSkillPreview> {
    return apiRequest('/skills/remote/inspect', { method: 'POST', body: JSON.stringify({ id }) })
  },

  async installRemoteSkill(
    id: string,
    targets: string[],
    commit: string
  ): Promise<{ success: boolean; name: string; source: string; repositoryUrl: string; commit: string }> {
    return apiRequest('/skills/remote/install', { method: 'POST', body: JSON.stringify({ id, targets, commit }) })
  },

  async copySkill(
    locationKey: string,
    name: string,
    targets: string[]
  ): Promise<{ success: boolean; name: string; targets: string[] }> {
    return apiRequest('/skills/copy', { method: 'POST', body: JSON.stringify({ locationKey, name, targets }) })
  },

  async toggleSkillAgent(
    name: string,
    agent: string,
    enabled: boolean
  ): Promise<{ success: boolean; manifest: Record<string, string[]> }> {
    return apiRequest('/skills/toggle', {
      method: 'POST',
      body: JSON.stringify({ name, agent, enabled })
    })
  },

  async syncSkills(): Promise<{ success: boolean; report: SkillSyncReport }> {
    return apiRequest('/skills/sync', { method: 'POST' })
  },

  async deleteSkill(locationKey: string, name: string): Promise<void> {
    await apiRequest('/skills', { method: 'DELETE', body: JSON.stringify({ locationKey, name }) })
  },

  async getSkillTranslation(): Promise<{ task: SkillTranslationTask | null }> {
    return apiRequest('/skills/translation')
  },

  async cancelSkillTranslation(taskId: string): Promise<{ task: SkillTranslationTask }> {
    return apiRequest(`/skills/translation/${encodeURIComponent(taskId)}`, { method: 'DELETE' })
  },

  async startSkillTranslation(payload: {
    channelId: string
    protocol: string
    model: string
  }): Promise<{ task: SkillTranslationTask }> {
    return apiRequest('/skills/translation', { method: 'POST', body: JSON.stringify(payload) })
  },

  async backupSkill(payload: {
    locationKey: string
    name: string
    original: string
    translated: string
    model?: string
    channelName?: string
    automatic?: boolean
  }): Promise<{
    success: boolean
    path: string
    translatedName: string
    translatedDescription: string
    translationLanguage: SkillLanguage
  }> {
    return apiRequest('/skills/backup', { method: 'POST', body: JSON.stringify(payload) })
  }
}
