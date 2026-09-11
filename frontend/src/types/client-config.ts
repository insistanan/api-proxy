// 四客户端配置页（OpenCode / ClaudeCode / pi-agent / DSH）共用的配置类型。

// ============== CLI 工具版本检测（客户端配置页顶部横幅） ==============

/** GET /api/tools/:tool/version 的响应 */
export interface ToolVersionInfo {
  tool: string
  displayName: string
  /** 当前已安装版本，空 = 未安装 */
  current: string
  currentError: string
  /** npm latest 版本，空 = 未知 */
  latest: string
  latestError: string
  /** 当前平台的安装/升级命令 */
  installCmd: string
  /** 结果生成时间（Unix 毫秒） */
  detectedAt: number
}

export type OpenCodeProtocol = 'chat' | 'responses' | 'messages' | 'gemini' | 'custom'

export interface OpenCodeVariant {
  reasoningEffort: string
}

export interface OpenCodeModel {
  key: string
  apiModelId: string
  name: string
  contextLimit: number
  inputLimit: number
  outputLimit: number
  options: Record<string, unknown>
  variants?: Record<string, OpenCodeVariant>
}

export interface OpenCodeProvider {
  id: string
  name: string
  protocol: OpenCodeProtocol
  npm: string
  baseUrl: string
  apiKeyMasked: string
  apiKeyPresent: boolean
  headers: Record<string, string>
  options: Record<string, unknown>
  models: OpenCodeModel[]
}

export interface OpenCodeConfig {
  path: string
  exists: boolean
  jsonc: boolean
  model: string
  smallModel: string
  providers: OpenCodeProvider[]
}

export interface SaveOpenCodeProvider extends Omit<OpenCodeProvider, 'apiKeyMasked' | 'apiKeyPresent'> {
  apiKeyAction: 'keep' | 'replace' | 'remove'
  apiKey?: string
}

export interface ClaudeCodeModelDefault {
  family: string
  model: string
  name: string
  /** 该 family 是否启用 1M 上下文（写入时在模型标识追加 [1m] 后缀） */
  supports1M: boolean
}

export interface ClaudeCodeSettings {
  path: string
  exists: boolean
  jsonc: boolean
  baseUrl: string
  credentialKind: 'authToken' | 'apiKey'
  credentialMasked: string
  credentialPresent: boolean
  modelDefaults: ClaudeCodeModelDefault[]
}

export interface SaveClaudeCodeSettings extends Pick<
  ClaudeCodeSettings,
  'baseUrl' | 'credentialKind' | 'modelDefaults'
> {
  credentialAction: 'keep' | 'replace' | 'remove'
  credential?: string
}

export interface CodexProvider {
  id: string
  name: string
  baseUrl: string
}

/** models.json 中已登记的模型条目视图（builtin=true 表示内置模型的复制品） */
export interface CodexCatalogModel {
  slug: string
  displayName: string
  description: string
  supportsImage: boolean
  contextWindow: number
  defaultReasoningLevel: string
  reasoningLevels: string[]
  supportsSearchTool: boolean
  priority: number
  visibility: string
  builtin: boolean
}

export interface CodexSettings {
  configPath: string
  configExists: boolean
  authPath: string
  authExists: boolean
  modelsPath: string
  modelsExists: boolean
  activeProvider: string
  selectedProvider: string
  providers: CodexProvider[]
  apiKeyMasked: string
  apiKeyPresent: boolean
  model: string
  modelCatalogJson: string
  modelReasoningEffort: string
  catalogModels: CodexCatalogModel[]
  /** 动态获取的 Codex 内置模型清单（codex debug models） */
  builtinModels: CodexCatalogModel[]
  modelsError: string
  builtinError: string
}

export interface SaveCodexSettings {
  provider: string
  baseUrl: string
  apiKeyAction: 'keep' | 'replace' | 'remove'
  apiKey?: string
}

