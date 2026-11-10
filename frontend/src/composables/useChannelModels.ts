// 渠道编辑器的模型探测与候选缓存；只操作传入的草稿，不提交渠道配置。
import { computed, ref, type Ref } from 'vue'
import { fetchUpstreamModels, channelApiByType, ApiError } from '@/services/api'
import { imagesModelOptions, imagesProtocolLabel } from '@/utils/imagesChannelProtocol'
import type { ChannelFormState } from './useChannelEditor'
import type { ChannelModalProps } from './useChannelModal'
import type { useQuickChannelDraft } from './useQuickChannelDraft'

export function useChannelModels(
  props: ChannelModalProps,
  form: ChannelFormState,
  isQuickMode: Ref<boolean>,
  quick: Pick<ReturnType<typeof useQuickChannelDraft>, 'detectedBaseUrl' | 'detectedBaseUrls' | 'detectedApiKeys'>
) {
  const { detectedBaseUrl, detectedBaseUrls, detectedApiKeys } = quick

  // 目标模型列表（从上游获取）
  const targetModelOptions = ref<Array<{ title: string; value: string }>>([])
  const fetchingModels = ref(false)
  const fetchModelsError = ref('')

  // 模型映射的目标渠道：候选渠道列表（同协议、可用、排除本渠道），以及各目标渠道的模型候选缓存。
  // 键为目标渠道 ID；本渠道的候选沿用上面的 targetModelOptions（键为空串）。
  const targetChannelOptions = ref<Array<{ title: string; value: string }>>([])
  const targetChannelModelOptions = ref<Record<string, Array<{ title: string; value: string }>>>({})
  const targetChannelModelStatus = ref<Record<string, { loading: boolean; error: string }>>({})
  const targetModelOptionsByChannel = computed(() => ({
    '': props.channelType === 'images' ? [...imagesModelOptions] : targetModelOptions.value,
    ...Object.fromEntries(Object.keys(targetChannelModelOptions.value).map(channelId => [
      channelId,
      props.channelType === 'images' ? [...imagesModelOptions] : targetChannelModelOptions.value[channelId]
    ]))
  }))
  const targetStatusByChannel = computed(() => ({
    '': { loading: fetchingModels.value, error: fetchModelsError.value },
    ...targetChannelModelStatus.value
  }))

  // API Key 的 models 状态管理
  interface KeyModelsStatus {
    loading: boolean
    success: boolean
    statusCode?: number
    error?: string
    modelCount?: number
  }
  const keyModelsStatus = ref<Map<string, KeyModelsStatus>>(new Map())

  const handleFetchModels = (channelId: string) => {
    if (channelId) {
      void fetchChannelTargetModels(channelId)
      return
    }
    fetchTargetModels()
  }

  // 加载模型映射的目标渠道候选（同协议、可用，排除本渠道）
  const loadMappingTargetChannels = async () => {
    try {
      const response = await channelApiByType(props.channelType).listMappingTargets(props.channel?.id)
      targetChannelOptions.value = (response.channels || []).map(channel => ({
        title: `${channel.name}（${props.channelType === 'images' ? imagesProtocolLabel(channel.serviceType) : channel.serviceType}）`,
        value: channel.id
      }))
    } catch (error) {
      console.error('加载模型映射目标渠道失败:', error)
      targetChannelOptions.value = []
    }
  }

  // 获取指定目标渠道的模型列表（使用目标渠道自身的密钥，服务端完成探测）。成功后按渠道缓存，失败不缓存以便重试。
  const fetchChannelTargetModels = async (channelId: string) => {
    if (targetChannelModelOptions.value[channelId] || targetChannelModelStatus.value[channelId]?.loading) {
      return
    }

    targetChannelModelStatus.value = {
      ...targetChannelModelStatus.value,
      [channelId]: { loading: true, error: '' }
    }

    try {
      const response = await channelApiByType(props.channelType).listMappingTargetModels(channelId)
      const options = response.data
        .map(model => model.id)
        .sort()
        .map(id => ({ title: id, value: id }))
      targetChannelModelOptions.value = { ...targetChannelModelOptions.value, [channelId]: options }
      targetChannelModelStatus.value = {
        ...targetChannelModelStatus.value,
        [channelId]: { loading: false, error: '' }
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : '未知错误'
      targetChannelModelStatus.value = {
        ...targetChannelModelStatus.value,
        [channelId]: { loading: false, error: `无法获取目标渠道的模型列表: ${message}` }
      }
    }
  }

  const fetchTargetModels = async () => {
  const modelBaseUrl = props.channelType === 'images' && isQuickMode.value
      ? detectedBaseUrl.value
      : form.baseUrl
  const modelBaseUrls = props.channelType === 'images' && isQuickMode.value
      ? detectedBaseUrls.value
      : form.baseUrls
  const modelApiKeys = props.channelType === 'images' && isQuickMode.value
      ? detectedApiKeys.value
      : form.apiKeys

    if (!modelBaseUrl) {
      fetchModelsError.value = '请先填写 Base URL'
      return
    }

    // 无 API Key 时仍尝试一次空密钥探测（Ollama 等本地上游）
  const keysToCheck = modelApiKeys.length > 0 ? modelApiKeys : ['']

    // 如果已经有模型列表且所有 key 都已检测过,直接返回(缓存)
    if (targetModelOptions.value.length > 0) {
      const allKeysChecked = keysToCheck.every(key => keyModelsStatus.value.has(key))
      if (allKeysChecked) {
        return
      }
    }

    fetchingModels.value = true
    fetchModelsError.value = ''

  const uncheckedKeys = keysToCheck.filter(key => !keyModelsStatus.value.has(key))

    if (uncheckedKeys.length === 0) {
      fetchingModels.value = false
      return
    }

  const keyPromises = uncheckedKeys.map(async (apiKey) => {
      keyModelsStatus.value.set(apiKey, { loading: true, success: false })

      try {
        const response = await fetchUpstreamModels(modelBaseUrl, apiKey, form.serviceType, {
          baseUrls: modelBaseUrls,
          insecureSkipVerify: form.insecureSkipVerify,
          proxyMode: form.proxyMode,
          proxyUrl: form.proxyMode === 'custom' ? form.proxyUrl.trim() : ''
        })

        keyModelsStatus.value.set(apiKey, {
          loading: false,
          success: true,
          statusCode: 200,
          modelCount: response.data.length
        })

        return response.data
      } catch (error) {
        let errorMsg = '未知错误'
        let statusCode = 0

        if (error instanceof ApiError) {
          errorMsg = error.message
          statusCode = error.status
        } else if (error instanceof Error) {
          errorMsg = error.message
        }

        keyModelsStatus.value.set(apiKey, {
          loading: false,
          success: false,
          statusCode,
          error: errorMsg
        })

        return []
      }
    })

    try {
      const results = await Promise.all(keyPromises)

      // 合并新获取的模型列表到现有列表
      const allModels = new Set<string>(targetModelOptions.value.map(opt => opt.value))
      results.forEach(models => {
        models.forEach(m => allModels.add(m.id))
      })

      targetModelOptions.value = Array.from(allModels)
        .sort()
        .map(id => ({ title: id, value: id }))

      // 如果所有 key 都失败了,显示错误
      const allFailed = keysToCheck.every(key => {
        const status = keyModelsStatus.value.get(key)
        return status && !status.success
      })

      if (allFailed) {
        const firstError = keysToCheck
          .map(key => keyModelsStatus.value.get(key)?.error)
          .find(Boolean)
        fetchModelsError.value = firstError
          ? `无法获取模型列表: ${firstError}`
          : '所有 API Key 都无法获取模型列表,请检查 API 密钥列表中的错误信息'
      }
    } finally {
      fetchingModels.value = false
    }
  }

  return {
    targetModelOptions,
    fetchingModels,
    fetchModelsError,
    targetChannelOptions,
    targetChannelModelOptions,
    targetChannelModelStatus,
    targetModelOptionsByChannel,
    targetStatusByChannel,
    keyModelsStatus,
    handleFetchModels,
    loadMappingTargetChannels,
    fetchChannelTargetModels,
    fetchTargetModels
  }
}
