// 内容安全表单的默认草稿与旧配置规范化；不访问网络或组件状态。
import type { ContentSafetySettings, CustomRuleItem, SensitiveInfoRule } from '@/types'

export const defaultContentSafety = (): ContentSafetySettings => ({
  sensitiveData: {
    mode: 'block'
  },
  sensitiveWord: {
    enabled: false,
    pornographyEnabled: false,
    gamblingEnabled: false,
    drugsEnabled: false,
    violenceTerrorEnabled: false,
    politicalEnabled: false,
    illegalCrimeEnabled: false,
    customWords: []
  },
  sensitiveInfo: {
    enabled: false,
    mode: 'mask',
    enabledRules: [],
    ipMaskScope: 'all'
  },
  credential: {
    enabled: false,
    userInputMode: 'block',
    toolResultMode: 'block',
    toolArgumentMode: 'block',
    enabledRules: []
  },
  dangerousCmd: {
    enabled: false,
    enabledRules: [],
    aiGate: {
      enabled: false,
      protocol: 'chat',
      model: '',
      channelId: '',
      waitSeconds: 10
    }
  },
  whitelist: {
    enabled: false,
    toolNames: []
  },
  allowlist: {
    enabled: true,
    entries: ['127.0.0.1', 'localhost', '0.0.0.0', '::1']
  },
  customRules: {
    enabled: false,
    rules: []
  }
})

export const normalizeContentSafety = (
  settings: ContentSafetySettings | null | undefined
): ContentSafetySettings => {
  const defaults = defaultContentSafety()
  if (!settings) return defaults

  const sensitiveWord = settings.sensitiveWord
  const sensitiveInfo = settings.sensitiveInfo
  const credential = settings.credential
  const dangerousCmd = settings.dangerousCmd

  const rawInfoRules = Array.isArray(sensitiveInfo?.enabledRules)
    ? (sensitiveInfo.enabledRules as string[])
    : []
  const legacyAPIKey = rawInfoRules.includes('api_key')
  const normalizedInfoRules = rawInfoRules.filter(rule => rule !== 'api_key') as SensitiveInfoRule[]

  return {
    sensitiveData: {
      mode: settings.sensitiveData?.mode === 'mask' ? 'mask' : 'block'
    },
    sensitiveWord: {
      ...defaults.sensitiveWord,
      ...(sensitiveWord || {}),
      customWords: Array.isArray(sensitiveWord?.customWords) ? [...sensitiveWord.customWords] : []
    },
    sensitiveInfo: {
      ...defaults.sensitiveInfo,
      ...(sensitiveInfo || {}),
      enabled: legacyAPIKey && normalizedInfoRules.length === 0 ? false : Boolean(sensitiveInfo?.enabled),
      enabledRules: normalizedInfoRules,
      ipMaskScope:
        sensitiveInfo?.ipMaskScope === 'all' || sensitiveInfo?.ipMaskScope === 'public'
          ? sensitiveInfo.ipMaskScope
          : defaults.sensitiveInfo.ipMaskScope
    },
    credential: {
      ...defaults.credential,
      ...(credential || {}),
      enabled: credential?.enabled ?? (legacyAPIKey && Boolean(sensitiveInfo?.enabled)),
      userInputMode: credential?.userInputMode ?? (legacyAPIKey ? 'mask' : defaults.credential.userInputMode),
      toolResultMode: credential?.toolResultMode ?? defaults.credential.toolResultMode,
      toolArgumentMode: credential?.toolArgumentMode ?? defaults.credential.toolArgumentMode,
      enabledRules: Array.isArray(credential?.enabledRules)
        ? [...credential.enabledRules]
        : legacyAPIKey
          ? ['api_key']
          : []
    },
    dangerousCmd: {
      ...defaults.dangerousCmd,
      ...(dangerousCmd || {}),
      enabledRules: Array.isArray(dangerousCmd?.enabledRules)
        ? [...dangerousCmd.enabledRules]
        : dangerousCmd
          ? []
          : [...defaults.dangerousCmd.enabledRules],
      aiGate: {
        ...defaults.dangerousCmd.aiGate,
        ...(dangerousCmd?.aiGate || {}),
        protocol: dangerousCmd?.aiGate?.protocol ?? 'chat',
        model: typeof dangerousCmd?.aiGate?.model === 'string' ? dangerousCmd.aiGate.model : '',
        channelId: typeof dangerousCmd?.aiGate?.channelId === 'string' ? dangerousCmd.aiGate.channelId : '',
        waitSeconds:
          typeof dangerousCmd?.aiGate?.waitSeconds === 'number' && dangerousCmd.aiGate.waitSeconds > 0
            ? dangerousCmd.aiGate.waitSeconds
            : defaults.dangerousCmd.aiGate.waitSeconds
      }
    },
    whitelist: {
      ...defaults.whitelist,
      ...(settings.whitelist || {}),
      toolNames: Array.isArray(settings.whitelist?.toolNames) ? [...settings.whitelist.toolNames] : []
    },
    allowlist: {
      ...defaults.allowlist,
      ...(settings.allowlist || {}),
      enabled: settings.allowlist?.enabled ?? defaults.allowlist.enabled,
      entries: Array.isArray(settings.allowlist?.entries)
        ? settings.allowlist.entries.filter(entry => typeof entry === 'string' && entry !== '')
        : [...defaults.allowlist.entries]
    },
    customRules: {
      ...defaults.customRules,
      ...(settings.customRules || {}),
      rules: Array.isArray(settings.customRules?.rules)
        ? settings.customRules.rules
            .filter(
              (rule): rule is CustomRuleItem =>
                rule && typeof rule.name === 'string' && typeof rule.type === 'string' && typeof rule.pattern === 'string'
            )
            .map(rule => ({ name: rule.name, type: rule.type, pattern: rule.pattern }))
        : []
    }
  }
}
