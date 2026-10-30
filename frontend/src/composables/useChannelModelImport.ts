import type { ApiTab, ModelEntry } from '@/services/api'
import { PROTOCOL_DEFAULTS } from './channelDefaults'

/**
 * "一键导入本代理模型"共享逻辑。
 *
 * 模型来源：本代理自身暴露的模型列表（GET /api/proxy-models，等同 /v1/models）。
 * 每个条目的 owned_by 形如 "pool:messages" / "pool:messages,responses"，标识该模型属于哪些协议分组；
 * 与静态家族别名同名的分组也保留上述协议归属；只有未配置同名分组的别名才标记为 "api-proxy"。
 *
 * 导入策略：选定协议后，只导入 owned_by 含该协议的模型（按协议分组过滤），
 * 然后替换 provider.models（导入即替换）。
 */

/** 本代理模型条目（/api/proxy-models 返回） */
export interface ProxyModelEntry {
  id: string
  ownedBy: string
}

/**
 * 从 /api/proxy-models 的返回中按协议分组过滤出模型 ID 列表。
 *
 * 过滤规则：
 * - owned_by 以 "pool:" 开头且协议列表含指定 kind → 归属该协议，导入
 * - owned_by 为 "api-proxy"（未配置同名分组的静态别名）→ 不属于具体协议分组，跳过
 */
export function filterProxyModelsByKind(entries: ModelEntry[], kind: ApiTab): string[] {
  const prefix = 'pool:'
  const result: string[] = []
  const seen = new Set<string>()
  for (const entry of entries) {
    const ownedBy = (entry.owned_by ?? '').trim()
    if (!ownedBy.startsWith(prefix)) continue
    const kinds = ownedBy.slice(prefix.length).split(',').map(k => k.trim())
    if (!kinds.includes(kind)) continue
    const id = entry.id.trim()
    if (!id || seen.has(id)) continue
    seen.add(id)
    result.push(id)
  }
  return result
}

/**
 * 通用导入入口：把本代理某协议分组下的模型 ID 清单转成客户端模型对象，
 * 然后替换 provider.models（导入即替换）。
 *
 * @param kind 协议分组（messages/responses/gemini/chat）
 * @param modelIds 本代理该协议分组下的模型 ID 列表
 * @param buildModel 单个模型 ID -> 客户端模型对象的转换函数
 * @param setModels 把新模型数组写回 provider 的函数
 * @returns 导入的模型数量
 */
export function importProxyModels<TModel>(
  kind: ApiTab,
  modelIds: string[],
  buildModel: (modelId: string, defaults: typeof PROTOCOL_DEFAULTS[ApiTab]) => TModel,
  setModels: (models: TModel[]) => void
): number {
  const defaults = PROTOCOL_DEFAULTS[kind]
  const models = modelIds.map(id => buildModel(id, defaults))
  setModels(models)
  return models.length
}

// ============ DSH ============

/** DSH 模型结构（与 DSHModel 对齐，导入所需字段） */
export interface DSHImportModel {
  id: string
  name?: string
  input: string[]
  reasoningEfforts: Record<string, string | null>
  contextWindow?: number | null
  maxTokens?: number | null
}

/** DSH 推理等级默认勾选：off/medium/high/max 全开，off 映射 null */
function dshDefaultReasoningEfforts(): Record<string, string | null> {
  return { off: null, medium: 'medium', high: 'high', max: 'max' }
}

/** 构建 DSH 模型（识图默认开，本项目有图片理解层兜底） */
export function buildDSHModel(
  modelId: string,
  defaults: typeof PROTOCOL_DEFAULTS[ApiTab]
): DSHImportModel {
  return {
    id: modelId,
    name: modelId,
    input: ['text', 'image'],
    reasoningEfforts: dshDefaultReasoningEfforts(),
    contextWindow: defaults.contextLimit,
    maxTokens: defaults.outputLimit
  }
}

// ============ OpenCode ============

