// 快速添加草稿：输入检测与名称生成，独立于详细表单和网络请求。
import { computed, ref } from 'vue'
import { parseQuickInput } from '@/utils/quickInputParser'

export function useQuickChannelDraft() {
  // 快速添加模式的数据
  const quickInput = ref('')
  const quickChannelName = ref('')
  const detectedBaseUrl = ref('')
  const detectedBaseUrls = ref<string[]>([])
  const detectedApiKeys = ref<string[]>([])
  const detectedChannelName = ref('')
  const detectedServiceType = ref<'openai' | 'gemini' | 'claude' | 'responses' | 'chat' | null>(null)

  // 生成随机字符串
  const generateRandomString = (length: number): string => {
    const chars = 'abcdefghijklmnopqrstuvwxyz0123456789'
    let result = ''
    for (let i = 0; i < length; i++) {
      result += chars.charAt(Math.floor(Math.random() * chars.length))
    }
    return result
  }

  // 从 URL 提取二级域名
  const extractDomain = (url: string): string => {
    try {
      const hostname = new URL(url).hostname
      // 移除 www. 前缀
      const cleanHost = hostname.replace(/^www\./, '')
      const parts = cleanHost.split('.')

      // 处理特殊情况
      if (parts.length <= 1) {
        // localhost 等单段域名
        return cleanHost
      } else if (parts.length === 2) {
        // example.com → example
        return parts[0]
      } else {
        // api.openai.com → openai (取倒数第二段)
        return parts[parts.length - 2]
      }
    } catch {
      return 'channel'
    }
  }

  // 随机后缀和生成的渠道名称
  const randomSuffix = ref(generateRandomString(6))

  const generatedChannelName = computed(() => {
    const manualName = quickChannelName.value.trim()
    if (manualName) {
      return manualName
    }
    if (detectedChannelName.value) {
      return detectedChannelName.value
    }
    if (!detectedBaseUrl.value) {
      return `channel-${randomSuffix.value}`
    }
    const domain = extractDomain(detectedBaseUrl.value)
    return `${domain}-${randomSuffix.value}`
  })

  const parse = (useFullUrl: boolean) => {
    const result = parseQuickInput(quickInput.value, { useFullUrl })
    detectedBaseUrl.value = result.detectedBaseUrl
    detectedBaseUrls.value = result.detectedBaseUrls
    detectedApiKeys.value = result.detectedApiKeys
    detectedChannelName.value = result.detectedChannelName
    detectedServiceType.value = result.detectedServiceType
  }

  const reset = () => {
    quickInput.value = ''
    quickChannelName.value = ''
    detectedBaseUrl.value = ''
    detectedBaseUrls.value = []
    detectedApiKeys.value = []
    detectedChannelName.value = ''
    detectedServiceType.value = null
    randomSuffix.value = generateRandomString(6)
  }

  return { quickInput, quickChannelName, detectedBaseUrl, detectedBaseUrls, detectedApiKeys, detectedChannelName, detectedServiceType, generatedChannelName, parse, reset }
}
