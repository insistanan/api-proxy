// 渠道弹窗编排：连接独立草稿、快速输入和模型探测，管理校验、提交与关闭。
import { ref, reactive, computed, watch, onMounted, onUnmounted } from 'vue'
import { useTheme } from 'vuetify'
import type { Channel, ChannelPool, ChannelStatus } from '../services/api'
import { api, channelApiByType } from '../services/api'
import { imagesModelOptions, imagesProtocolLabel, isImagesServiceType, validateImagesOutputSettings } from '@/utils/imagesChannelProtocol'
import {
  isValidApiKey as _isValidApiKey,
  isValidUrl as _isValidQuickInputUrl
} from '../utils/quickInputParser'
import { useChannelEditor, buildImagesOutputSettings, EMBEDDINGS_DIMENSIONS_DEFAULT, validateEmbeddingsDimensions } from '../composables/useChannelEditor'
import { compileImagesExtraParams } from '@/utils/imagesExtraParams'

import { useQuickChannelDraft } from './useQuickChannelDraft'
import { useChannelModels } from './useChannelModels'
import type { ApiTab } from '@/services/channel-api'

export interface ChannelModalProps {
  show: boolean
  saving?: boolean
  channel?: Channel | null
  channelType: ApiTab
}

export interface ChannelModalEmit {
  (event: 'update:show', value: boolean): void
  (event: 'save', channel: Omit<Channel, 'id' | 'index' | 'latency'>, options?: { isQuickAdd?: boolean }): void
}