/** OpenCode 模型结构（与 EditableModel 对齐，导入所需字段） */
export interface OpenCodeImportModel {
  key: string
  apiModelId: string
  name: string
  contextLimit: number
  inputLimit: number
  outputLimit: number
  supportsImage: boolean
  variants: Record<string, { reasoningEffort: string }>
}

/** OpenCode 默认 variants：none/low/medium/high/xhigh/max，全部启用 */
function openCodeDefaultVariants(): Record<string, { reasoningEffort: string }> {
  return {
    none: { reasoningEffort: 'none' },
    low: { reasoningEffort: 'low' },
    medium: { reasoningEffort: 'medium' },
    high: { reasoningEffort: 'high' },
    xhigh: { reasoningEffort: 'xhigh' },
    max: { reasoningEffort: 'max' }
  }
}

/** 构建 OpenCode 模型 */
export function buildOpenCodeModel(
  modelId: string,
  defaults: typeof PROTOCOL_DEFAULTS[ApiTab]
): OpenCodeImportModel {
  return {
    key: modelId,
    apiModelId: '',
    name: modelId,
    contextLimit: defaults.contextLimit,
    inputLimit: defaults.inputLimit,
    outputLimit: defaults.outputLimit,
    supportsImage: true,
    variants: openCodeDefaultVariants()
  }
}

// ============ PiAgent ============

/** PiAgent 模型结构（与 EditableModel 对齐，导入所需字段） */
export interface PiAgentImportModel {
  id: string
  name: string
  reasoning: boolean
  input: string[]
  contextWindow: number | null
  maxTokens: number | null
}

/** 构建 PiAgent 模型（思考默认开、识图默认开） */
export function buildPiAgentModel(
  modelId: string,
  defaults: typeof PROTOCOL_DEFAULTS[ApiTab]
): PiAgentImportModel {
  return {
    id: modelId,
    name: modelId,
    reasoning: true,
    input: ['text', 'image'],
    contextWindow: defaults.contextLimit,
    maxTokens: defaults.outputLimit
  }
}

// ============ ClaudeCode ============

/**
 * ClaudeCode modelDefaults 导入。
 *
 * 客户端固定连接本代理的 messages 协议，导入该协议分组下的模型 ID，
 * 按 family（fable/opus/sonnet/haiku）关键词归类；不含关键词的模型跳过。
 *
 * 注意：1M 后缀 [1m] 不在此处拼接，由后端保存时根据各 family 的 supports1M 独立追加。
 *
 * @param modelIds messages 协议分组下的模型 ID 列表
 * @param supports1M 导入时是否默认勾选 1M 上下文（每个 family 共用此初值，用户可在 UI 单独调整）
 */
export interface ClaudeCodeImportDefault {
  family: string
  model: string
  name: string
  supports1M: boolean
}

const CLAUDE_CODE_FAMILY_KEYWORDS: Array<{ family: string; keywords: string[] }> = [
  { family: 'fable', keywords: ['fable'] },
  { family: 'opus', keywords: ['opus'] },
  { family: 'sonnet', keywords: ['sonnet'] },
  { family: 'haiku', keywords: ['haiku'] }
]

/** 从模型 ID 清单构建 ClaudeCode modelDefaults（按 family 归类，supports1M 为导入初值） */
export function buildClaudeCodeModelDefaults(
  modelIds: string[],
  supports1M: boolean
): ClaudeCodeImportDefault[] {
  const result: ClaudeCodeImportDefault[] = []
  const usedFamilies = new Set<string>()

  for (const modelId of modelIds) {
    const modelLower = modelId.toLowerCase()
    const matched = CLAUDE_CODE_FAMILY_KEYWORDS.find(f => f.keywords.some(k => modelLower.includes(k)))
    if (!matched || usedFamilies.has(matched.family)) continue
    usedFamilies.add(matched.family)
    result.push({ family: matched.family, model: modelId, name: modelId, supports1M })
  }

  return result
}
