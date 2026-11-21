// 渠道编辑器的模型探测与候选缓存；只操作传入的草稿，不提交渠道配置。
import { computed, ref, watch, type Ref } from 'vue'
import { fetchUpstreamModels, channelApiByType, ApiError } from '@/api'
import { imagesModelOptions, imagesProtocolLabel } from '@/features/channels/utils/imagesChannelProtocol'
import type { ChannelFormState } from '@/features/channels/composables/useChannelEditor'
import type { ChannelModalProps } from '@/features/channels/composables/useChannelModal'
import type { useQuickChannelDraft } from '@/features/channels/composables/useQuickChannelDraft'

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
  const mappingTargetChannelsError = ref('')
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
  let requestRevision = 0
  let modelRequestRevision = 0
  const modelConnection = computed(() => {
    const useDetected = props.channelType === 'images' && isQuickMode.value
    return {
      baseUrl: useDetected ? detectedBaseUrl.value : form.baseUrl,
      baseUrls: [...(useDetected ? detectedBaseUrls.value : form.baseUrls)],
      apiKeys: [...(useDetected ? detectedApiKeys.value : form.apiKeys)],
      serviceType: form.serviceType,
      insecureSkipVerify: form.insecureSkipVerify,
      proxyMode: form.proxyMode,
      proxyUrl: form.proxyMode === 'custom' ? form.proxyUrl.trim() : ''
    }
  })
  const resetLocalModels = () => {
    modelRequestRevision++
    targetModelOptions.value = []
    fetchingModels.value = false
    fetchModelsError.value = ''
    keyModelsStatus.value.clear()
  }
  // 连接信息变更后不能复用旧上游的候选，也不能让迟到响应写回新草稿。
  watch(modelConnection, resetLocalModels, { flush: 'sync' })
  watch([() => props.show, () => props.channelType], () => {
    requestRevision++
    targetChannelOptions.value = []
    mappingTargetChannelsError.value = ''
    resetLocalModels()
    targetChannelModelOptions.value = {}
    targetChannelModelStatus.value = {}
  }, { flush: 'sync' })

  const handleFetchModels = (channelId: string) => {
    if (channelId) {
      void fetchChannelTargetModels(channelId)
      return
    }
    void fetchTargetModels()
  }

  // 加载模型映射的目标渠道候选（同协议、可用，排除本渠道）
  const loadMappingTargetChannels = async () => {
    const revision = requestRevision
    const kind = props.channelType
    mappingTargetChannelsError.value = ''
    try {
      const response = await channelApiByType(kind).listMappingTargets(props.channel?.id)
      if (revision !== requestRevision) return
      if (!Array.isArray(response.channels)) throw new Error('模型映射目标渠道响应格式无效')
      targetChannelOptions.value = response.channels.map(channel => ({
        title: `${channel.name}（${kind === 'images' ? imagesProtocolLabel(channel.serviceType) : channel.serviceType}）`,
        value: channel.id
      }))
    } catch (error) {
      if (revision !== requestRevision) return
      mappingTargetChannelsError.value = `加载模型映射目标渠道失败：${error instanceof Error ? error.message : String(error)}`
      targetChannelOptions.value = []
    }
  }

  // 获取指定目标渠道的模型列表（使用目标渠道自身的密钥，服务端完成探测）。成功后按渠道缓存，失败不缓存以便重试。
  const fetchChannelTargetModels = async (channelId: string) => {
    const revision = requestRevision
    if (targetChannelModelOptions.value[channelId] || targetChannelModelStatus.value[channelId]?.loading) {
      return
    }

    targetChannelModelStatus.value = {
      ...targetChannelModelStatus.value,
      [channelId]: { loading: true, error: '' }
    }

    try {
      const response = await channelApiByType(props.channelType).listMappingTargetModels(channelId)
      if (revision !== requestRevision) return
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
      if (revision !== requestRevision) return
      const message = error instanceof Error ? error.message : '未知错误'
      targetChannelModelStatus.value = {
        ...targetChannelModelStatus.value,
        [channelId]: { loading: false, error: `无法获取目标渠道的模型列表: ${message}` }
      }
    }
  }

  const fetchTargetModels = async () => {
    if (fetchingModels.value) return
    const revision = modelRequestRevision
    const connection = modelConnection.value
    if (!connection.baseUrl) {
      fetchModelsError.value = '请先填写 Base URL'
      return
    }

    // 无 API Key 时仍尝试一次空密钥探测（Ollama 等本地上游）
    const keysToCheck = connection.apiKeys.length > 0 ? [...new Set(connection.apiKeys)] : ['']
    // 只缓存成功结果，重新打开下拉时允许失败的密钥重试。
    const uncheckedKeys = keysToCheck.filter(key => !keyModelsStatus.value.get(key)?.success)
    if (uncheckedKeys.length === 0) return

    fetchingModels.value = true
    fetchModelsError.value = ''

    const fetchKeyModels = async (apiKey: string) => {
      keyModelsStatus.value.set(apiKey, { loading: true, success: false })

      try {
        const response = await fetchUpstreamModels(connection.baseUrl, apiKey, connection.serviceType, {
          baseUrls: connection.baseUrls,
          insecureSkipVerify: connection.insecureSkipVerify,
          proxyMode: connection.proxyMode,
          proxyUrl: connection.proxyUrl
        })
        if (revision !== modelRequestRevision) return []

        keyModelsStatus.value.set(apiKey, {
          loading: false,
          success: true,
          statusCode: 200,
          modelCount: response.data.length
        })

        return response.data
      } catch (error) {
        if (revision !== modelRequestRevision) return []
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
    }

    try {
      // 限制多密钥探测并发，避免一次打开下拉就占满浏览器与上游连接。
      const results: Array<Array<{ id: string }>> = []
      let nextKey = 0
      await Promise.all(Array.from({ length: Math.min(4, uncheckedKeys.length) }, async () => {
        while (revision === modelRequestRevision) {
          const apiKey = uncheckedKeys[nextKey++]
          if (apiKey === undefined) return
          results.push(await fetchKeyModels(apiKey))
        }
      }))
      if (revision !== modelRequestRevision) return

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
      if (revision === modelRequestRevision) fetchingModels.value = false
    }
  }

  return {
    targetModelOptions,
    fetchingModels,
    fetchModelsError,
    targetChannelOptions,
    mappingTargetChannelsError,
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
