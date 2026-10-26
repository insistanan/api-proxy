// useChannelEditor 渠道编辑表单的草稿生命周期（R5，方案 8.1：
// AddChannelModal 的编辑、校验与 payload 组装收敛）。
// 职责边界：表单草稿持有独立状态；取消不修改 store；保存成功后由
// 调用方以服务端结果刷新。本文件不发起任何网络请求。
import { reactive, toValue, type MaybeRefOrGetter } from 'vue'
import type { Channel, ChannelStatus } from '@/services/api'
import { rowsFromImagesExtraParams, type ImagesExtraParamRow } from '@/utils/imagesExtraParams'

/** 表单草稿：AddChannelModal 的唯一可编辑状态本体。 */
export type ChannelFormState = {
  name: string
  serviceType: 'openai' | 'gemini' | 'claude' | 'responses' | 'chat' | ''
  status: ChannelStatus
  baseUrl: string
  baseUrls: string[]
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
  description: string
  apiKeys: string[]
  modelMapping: Record<string, string[]>
  /** 模型映射的目标渠道登记：源模型 -> 目标模型 -> 目标渠道 ID；未登记即沿用本渠道。 */
  modelMappingChannels: Record<string, Record<string, string>>
  defaultModel: string
  responsesModel: string
  imagesExtraParams: ImagesExtraParamRow[]
  imagesForceNonStream: boolean
}

/** 新渠道的默认草稿。responses 协议默认启用图片理解（历史行为）。 */
export function defaultChannelForm(channelType: string): ChannelFormState {
  return {
    name: '',
    serviceType: channelType === 'images' ? 'openai' : '',
    status: 'active',
    baseUrl: '',
    baseUrls: [],
    website: '',
    insecureSkipVerify: false,
    proxyMode: 'inherit',
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
    description: '',
    apiKeys: [],
    modelMapping: {},
    modelMappingChannels: {},
    defaultModel: '',
    responsesModel: '',
    imagesExtraParams: [],
    imagesForceNonStream: false
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

/** BaseURL 集合：去重（忽略末尾 / 与 # 差异），过滤空值。 */
export function deduplicateBaseUrls(input: { baseUrl: string; baseUrls: string[] }): string[] {
  const seen = new Set<string>()
  const raw = input.baseUrls.length > 0 ? input.baseUrls : [input.baseUrl]
  const result: string[] = []
  for (const url of raw) {
    const trimmed = url.trim().replace(/[#/]+$/, '')
    if (!trimmed) continue
    const normalized = trimmed.replace(/[#/]+$/, '')
    if (seen.has(normalized)) continue
    seen.add(normalized)
    result.push(trimmed)
  }
  return result
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
    form.name = channel.name
    form.serviceType = channel.serviceType
    // 前端 Channel.status 含运行时衍生值（''/error/healthy 等，来自 dashboard 合并）；
    // 表单只接受持久化状态，其余回退 active。
    form.status = isPersistableStatus(channel.status) ? channel.status : 'active'
    form.baseUrl = channel.baseUrl
    form.baseUrls = [...(channel.baseUrls || [])]
    form.website = channel.website || ''
    form.insecureSkipVerify = !!channel.insecureSkipVerify
    form.proxyMode = channel.proxyMode || 'inherit'
    form.proxyUrl = channel.proxyUrl || ''
    form.lowQuality = currentChannelType !== 'images' && !!channel.lowQuality
    form.visionCapable = currentChannelType !== 'images' && !!channel.visionCapable
    form.poolId = channel.poolId || 'default'
    form.excludeFromConversation = currentChannelType !== 'images' && !!channel.excludeFromConversation
    form.visionLayerEnabled = currentChannelType !== 'images' && !!channel.visionLayerEnabled
    form.visionLayerChannelId = currentChannelType === 'images' ? '' : channel.visionLayerChannelId || ''
    form.visionLayerModel = currentChannelType === 'images' ? '' : channel.visionLayerModel || ''
    form.temporary = !!channel.temporary
    form.injectDummyThoughtSignature = !!channel.injectDummyThoughtSignature
    form.stripThoughtSignature = !!channel.stripThoughtSignature
    form.disablePromptCacheKey = !!channel.disablePromptCacheKey
    form.description = channel.description || ''
    form.apiKeys = [...channel.apiKeys]
    form.modelMapping = cloneModelMapping(channel.modelMapping)
    form.modelMappingChannels = cloneModelMappingChannels(form.modelMapping, channel.modelMappingChannels)
    form.defaultModel = channel.defaultModel || ''
    form.responsesModel = channel.responsesModel || ''
    form.imagesExtraParams = rowsFromImagesExtraParams(channel.imagesExtraParams)
    form.imagesForceNonStream = currentChannelType === 'images' && !!channel.imagesForceNonStream
  }

  return { form, resetForm, loadChannelData }
}
