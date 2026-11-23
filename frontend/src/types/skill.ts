// Skills 管理类型：本地托管、备份与远程检索。

export interface SkillLocation {
  key: string
  agent: string
  path: string
  sourceType: string
  readOnly: boolean
  exists: boolean
}

export type SkillLanguage = 'zh' | 'en'

export interface ManagedSkill {
  locationKey: string
  agent: string
  name: string
  displayName?: string
  language?: SkillLanguage
  hasTranslation?: boolean
  translatedName?: string
  translatedDescription?: string
  translationLanguage?: SkillLanguage
  note?: string
  description: string
  path: string
  size: number
  modifiedAt: string
  valid: boolean
  issue?: string
  sourceType: string
  readOnly: boolean
}

export interface SkillTranslationTask {
  id: string
  running: boolean
  current: string
  index: number
  total: number
  retry: boolean
  succeeded: number
  skipped: number
  failures: Array<{ name: string; error: string }>
}

export interface SkillSyncReport {
  synced: number
  removed: number
  skipped: number
  errors?: string[]
}

export interface SkillsResponse {
  locations: SkillLocation[]
  skills: ManagedSkill[]
  manifest?: Record<string, string[]>
  managedAgents?: string[]
}

export interface SkillBackup {
  found: boolean
  translated?: string
  translatedName?: string
  translatedDescription?: string
  translationLanguage?: SkillLanguage
  path?: string
  createdAt?: string
  model?: string
  channelName?: string
}

export interface SkillSearchResult {
  id: string
  skillId: string
  name: string
  source: string
  installs: number
  repositoryUrl?: string
  skillsUrl?: string
}

export interface SkillSearchResponse {
  query: string
  searchType: string
  skills: SkillSearchResult[]
}

export interface RemoteSkillFile {
  path: string
  size: number
}

export interface RemoteSkillPreview {
  id: string
  name: string
  description: string
  source: string
  repositoryUrl: string
  skillUrl: string
  commit: string
  license?: string
  files: RemoteSkillFile[]
  containsScripts: boolean
}
