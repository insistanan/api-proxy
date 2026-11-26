// useChannelEditor 渠道编辑表单的草稿生命周期（R5，方案 8.1：
// AddChannelModal 的编辑、校验与 payload 组装收敛）。
// 职责边界：表单草稿持有独立状态；取消不修改 store；保存成功后由
// 调用方以服务端结果刷新。本文件不发起任何网络请求。
import { reactive, toValue, type MaybeRefOrGetter } from 'vue'
import { DEFAULT_BRANCH_CONCURRENCY, isPositiveInteger } from '@/features/channels/utils/branchConcurrency'
import type { Channel, ChannelStatus } from '@/api'
import { rowsFromImagesExtraParams, type ImagesExtraParamRow } from '@/features/channels/utils/imagesExtraParams'
import type { ApiTab, CompactionConfig, ImagesOutputFormat } from '@/types/channel'
import { isConversationProtocol } from '@/features/channels/services/channel-api'
import { imagesGIFDefaults } from '@/features/channels/utils/imagesChannelProtocol'
import { buildCacheKeepalive, defaultCacheKeepalive, supportsCacheKeepalive, type CacheKeepaliveDraft } from '@/features/channels/utils/cacheKeepalive'

/** 表单草稿：AddChannelModal 的唯一可编辑状态本体。 */
export type ChannelFormState = {
  compaction: CompactionConfig
  cacheKeepalive: CacheKeepaliveDraft
  maxConcurrency: number | string | null
  maxBranchConcurrency: number | string | null
  clientIdentityMode: 'inherit' | 'api' | 'compatibility'
  name: string
  serviceType: 'openai' | 'gemini' | 'claude' | 'responses' | 'chat' | ''
  status: ChannelStatus
  baseUrl: string
  baseUrls: string[]
  useFullUrl: boolean
  website: string
  insecureSkipVerify: boolean
  proxyMode: 'inherit' | 'direct' | 'custom'
  proxyUrl: string
  lowQuality: boolean
  visionCapable: boolean
  poolId: string
  excludeFromConversation: boolean
  visionLayerEnabled: boolean
  visionLayerChannelId: string
  visionLayerModel: string
  temporary: boolean
  injectDummyThoughtSignature: boolean
  stripThoughtSignature: boolean
  disablePromptCacheKey: boolean
  reasoningOutputMode: 'preserve' | 'hide'
  reasoningEffort: NonNullable<Channel['reasoningEffort']>
  parseEmbeddedReasoning: boolean
  description: string
  apiKeys: string[]
  modelMapping: Record<string, string[]>
  /** 模型映射的目标渠道登记：源模型 -> 目标模型 -> 目标渠道 ID；未登记即沿用本渠道。 */
  modelMappingChannels: Record<string, Record<string, string>>
  defaultModel: string
  responsesModel: string
  imagesExtraParams: ImagesExtraParamRow[]
  imagesForceNonStream: boolean
  imagesOutputFormat: ImagesOutputFormat
  imagesFrameCount: number | string | null
  imagesFrameDurationMs: number | string | null
  /** Embeddings 输出维度；0 表示不覆盖请求中的 dimensions。 */
  embeddingsDimensions: number | string | null
}

/** 新建 Embeddings 渠道的默认输出维度，与常用的 1024 维向量模型一致。 */
export const EMBEDDINGS_DIMENSIONS_DEFAULT = 1024

/** 独立出站代理的默认地址：切换到“使用独立代理”时直接填入，而不是只显示占位提示。 */
export const DEFAULT_CUSTOM_PROXY_URL = 'http://localhost:7897'

export const defaultCompaction = (): CompactionConfig => ({
  summaryPool: false, summaryCapable: false, summaryModel: '', detectClientPrompts: false,
  nativeSourceModel: '', nativeModel: '', nativeCompatibilityConfirmed: false
})

/**
 * Embeddings 输出维度校验：必须是非负整数，0 表示不覆盖。
 * 返回空字符串表示合法，否则返回错误说明。
 */
export function validateEmbeddingsDimensions(value: unknown): string {
  if (value === null || value === '') return '请输入输出维度（0 表示不覆盖）'
  if (typeof value !== 'number' && typeof value !== 'string') return '输出维度必须是整数'
  const number = Number(value)
  if (!Number.isInteger(number) || number < 0) return '输出维度必须是非负整数（0 表示不覆盖）'
  return ''
}

/**
 * 客户端伪装是否对该渠道真正生效：由入口协议与上游 serviceType 共同决定。
 * Messages / Responses 两种入口均按实际 Messages / Responses 上游选择身份。
 */