export interface SaveCodexModelCatalog {
  action: 'upsert' | 'delete' | 'setDefault'
  slug: string
  displayName?: string
  description?: string
  supportsImage?: boolean
  contextWindow?: number
  defaultReasoningLevel?: string
  /** setDefault 可选：写入根级 model_reasoning_effort */
  reasoningEffort?: string
  /** setDefault 可选：删除根级 model_reasoning_effort，与 reasoningEffort 互斥 */
  clearReasoningEffort?: boolean
}

// ============== pi-agent 配置管理类型 ==============

export type PiAgentFileKind = 'models.json' | 'auth.json' | 'settings.json'

export interface PiAgentFileStatus {
  path: string
  exists: boolean
  writable: boolean
  revision: string
}

export interface PiAgentStatus {
  configDir: string
  exists: boolean
  writable: boolean
  files: Record<PiAgentFileKind, PiAgentFileStatus>
}

export interface PiAgentModelCost {
  input: number
  output: number
  cacheRead: number
  cacheWrite: number
}

export interface PiAgentModel {
  id: string
  name?: string
  api?: string
  baseUrl?: string
  reasoning?: boolean | null
  thinkingLevelMap?: Record<string, string | null>
  input?: string[]
  contextWindow?: number | null
  maxTokens?: number | null
  cost?: PiAgentModelCost
  headers?: Record<string, string>
  compat?: Record<string, unknown>
}

export interface PiAgentProvider {
  id: string
  name?: string
  api?: string
  baseUrl?: string
  authHeader?: boolean | null
  headers?: Record<string, string>
  compat?: Record<string, unknown>
  models?: PiAgentModel[]
  modelOverrides?: Record<string, unknown>
  hasOAuth?: boolean
  apiKeyMasked?: string
  apiKeyPresent?: boolean
}

export interface PiAgentProvidersResponse {
  revision: string
  providers: PiAgentProvider[]
  rawExists: boolean
}

export interface PiAgentCredential {
  id: string
  type: 'api_key' | 'oauth' | 'unknown'
  keyMasked: string
  keyPresent: boolean
  hasOAuth: boolean
  envMasked: Record<string, string>
  hasEnvValues: boolean
}

export interface PiAgentCredentialsResponse {
  revision: string
  credentials: PiAgentCredential[]
}

export interface PiAgentModelSettings {
  defaultProvider: string
  defaultModel: string
  defaultThinkingLevel: string
  enabledModels: string[]
}

export interface PiAgentModelSettingsResponse {
  revision: string
  settings: PiAgentModelSettings
}

export interface PiAgentBackup {
  id: string
  file: string
  size: number
  created: string
  revision: string
  sensitive: boolean
}

export interface PiAgentBackupsResponse {
  backups: PiAgentBackup[]
}

export interface PiAgentProbeResult {
  success: boolean
  latencyMs?: number
  statusCode?: number
  error?: string
}

export interface PiAgentDiscoverResult {
  success: boolean
  models?: string[]
  method?: string
  error?: string
}

// ============== DSH 配置类型 ==============

export interface DSHModel {
  id: string
  name?: string
  input?: string[]
  reasoningEfforts?: Record<string, string | null> | false
  contextWindow?: number | null
  maxTokens?: number | null
}

export interface DSHProvider {
  apiKeyEnv?: string
  api?: string
  baseURL?: string
  displayName?: string
  models?: DSHModel[]
}

export interface DSHDefaultModel {
  provider: string
  model: string
}

export interface DSHSettings {
  dshHome: string
  path: string
  exists: boolean
  writable: boolean
  providers: Record<string, DSHProvider>
  providerKeys: string[]
  defaultModel: DSHDefaultModel | null
  theme: string
}

export interface SaveDSHSettings {
  providers: Record<string, DSHProvider>
  providerKeys?: string[]
  defaultModel: DSHDefaultModel | null
}