export function useChannelModal(props: ChannelModalProps, emit: ChannelModalEmit) {
  // 主题
  const theme = useTheme()

  // 表单引用
  const formRef = ref()

  // 高级兼容设置默认折叠，每次载入渠道或重置草稿时收起。
  const reasoningCompatibilityPanel = ref<string>()

  // 模式切换: 快速添加 vs 详细表单
  const isQuickMode = ref(true)

  const quickDraft = useQuickChannelDraft()
  const { quickInput, quickChannelName, detectedBaseUrl, detectedBaseUrls, detectedApiKeys, detectedChannelName, detectedServiceType, generatedChannelName } = quickDraft

  // Embeddings / Decisions 只接受 OpenAI 兼容上游，不允许从快速输入推断服务类型。
  const isFixedOpenAIProtocol = () => props.channelType === 'embeddings' || props.channelType === 'decisions'

  // 切换模式时，将快速模式检测到的值同步到详细表单，但不清空快速模式输入
  const toggleMode = () => {
    if (isQuickMode.value) {
      // 从快速模式切换到详细模式：始终用检测到的值覆盖表单
      if (detectedBaseUrls.value.length > 0) {
        // 多个 BaseURL
        form.baseUrl = detectedBaseUrls.value[0]
        form.baseUrls = [...detectedBaseUrls.value]
        baseUrlsText.value = detectedBaseUrls.value.join('\n')
      } else if (detectedBaseUrl.value) {
        // 单个 BaseURL
        form.baseUrl = detectedBaseUrl.value
        form.baseUrls = []
        baseUrlsText.value = detectedBaseUrl.value
      }
      if (detectedApiKeys.value.length > 0) {
        form.apiKeys = [...detectedApiKeys.value]
      }
      if (generatedChannelName.value) {
        form.name = generatedChannelName.value
      }
      if (props.channelType !== 'images') {
        form.serviceType = isFixedOpenAIProtocol() ? 'openai' : detectedServiceType.value || getDefaultServiceTypeValue()
      }
    }
    // 切换回快速模式时不做任何清理，保留 quickInput 原有内容
    isQuickMode.value = !isQuickMode.value
  }

  // 解析快速输入内容
  const parseQuickInput = () => {
    quickDraft.parse(form.useFullUrl)
    if (props.channelType === 'images' && detectedServiceType.value && isImagesServiceType(detectedServiceType.value)) {
      form.serviceType = detectedServiceType.value
    }
  }

  // 获取默认服务类型
  const getDefaultServiceType = (): string => {
    if (props.channelType === 'gemini') {
      return 'Gemini'
    }
    if (props.channelType === 'responses') {
      return 'Responses (原生接口)'
    }
    if (props.channelType === 'chat') {
      return 'Chat'
    }
    if (props.channelType === 'images') {
      return 'Images'
    }
    if (props.channelType === 'embeddings') {
      return 'Embeddings'
    }
    if (props.channelType === 'decisions') {
      return 'Decisions'
    }
    return 'Messages'
  }

  // 获取默认服务类型值
  const getDefaultServiceTypeValue = (): 'openai' | 'gemini' | 'claude' | 'responses' | 'chat' => {
    if (props.channelType === 'gemini') {
      return 'gemini'
    }
    if (props.channelType === 'responses') {
      return 'responses'
    }
    if (props.channelType === 'chat' || props.channelType === 'images' || isFixedOpenAIProtocol()) {
      return 'openai'
    }
    return 'claude'
  }

  // 快速模式表单验证
  const isQuickFormValid = computed(() => {
    return detectedBaseUrls.value.length > 0 && detectedApiKeys.value.length > 0 && isImagesProtocolValid.value && (
      props.channelType !== 'images' || !validateImagesOutputSettings(form)
    )
  })

  // 处理快速添加提交
  const handleQuickSubmit = () => {
    if (props.saving) return
    if (!isQuickFormValid.value) return

    const channelData = {
      maxConcurrency: 5,
      clientIdentityMode: 'inherit' as const,
      name: generatedChannelName.value,
      serviceType: isFixedOpenAIProtocol()
        ? 'openai'
        : props.channelType === 'images'
          ? form.serviceType as Channel['serviceType']
          : detectedServiceType.value || getDefaultServiceTypeValue(),
      responsesModel: props.channelType === 'images' && form.serviceType === 'responses' ? form.responsesModel.trim() : '',
      ...(props.channelType === 'images' ? buildImagesOutputSettings(form) : {}),
      embeddingsDimensions: props.channelType === 'embeddings' ? EMBEDDINGS_DIMENSIONS_DEFAULT : 0,
      baseUrl: detectedBaseUrl.value,
      baseUrls: detectedBaseUrls.value,
      useFullUrl: form.useFullUrl,
      apiKeys: detectedApiKeys.value,
      modelMapping: {},
      defaultModel: '',
      poolId: 'default',
      visionCapable: props.channelType === 'responses',
      excludeFromConversation: false,
      visionLayerEnabled: false,
      disablePromptCacheKey: false,
      proxyMode: 'inherit' as const,
      proxyUrl: ''
    }

    // 传递 isQuickAdd 标志，让 App.vue 知道需要进行后续处理
    emit('save', channelData, { isQuickAdd: true })
  }

  // 服务类型选项 - 根据渠道类型动态显示
  const serviceTypeOptions = computed(() => {
    if (props.channelType === 'gemini') {
      return [
        { title: 'Gemini', value: 'gemini' },
        { title: 'Chat', value: 'openai' },
        { title: 'Messages', value: 'claude' }
      ]
    }
    if (props.channelType === 'chat') {
      return [
        { title: 'Chat', value: 'openai' }
      ]
    }
    if (props.channelType === 'embeddings' || props.channelType === 'decisions') {
      return [
        { title: 'OpenAI 兼容', value: 'openai' }
      ]
    }
    if (props.channelType === 'responses') {
      return [
        { title: 'Responses (原生接口)', value: 'responses' },
        { title: 'Chat', value: 'openai' },
        { title: 'Messages', value: 'claude' }
      ]
    } else {
      return [
        { title: 'Chat', value: 'openai' },
        { title: 'Messages', value: 'claude' },
        { title: 'Responses', value: 'responses' },
        { title: 'Gemini', value: 'gemini' }
      ]
    }
  })

  // 全部源模型选项 - 根据渠道类型动态显示
  const allSourceModelOptions = computed(() => {
    if (props.channelType === 'images') {
      return [...imagesModelOptions]
    }
    if (props.channelType === 'gemini') {
      // Gemini API 常用模型别名（模糊匹配，无需指定版本）
      return [
        { title: 'gemini-flash', value: 'gemini-flash' },
        { title: 'gemini-pro', value: 'gemini-pro' },
      ]
    }
    if (props.channelType === 'responses') {
      // Responses API (Codex) 常用模型名称（模糊匹配，无需指定版本）
      return [
        { title: 'codex', value: 'codex' },
        { title: 'gpt-5', value: 'gpt-5' },
        { title: 'gpt-5.2', value: 'gpt-5.2' },
        { title: 'gpt-5.3', value: 'gpt-5.3' },
        { title: 'gpt-5.3-codex', value: 'gpt-5.3-codex' },
        { title: 'gpt-5.4', value: 'gpt-5.4' },
        { title: 'gpt-5.4-mini', value: 'gpt-5.4-mini' },
        { title: 'gpt-5.5', value: 'gpt-5.5' },
        { title: 'gpt-5.6-luna', value: 'gpt-5.6-luna' },
        { title: 'gpt-5.6-sol', value: 'gpt-5.6-sol' },
        { title: 'gpt-5.6-terra', value: 'gpt-5.6-terra' },
      ]
    }
    if (props.channelType === 'embeddings') {
      return [
        { title: 'text-embedding-3-small', value: 'text-embedding-3-small' },
        { title: 'text-embedding-3-large', value: 'text-embedding-3-large' },
        { title: 'text-embedding-ada-002', value: 'text-embedding-ada-002' },
      ]
    }
    if (props.channelType === 'decisions') {
      return [
        { title: 'cloudflare/clef', value: 'cloudflare/clef' },
      ]
    }
    if (props.channelType === 'chat') {
      return [
        { title: 'gpt-4o', value: 'gpt-4o' },
        { title: 'gpt-4.1', value: 'gpt-4.1' },
        { title: 'gpt-5', value: 'gpt-5' },
        { title: 'o3', value: 'o3' },
        { title: 'o4-mini', value: 'o4-mini' },
      ]
    } else {
      // Messages API (Claude) 常用模型别名（模糊匹配，无需指定版本）
      return [
        { title: 'opus', value: 'opus' },
        { title: 'sonnet', value: 'sonnet' },
        { title: 'haiku', value: 'haiku' }
      ]
    }
  })

  // 可选的源模型选项
  // 允许重复选择同一个源模型，以便为其继续追加多个目标模型
  const sourceModelOptions = computed(() => [
    { title: '*（任意源模型）', value: '*' },
    ...allSourceModelOptions.value
  ])

  const targetModelPlaceholder = computed(() => {
    if (props.channelType === 'images') {
      return '例如：sensenova-u1.5-lite'
    }
    if (props.channelType === 'embeddings') {
      return '例如：text-embedding-3-small'
    }
    if (props.channelType === 'decisions') {
      return '例如：cloudflare/clef'
    }
    if (props.channelType === 'gemini') {
      return '例如：gemini-2.0-flash'
    }
    if (props.channelType === 'responses') {
      return '例如：gpt-5.1-codex-max'
    }
    if (props.channelType === 'chat') {
      return '例如：gpt-4.1'
    } else {
      return '例如：claude-3-5-sonnet'
    }
  })

  // 表单草稿经 useChannelEditor（R5，方案 8.1）：草稿独立于 store，取消不落盘。
  const { form, resetForm: resetFormDraft, loadChannelData: loadChannelDataDraft, buildPayload } = useChannelEditor(() => props.channelType)

  // 开关以“自动补全”表达，配置只记录是否使用完整地址，旧渠道默认保持开启。
  const autoCompleteUrl = computed({
    get: () => !form.useFullUrl,
    set: (enabled: boolean) => { form.useFullUrl = !enabled }
  })

  watch(() => form.useFullUrl, () => {
    if (!isEditing.value && isQuickMode.value) parseQuickInput()
  })

  const imagesExtraParamsError = ref('')
  const imagesOutputError = ref('')
  const isNativeSpecialOutput = computed(() => form.serviceType === 'openai' && (
    form.imagesOutputFormat === 'gif' || form.imagesOutputFormat === 'svg'
  ))
  watch(() => [form.imagesOutputFormat, form.imagesFrameCount, form.imagesFrameDurationMs, form.serviceType], () => {
    imagesOutputError.value = ''
  })

  const addImagesExtraParam = () => {
    imagesExtraParamsError.value = ''
    form.imagesExtraParams.push({ key: '', value: '' })
  }

  const removeImagesExtraParam = (index: number) => {
    imagesExtraParamsError.value = ''
    form.imagesExtraParams.splice(index, 1)
  }

  const addApiKey = () => {
    const key = newApiKey.value.trim()
    if (!key) return

    // 重置错误状态
    apiKeyError.value = ''
    duplicateKeyIndex.value = -1

    // 检查是否与现有密钥重复
    const duplicateIndex = findDuplicateKeyIndex(key)
    if (duplicateIndex !== -1) {
      apiKeyError.value = '该密钥已存在'
      duplicateKeyIndex.value = duplicateIndex
      // 清除输入框，让用户重新输入
      newApiKey.value = ''
      return
    }

    // 直接存储原始密钥
    form.apiKeys.push(key)
    newApiKey.value = ''
  }

  // resetForm：草稿重置 + 组件内 UI 状态（错误提示、模型缓存、快速添加输入）清理。
  const resetForm = () => {
    resetFormDraft()
    reasoningCompatibilityPanel.value = undefined
    newApiKey.value = ''
    baseUrlsText.value = ''
    originalKeyMap.value.clear()
    apiKeyError.value = ''
    duplicateKeyIndex.value = -1
    targetModelOptions.value = []
    fetchingModels.value = false
    fetchModelsError.value = ''
    targetChannelModelOptions.value = {}
    targetChannelModelStatus.value = {}
    keyModelsStatus.value.clear()
    errors.name = ''
    errors.serviceType = ''
    errors.baseUrl = ''
    quickDraft.reset()
  }

  // loadChannelData：载入编辑目标 + 组件内 UI 状态同步（baseUrl 预览、模型缓存重置）。
  const loadChannelData = (channel: Channel) => {
    loadChannelDataDraft(channel)
    reasoningCompatibilityPanel.value = undefined
    if (channel.baseUrls && channel.baseUrls.length > 0) {
      baseUrlsText.value = channel.baseUrls.join('\n')
    } else {
      baseUrlsText.value = channel.baseUrl || ''
    }
    originalKeyMap.value.clear()
    targetModelOptions.value = []
    fetchingModels.value = false
    fetchModelsError.value = ''
    targetChannelModelOptions.value = {}
    targetChannelModelStatus.value = {}
    keyModelsStatus.value.clear()
  }

  const channelStatusOptions = [
    { title: '活跃', value: 'active' },
    { title: '熔断', value: 'suspended' },
    { title: '禁用', value: 'disabled' }
  ] satisfies Array<{ title: string; value: ChannelStatus }>

  const proxyModeOptions = [
    { title: '继承全局设置', value: 'inherit' },
    { title: '强制直连', value: 'direct' },
    { title: '使用独立代理', value: 'custom' }
  ]

  // 多 BaseURL 文本输入（独立变量，保留用户输入的换行）
  const baseUrlsText = ref('')

  // 监听 baseUrlsText 变化，同步到 form（仅做基本同步，不修改用户输入）
  watch(baseUrlsText, val => {
    const urls = val
      .split('\n')
      .map(s => s.trim())
      .filter(Boolean)
    if (urls.length === 0) {
      form.baseUrl = ''
      form.baseUrls = []
    } else if (urls.length === 1) {
      form.baseUrl = urls[0]
      form.baseUrls = []
    } else {
      form.baseUrl = urls[0]
      form.baseUrls = urls
    }
  })

  // 原始密钥映射 (掩码密钥 -> 原始密钥)
  const originalKeyMap = ref<Map<string, string>>(new Map())

  // 新API密钥输入
  const newApiKey = ref('')

  // 密钥重复检测状态
  const apiKeyError = ref('')
  const duplicateKeyIndex = ref(-1)

  // 处理 API 密钥输入事件
  const handleApiKeyInput = () => {
    apiKeyError.value = ''
    duplicateKeyIndex.value = -1
  }

  // 复制功能相关状态
  const copiedKeyIndex = ref<number | null>(null)

  const {
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
    loadMappingTargetChannels
  } = useChannelModels(props, form, isQuickMode, quickDraft)

  // 表单验证错误
  const errors = reactive({
    name: '',
    serviceType: '',
    baseUrl: '',
    website: ''
  })

  // 验证规则
  const rules = {
    required: (value: string) => !!value || '此字段为必填项',
    url: (value: string) => {
      try {
        new URL(value)
        return true
      } catch {
        return '请输入有效的URL'
      }
    },
    urlOptional: (value: string) => {
      if (!value) return true
      try {
        new URL(value)
        return true
      } catch {
        return '请输入有效的URL'
      }
    },
    baseUrls: (value: string) => {
      if (!value) return '此字段为必填项'
      const urls = value
        .split('\n')
        .map(s => s.trim())
        .filter(Boolean)
      if (urls.length === 0) return '请至少输入一个 URL'
      for (const url of urls) {
        try {
          new URL(url)
        } catch {
          return `无效的 URL: ${url}`
        }
      }
      return true
    },
    proxyUrl: (value: string) => {
      if (form.proxyMode !== 'custom') return true
      const raw = value?.trim()
      if (!raw) return '使用独立代理时必须填写代理地址'
      try {
        const parsed = new URL(raw)
        if (!['http:', 'https:', 'socks5:', 'socks5h:'].includes(parsed.protocol)) {
          return '仅支持 http、https、socks5 和 socks5h 协议'
        }
        if (!parsed.hostname) return '代理地址必须包含主机'
        if ((parsed.pathname && parsed.pathname !== '/') || parsed.search || parsed.hash) {
          return '代理地址不能包含路径、查询参数或片段'
        }
        return true
      } catch {
        return '请输入有效的代理地址'
      }
    }
  }

  // 计算属性
  const isEditing = computed(() => !!props.channel)

  // 动态header样式
  const headerClasses = computed(() => {
    const isDark = theme.global.current.value.dark
    // Dark: keep neutral surface header; Light: use brand primary header
    return isDark ? 'bg-surface text-high-emphasis' : 'bg-primary text-white'
  })

  const avatarColor = computed(() => 'primary')

  // Use Vuetify theme "on-primary" token so icon isn't fixed white
  const headerIconStyle = computed(() => ({
    color: 'rgb(var(--v-theme-on-primary))'
  }))

  const subtitleClasses = computed(() => {
    const isDark = theme.global.current.value.dark
    // Dark mode: use medium emphasis; Light mode: use white with opacity for primary bg
    return isDark ? 'text-medium-emphasis' : 'text-white-subtitle'
  })

  const isFormValid = computed(() => {
    return (
      Number.isSafeInteger(Number(form.maxConcurrency)) && Number(form.maxConcurrency) > 0 &&
      form.name.trim() && form.serviceType && form.baseUrl.trim() && isValidUrl(form.baseUrl) && form.apiKeys.length > 0 && isImagesProtocolValid.value &&
      (!form.visionLayerEnabled || form.visionLayerChannelId) &&
      (form.proxyMode !== 'custom' || rules.proxyUrl(form.proxyUrl) === true) &&
      (props.channelType !== 'embeddings' || !validateEmbeddingsDimensions(form.embeddingsDimensions))
    )
  })

  const isImagesProtocolValid = computed(() => props.channelType !== 'images' || (
    isImagesServiceType(form.serviceType) && (form.serviceType !== 'responses' || !!form.responsesModel.trim())
  ))

  const visionChannels = ref<Channel[]>([])
  const visionChannelsLoading = ref(false)
  const pools = ref<ChannelPool[]>([])
  const poolsLoading = ref(false)
  const poolLoadError = ref('')
  const poolOptions = computed(() => pools.value.map(pool => ({
    title: pool.id === 'default' ? pool.name : `${pool.name}（匹配 ${pool.modelMatcher}）`,
    value: pool.id
  })))

  const visionChannelOptions = computed(() => {
    const currentChannelID = props.channel?.id
    return visionChannels.value
      .filter(channel => channel.id && channel.id !== currentChannelID && channel.visionCapable && channel.status === 'active' &&
        (channel.excludeFromConversation || (channel.poolId || 'default') === form.poolId))
      .map(channel => ({ title: `${channel.name}（${channel.serviceType}）`, value: channel.id }))
  })

  const visionLayerChannelError = computed(() => {
    if (form.visionLayerEnabled && !form.visionLayerChannelId) {
      return '启用图片理解层后必须选择图片理解渠道'
    }
    return ''
  })

  const loadVisionChannels = async () => {
    visionChannelsLoading.value = true
    try {
      const response = await channelApiByType(props.channelType).getChannels()
      visionChannels.value = response.channels || []
    } catch (error) {
      console.error('加载图片理解渠道失败:', error)
      visionChannels.value = []
    } finally {
      visionChannelsLoading.value = false
    }
  }

  const loadPools = async () => {
    poolsLoading.value = true
    poolLoadError.value = ''
    try {
      const response = await api.getChannelPools(props.channelType)
      pools.value = response.pools || []
      if (!pools.value.some(pool => pool.id === form.poolId)) form.poolId = 'default'
    } catch (error) {
      pools.value = []
      poolLoadError.value = error instanceof Error ? error.message : '加载分组失败'
    } finally {
      poolsLoading.value = false
    }
  }

  watch(() => form.visionCapable, enabled => {
    if (enabled) {
      form.visionLayerEnabled = false
      form.visionLayerChannelId = ''
      form.visionLayerModel = ''
    } else {
      form.excludeFromConversation = false
    }
  })

  watch(() => form.excludeFromConversation, enabled => {
    if (enabled) {
      form.visionCapable = true
    }
  })

  watch([() => form.poolId, visionChannelOptions], () => {
    if (form.visionLayerChannelId && !visionChannelOptions.value.some(option => option.value === form.visionLayerChannelId)) {
      form.visionLayerChannelId = ''
    }
  })

  watch(() => form.visionLayerEnabled, enabled => {
    if (enabled) {
      form.visionCapable = false
    } else {
      form.visionLayerChannelId = ''
      form.visionLayerModel = ''
    }
  })

  // 工具函数
  const isValidUrl = (url: string): boolean => {
    try {
      new URL(url)
      return true
    } catch {
      return false
    }
  }

  const maskApiKey = (key: string): string => {
    if (key.length <= 10) return key.slice(0, 3) + '***' + key.slice(-2)
    return key.slice(0, 8) + '***' + key.slice(-5)
  }

  // 表单操作
  // 检查密钥是否重复，返回重复密钥的索引，如果没有重复返回-1
  const findDuplicateKeyIndex = (newKey: string): number => {
    return form.apiKeys.findIndex(existingKey => existingKey === newKey)
  }

  const removeApiKey = (index: number) => {
    form.apiKeys.splice(index, 1)

    // 如果删除的是当前高亮的重复密钥，清除高亮状态
    if (duplicateKeyIndex.value === index) {
      duplicateKeyIndex.value = -1
      apiKeyError.value = ''
    } else if (duplicateKeyIndex.value > index) {
      // 如果删除的密钥在高亮密钥之前，调整高亮索引
      duplicateKeyIndex.value--
    }
  }

  // 将指定密钥移到最上方
  const moveApiKeyToTop = (index: number) => {
    if (index <= 0 || index >= form.apiKeys.length) return
    const [key] = form.apiKeys.splice(index, 1)
    form.apiKeys.unshift(key)
    duplicateKeyIndex.value = -1
    copiedKeyIndex.value = null
  }

  // 将指定密钥移到最下方
  const moveApiKeyToBottom = (index: number) => {
    if (index < 0 || index >= form.apiKeys.length - 1) return
    const [key] = form.apiKeys.splice(index, 1)
    form.apiKeys.push(key)
    duplicateKeyIndex.value = -1
    copiedKeyIndex.value = null
  }

  // 复制API密钥到剪贴板
  const copyApiKey = async (key: string, index: number) => {
    try {
      await navigator.clipboard.writeText(key)
      copiedKeyIndex.value = index

      // 2秒后重置复制状态
      setTimeout(() => {
        copiedKeyIndex.value = null
      }, 2000)
    } catch (err) {
      console.error('复制密钥失败:', err)
      // 降级方案：使用传统的复制方法
      const textArea = document.createElement('textarea')
      textArea.value = key
      textArea.style.position = 'fixed'
      textArea.style.left = '-999999px'
      textArea.style.top = '-999999px'
      document.body.appendChild(textArea)
      textArea.focus()
      textArea.select()

      try {
        document.execCommand('copy')
        copiedKeyIndex.value = index

        setTimeout(() => {
          copiedKeyIndex.value = null
        }, 2000)
      } catch (err) {
        console.error('降级复制方案也失败:', err)
      } finally {
        textArea.remove()
      }
    }
  }

  const handleSubmit = async () => {
    if (props.saving) return
    if (!formRef.value) return

    const { valid } = await formRef.value.validate()
    if (!valid) return

    let imagesExtraParams: Record<string, string> | undefined
    if (props.channelType === 'images') {
      imagesOutputError.value = validateImagesOutputSettings(form)
      if (imagesOutputError.value) return
      const compiled = compileImagesExtraParams(form.imagesExtraParams)
      if (compiled.error) {
        imagesExtraParamsError.value = compiled.error
        return
      }
      imagesExtraParamsError.value = ''
      imagesExtraParams = compiled.params
    }

    emit('save', buildPayload(isEditing.value, imagesExtraParams))
  }

  const handleCancel = () => {
    if (props.saving) return
    emit('update:show', false)
    resetForm()
  }

  // 监听props变化
  watch(
    () => props.show,
    newShow => {
      if (newShow) {
        // 无论是编辑还是新增，都先清理密钥错误状态
        apiKeyError.value = ''
        duplicateKeyIndex.value = -1
        imagesExtraParamsError.value = ''
        imagesOutputError.value = ''
        // 图片理解只属于对话类入口协议，透传协议不加载相关渠道。
        if (
          props.channelType === 'messages' ||
          props.channelType === 'responses' ||
          props.channelType === 'gemini' ||
          props.channelType === 'chat'
        ) {
          void loadVisionChannels()
        }
        void loadPools()
        void loadMappingTargetChannels()

        if (props.channel) {
          // 编辑模式：使用表单模式
          isQuickMode.value = false
          loadChannelData(props.channel)
        } else {
          // 添加模式：默认使用快速模式
          isQuickMode.value = true
          resetForm()
        }
      }
    }
  )

  watch(
    () => props.channel,
    newChannel => {
      if (newChannel && props.show) {
        loadChannelData(newChannel)
      }
    }
  )

  // ESC键监听
  const handleKeydown = (event: Event) => {
    const keyboardEvent = event as KeyboardEvent
    if (keyboardEvent.key === 'Escape' && props.show) {
      handleCancel()
    }
  }

  onMounted(() => {
    document.addEventListener('keydown', handleKeydown)
  })

  onUnmounted(() => {
    document.removeEventListener('keydown', handleKeydown)
  })

  return {
    formRef,
    reasoningCompatibilityPanel,
    isQuickMode,
    toggleMode,
    parseQuickInput,
    getDefaultServiceType,
    isQuickFormValid,
    handleQuickSubmit,
    serviceTypeOptions,
    sourceModelOptions,
    targetModelPlaceholder,
    autoCompleteUrl,
    imagesExtraParamsError,
    imagesOutputError,
    isNativeSpecialOutput,
    addImagesExtraParam,
    removeImagesExtraParam,
    addApiKey,
    channelStatusOptions,
    proxyModeOptions,
    baseUrlsText,
    newApiKey,
    apiKeyError,
    duplicateKeyIndex,
    handleApiKeyInput,
    copiedKeyIndex,
    errors,
    rules,
    isEditing,
    headerClasses,
    avatarColor,
    headerIconStyle,
    subtitleClasses,
    isFormValid,
    visionChannelsLoading,
    poolsLoading,
    poolLoadError,
    poolOptions,
    visionChannelOptions,
    visionLayerChannelError,
    maskApiKey,
    removeApiKey,
    moveApiKeyToTop,
    moveApiKeyToBottom,
    copyApiKey,
    handleSubmit,
    handleCancel,
    quickInput,
    quickChannelName,
    detectedBaseUrls,
    detectedApiKeys,
    detectedChannelName,
    generatedChannelName,
    form,
    targetModelOptions,
    fetchingModels,
    fetchModelsError,
    targetChannelOptions,
    targetModelOptionsByChannel,
    targetStatusByChannel,
    keyModelsStatus,
    handleFetchModels,
    imagesProtocolLabel,
    detectedBaseUrl,
    channel: computed(() => props.channel),
    channelType: computed(() => props.channelType),
    saving: computed(() => props.saving)
  }
}