export function supportsChannelClientIdentity(channelType: string, serviceType: string): boolean {
  return ['messages', 'responses'].includes(channelType) && ['claude', 'responses'].includes(serviceType)
}

/** 新渠道的默认草稿。responses 协议默认启用图片理解（历史行为）。 */
export function defaultChannelForm(channelType: string): ChannelFormState {
  return {
    compaction: defaultCompaction(),
    cacheKeepalive: defaultCacheKeepalive(),
    maxConcurrency: 5,
    maxBranchConcurrency: DEFAULT_BRANCH_CONCURRENCY,
    clientIdentityMode: 'inherit',
    name: '',
    serviceType: channelType === 'images' || channelType === 'embeddings' || channelType === 'decisions' ? 'openai' : '',
    status: 'active',
    baseUrl: '',
    baseUrls: [],
    useFullUrl: false,
    website: '',
    insecureSkipVerify: false,
    proxyMode: 'direct',
    proxyUrl: '',
    lowQuality: false,
    visionCapable: channelType === 'responses',
    poolId: 'default',
    excludeFromConversation: false,
    visionLayerEnabled: false,
    visionLayerChannelId: '',
    visionLayerModel: '',
    temporary: false,
    injectDummyThoughtSignature: false,
    stripThoughtSignature: false,
    disablePromptCacheKey: false,
    reasoningOutputMode: 'preserve',
    reasoningEffort: 'inherit',
    parseEmbeddedReasoning: false,
    description: '',
    apiKeys: [],
    modelMapping: {},
    modelMappingChannels: {},
    defaultModel: '',
    responsesModel: '',
    imagesExtraParams: [],
    imagesForceNonStream: false,
    imagesOutputFormat: '',
    imagesFrameCount: imagesGIFDefaults.frameCount,
    imagesFrameDurationMs: imagesGIFDefaults.frameDurationMs,
    embeddingsDimensions: EMBEDDINGS_DIMENSIONS_DEFAULT
  }
}

/** 模型映射深拷贝：去空白、去重、去空目标。 */
export function cloneModelMapping(mapping?: Record<string, string[]>): Record<string, string[]> {
  const cloned: Record<string, string[]> = {}
  for (const [rawSource, rawTargets] of Object.entries(mapping || {})) {
    const source = rawSource.trim()
    if (!source) continue
    const targets = [...new Set(rawTargets.map(target => target.trim()).filter(Boolean))]
    if (targets.length > 0) {
      cloned[source] = targets
    }
  }
  return cloned
}

/**
 * 目标渠道登记深拷贝，并按已清理的模型映射剪枝：
 * 只保留 mapping 中仍存在的 源→目标 对，且渠道 ID 非空。
 * 调用方必须先对 mapping 调用 cloneModelMapping，保证两者一致。
 */
export function cloneModelMappingChannels(
  mapping: Record<string, string[]>,
  channels?: Record<string, Record<string, string>>
): Record<string, Record<string, string>> {
  const cloned: Record<string, Record<string, string>> = {}
  for (const [source, targets] of Object.entries(mapping)) {
    const sourceChannels = channels?.[source]
    if (!sourceChannels) continue
    const kept: Record<string, string> = {}
    for (const target of targets) {
      const channelId = sourceChannels[target]?.trim()
      if (channelId) {
        kept[target] = channelId
      }
    }
    if (Object.keys(kept).length > 0) {
      cloned[source] = kept
    }
  }
  return cloned
}

/** BaseURL 集合：自动补全模式规整末尾；完整地址模式保留路径和查询参数。 */
export function deduplicateBaseUrls(input: { baseUrl: string; baseUrls: string[]; useFullUrl?: boolean }): string[] {
  const seen = new Set<string>()
  const raw = input.baseUrls.length > 0 ? input.baseUrls : [input.baseUrl]
  const result: string[] = []
  for (const url of raw) {
    const trimmed = input.useFullUrl ? url.trim() : url.trim().replace(/[#/]+$/, '')
    if (!trimmed) continue
    const normalized = trimmed
    if (seen.has(normalized)) continue
    seen.add(normalized)
    result.push(trimmed)
  }
  return result
}

// 保留 combobox 的字符串和选项对象两种取值形式，清空选项时写回空字符串。
function modelValueString(value: unknown): string {
  if (!value) return ''
  if (typeof value === 'string') return value
  if (typeof value === 'object' && 'value' in value) {
    return typeof value.value === 'string' ? value.value : ''
  }
  return ''
}

/** 组装保存请求：表单显示与网络编排不再各自维护一套字段转换。 */
export function buildImagesOutputSettings(form: ChannelFormState) {
  const isGIF = form.imagesOutputFormat === 'gif'
  return {
    imagesOutputFormat: form.imagesOutputFormat,
    imagesFrameCount: isGIF && form.imagesFrameCount !== null && form.imagesFrameCount !== '' ? Number(form.imagesFrameCount) : 0,
    imagesFrameDurationMs: isGIF && form.imagesFrameDurationMs !== null && form.imagesFrameDurationMs !== '' ? Number(form.imagesFrameDurationMs) : 0
  }
}

export function buildChannelPayload(
  form: ChannelFormState,
  channelType: string,
  isEditing: boolean,
  imagesExtraParams?: Record<string, string>
): Omit<Channel, 'id' | 'index' | 'latency'> {
  if (!isPositiveInteger(form.maxBranchConcurrency)) throw new Error('渠道分支并发上限必须是大于零的整数')
  const isImages = channelType === 'images'
  const isChat = channelType === 'chat'
  // 只有事实上会注入伪装身份的组合才保留该配置，其余协议固定继承全局设置。
  const supportsClientIdentity = supportsChannelClientIdentity(channelType, form.serviceType)
  // Embeddings / Decisions 是纯透传协议，不参与图片理解与对话调度。
  const supportsConversationFeatures = !isImages && channelType !== 'embeddings' && channelType !== 'decisions'
  const supportsNativeCompaction = !form.compaction.summaryCapable &&
    ((channelType === 'messages' && form.serviceType === 'claude') || (channelType === 'responses' && form.serviceType === 'responses'))
  if (supportsConversationFeatures && form.compaction.summaryCapable && (!form.excludeFromConversation || !form.compaction.summaryModel.trim())) {
    throw new Error('加入摘要池需关闭常规对话调度并填写摘要模型')
  }
  if (supportsNativeCompaction && form.compaction.nativeModel.trim() && (!form.compaction.nativeSourceModel.trim() || !form.compaction.nativeCompatibilityConfirmed)) {
    throw new Error('请填写原生压缩的精确源模型，并确认此模型对的完整往返兼容性')
  }
  const urls = deduplicateBaseUrls(form)
  const mapping = isChat ? {} : cloneModelMapping(form.modelMapping)
  const channel: Omit<Channel, 'id' | 'index' | 'latency'> = {
    compaction: supportsConversationFeatures ? {
      ...form.compaction,
      summaryModel: form.compaction.summaryCapable ? form.compaction.summaryModel.trim() : '',
      nativeModel: supportsNativeCompaction ? form.compaction.nativeModel.trim() : '',
      nativeSourceModel: supportsNativeCompaction && form.compaction.nativeModel.trim() ? form.compaction.nativeSourceModel.trim() : '',
      nativeCompatibilityConfirmed: supportsNativeCompaction && !!form.compaction.nativeModel.trim() && form.compaction.nativeCompatibilityConfirmed
    } : defaultCompaction(),
    cacheKeepalive: buildCacheKeepalive(form.cacheKeepalive, supportsCacheKeepalive(channelType, form.serviceType)),
    maxConcurrency: Number(form.maxConcurrency),
    maxBranchConcurrency: Number(form.maxBranchConcurrency),
    clientIdentityMode: supportsClientIdentity ? form.clientIdentityMode : 'inherit',
    name: form.name.trim(),
    serviceType: form.serviceType as Channel['serviceType'],
    responsesModel: isImages && form.serviceType === 'responses' ? form.responsesModel.trim() : '',
    baseUrl: urls[0] || '',
    useFullUrl: form.useFullUrl,
    website: form.website.trim(), // 空字符串用于清除已有值。
    insecureSkipVerify: form.insecureSkipVerify,
    proxyMode: form.proxyMode,
    proxyUrl: form.proxyMode === 'custom' ? form.proxyUrl.trim() : '',
    lowQuality: isImages ? false : form.lowQuality,
    visionCapable: supportsConversationFeatures ? form.visionCapable : false,
    poolId: form.poolId,
    excludeFromConversation: supportsConversationFeatures ? form.excludeFromConversation : false,
    visionLayerEnabled: supportsConversationFeatures ? form.visionLayerEnabled : false,
    visionLayerChannelId: supportsConversationFeatures ? form.visionLayerChannelId || undefined : '',
    visionLayerModel: supportsConversationFeatures ? form.visionLayerModel.trim() : '',
    temporary: form.temporary,
    injectDummyThoughtSignature: isImages ? false : form.injectDummyThoughtSignature,
    stripThoughtSignature: isImages ? false : form.stripThoughtSignature,
    disablePromptCacheKey: isImages ? false : form.disablePromptCacheKey,
    reasoningOutputMode: form.reasoningOutputMode,
    reasoningEffort: supportsConversationFeatures ? form.reasoningEffort : 'inherit',
    parseEmbeddedReasoning: form.parseEmbeddedReasoning,
    description: form.description.trim(),
    apiKeys: form.apiKeys.filter(key => key.trim()),
    modelMapping: mapping,
    modelMappingChannels: isChat ? {} : cloneModelMappingChannels(mapping, form.modelMappingChannels),
    defaultModel: isChat ? modelValueString(form.defaultModel).trim() : '',
    ...(isImages ? { imagesForceNonStream: form.imagesForceNonStream } : {}),
    ...(isImages ? buildImagesOutputSettings(form) : {}),
    // 只有 Embeddings 渠道使用该字段；其他协议固定为 0，即不覆盖。
    embeddingsDimensions: channelType === 'embeddings' ? Number(form.embeddingsDimensions) : 0,
    ...(imagesExtraParams ? { imagesExtraParams } : {})
  }
  if (urls.length > 1) channel.baseUrls = urls
  if (isEditing) channel.status = form.status
  return channel
}

/** 持久化渠道状态集合（表单可写回的值）。 */
const PERSISTABLE_STATUSES: readonly ChannelStatus[] = ['active', 'suspended', 'disabled', 'deprecated', 'deleted']

function isPersistableStatus(value: string | undefined): value is ChannelStatus {
  return !!value && (PERSISTABLE_STATUSES as readonly string[]).includes(value)
}

/** 渠道编辑器：草域草稿 + 载入编辑目标。 */
export function useChannelEditor(channelType: MaybeRefOrGetter<string>) {
  const form = reactive<ChannelFormState>(defaultChannelForm(toValue(channelType)))

  /** 重置为默认草稿（取消/新建）。 */
  const resetForm = (): void => {
    Object.assign(form, defaultChannelForm(toValue(channelType)))
  }

  /** 载入编辑目标（草稿复制自服务端数据，未保存前不影响 store）。 */
  const loadChannelData = (channel: Channel): void => {
    const currentChannelType = toValue(channelType)
    const supportsConversationFeatures =
      currentChannelType !== 'images' && currentChannelType !== 'embeddings' && currentChannelType !== 'decisions'
    form.compaction = supportsConversationFeatures ? { ...defaultCompaction(), ...channel.compaction } : defaultCompaction()
    form.cacheKeepalive = channel.cacheKeepalive?.intervalSeconds
      ? { ...channel.cacheKeepalive } : defaultCacheKeepalive()
    form.maxConcurrency = channel.maxConcurrency ?? 5
    form.maxBranchConcurrency = channel.maxBranchConcurrency ?? DEFAULT_BRANCH_CONCURRENCY
    form.name = channel.name
    form.serviceType = channel.serviceType
    // 只对真正会注入伪装的入口 + 上游组合保留配置，避免误显示与回写。
    form.clientIdentityMode = supportsChannelClientIdentity(currentChannelType, channel.serviceType)
      ? channel.clientIdentityMode || 'inherit'
      : 'inherit'
    // 前端 Channel.status 含运行时衍生值（''/error/healthy 等，来自 dashboard 合并）；
    // 表单只接受持久化状态，其余回退 active。
    form.status = isPersistableStatus(channel.status) ? channel.status : 'active'
    form.baseUrl = channel.baseUrl
    form.baseUrls = [...(channel.baseUrls || [])]
    form.useFullUrl = !!channel.useFullUrl
    form.website = channel.website || ''
    form.insecureSkipVerify = !!channel.insecureSkipVerify
    form.proxyMode = channel.proxyMode || 'inherit'
    form.proxyUrl = channel.proxyUrl || ''
    form.lowQuality = currentChannelType !== 'images' && !!channel.lowQuality
    form.visionCapable = supportsConversationFeatures && !!channel.visionCapable
    form.poolId = channel.poolId || 'default'
    form.excludeFromConversation = supportsConversationFeatures && !!channel.excludeFromConversation
    form.visionLayerEnabled = supportsConversationFeatures && !!channel.visionLayerEnabled
    form.visionLayerChannelId = supportsConversationFeatures ? channel.visionLayerChannelId || '' : ''
    form.visionLayerModel = supportsConversationFeatures ? channel.visionLayerModel || '' : ''
    form.temporary = !!channel.temporary
    form.injectDummyThoughtSignature = !!channel.injectDummyThoughtSignature
    form.stripThoughtSignature = !!channel.stripThoughtSignature
    form.disablePromptCacheKey = !!channel.disablePromptCacheKey
    form.reasoningOutputMode = channel.reasoningOutputMode || 'preserve'
    form.reasoningEffort = supportsConversationFeatures ? channel.reasoningEffort || 'inherit' : 'inherit'
    form.parseEmbeddedReasoning = !!channel.parseEmbeddedReasoning
    form.description = channel.description || ''
    form.apiKeys = [...channel.apiKeys]
    form.modelMapping = cloneModelMapping(channel.modelMapping)
    form.modelMappingChannels = cloneModelMappingChannels(form.modelMapping, channel.modelMappingChannels)
    form.defaultModel = channel.defaultModel || ''
    form.responsesModel = channel.responsesModel || ''
    form.imagesExtraParams = rowsFromImagesExtraParams(channel.imagesExtraParams)
    form.imagesForceNonStream = currentChannelType === 'images' && !!channel.imagesForceNonStream
    form.imagesOutputFormat = currentChannelType === 'images' ? channel.imagesOutputFormat || '' : ''
    form.imagesFrameCount = channel.imagesFrameCount === undefined || channel.imagesFrameCount === 0
      ? imagesGIFDefaults.frameCount : channel.imagesFrameCount
    form.imagesFrameDurationMs = channel.imagesFrameDurationMs === undefined || channel.imagesFrameDurationMs === 0
      ? imagesGIFDefaults.frameDurationMs : channel.imagesFrameDurationMs
    // 旧渠道没有该字段时按 0 回填（不覆盖），避免编辑时悄悄改成默认维度。
    form.embeddingsDimensions = channel.embeddingsDimensions ?? 0
  }

  const buildPayload = (isEditing: boolean, imagesExtraParams?: Record<string, string>) =>
    buildChannelPayload(form, toValue(channelType), isEditing, imagesExtraParams)

  /** 同协议复制配置；跨协议沿用连接信息，并按目标入口初始化专属配置。 */
  const loadChannelCopy = (channel: Channel, sourceType: ApiTab): void => {
    resetForm()
    loadChannelData(channel)
    form.name = `${channel.name} (副本)`
    form.status = 'active'
    form.poolId = 'default'
    if (sourceType === toValue(channelType)) return

    const defaults = defaultChannelForm(toValue(channelType))
    // 跨协议不能把原先转给其他渠道的模型悄悄改成由当前供应商处理。
    form.modelMapping = Object.fromEntries(Object.entries(form.modelMapping)
      .map(([source, targets]) => [source, targets.filter(target => !channel.modelMappingChannels?.[source]?.[target])] as const)
      .filter(([, targets]) => targets.length > 0))
    Object.assign(form, {
      compaction: defaults.compaction,
      cacheKeepalive: defaults.cacheKeepalive,
      visionCapable: defaults.visionCapable,
      excludeFromConversation: defaults.excludeFromConversation,
      visionLayerEnabled: defaults.visionLayerEnabled,
      visionLayerChannelId: defaults.visionLayerChannelId,
      visionLayerModel: defaults.visionLayerModel,
      injectDummyThoughtSignature: defaults.injectDummyThoughtSignature,
      stripThoughtSignature: defaults.stripThoughtSignature,
      disablePromptCacheKey: defaults.disablePromptCacheKey,
      reasoningOutputMode: defaults.reasoningOutputMode,
      reasoningEffort: defaults.reasoningEffort,
      parseEmbeddedReasoning: defaults.parseEmbeddedReasoning,
      defaultModel: defaults.defaultModel,
      responsesModel: defaults.responsesModel,
      imagesExtraParams: defaults.imagesExtraParams,
      imagesForceNonStream: defaults.imagesForceNonStream,
      imagesOutputFormat: defaults.imagesOutputFormat,
      imagesFrameCount: defaults.imagesFrameCount,
      imagesFrameDurationMs: defaults.imagesFrameDurationMs,
      embeddingsDimensions: defaults.embeddingsDimensions,
      modelMappingChannels: {}
    })
    // 对话入口之间保留模型名；其他入口使用不同类型的模型，必须重新配置。
    if (!isConversationProtocol(sourceType) || !isConversationProtocol(toValue(channelType))) {
      form.modelMapping = {}
    } else if (sourceType === 'chat' && channel.defaultModel?.trim()) {
      form.modelMapping = { '*': [channel.defaultModel.trim()] }
    }
    if (toValue(channelType) === 'chat') {
      const targets = form.modelMapping['*']
      // 只有唯一的本渠道通配映射才能无歧义地转换为 Chat 默认模型。
      if (targets?.length === 1) {
        form.defaultModel = targets[0]
      }
      form.modelMapping = {}
    }
  }

  return { form, resetForm, loadChannelData, loadChannelCopy, buildPayload }
}
