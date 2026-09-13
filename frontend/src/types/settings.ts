// 应用设置与内容安全类型。

export interface AppSettings {
  network: {
    upstreamProxyUrl: string
    upstreamProxyEnabled: boolean
  }
  contentSafety: ContentSafetySettings
  integration: {
    /** CC Switch 可执行文件路径；空串表示未配置 */
    ccSwitchPath: string
  }
  assistant: {
    /** 助手模型 ID（渠道分组的匹配词）；空串表示自动（走各协议兜底分组） */
    model: string
    /** 显式指定的渠道稳定 ID；空串表示自动（按四协议优先级编排，不换渠道） */
    channelId: string
  }
}

export interface ContentSafetySettings {
  sensitiveData: {
    mode: 'block' | 'mask'
  }
  sensitiveWord: {
    enabled: boolean
    pornographyEnabled: boolean
    gamblingEnabled: boolean
    drugsEnabled: boolean
    violenceTerrorEnabled: boolean
    politicalEnabled: boolean
    illegalCrimeEnabled: boolean
    customWords: string[]
  }
  sensitiveInfo: {
    enabled: boolean
    mode: ContentSafetyMode
    enabledRules: SensitiveInfoRule[]
    ipMaskScope: SensitiveInfoIPMaskScope
  }
  credential: {
    enabled: boolean
    userInputMode: ContentSafetyMode
    toolResultMode: Exclude<ContentSafetyMode, 'mask'>
    toolArgumentMode: Exclude<ContentSafetyMode, 'mask'>
    enabledRules: CredentialRule[]
  }
  dangerousCmd: {
    enabled: boolean
    enabledRules: DangerousCommandRule[]
    aiGate: DangerousCmdAIGateSettings
  }
  whitelist: {
    enabled: boolean
    toolNames: string[]
  }
  /** 字符串级放行字面量：条目与命中片段完全相等时跳过全部检测 */
  allowlist: {
    enabled: boolean
    entries: string[]
  }
  /** 自定义检测规则：正则命中走可逆脱敏（仅 mask 模式参与） */
  customRules: {
    enabled: boolean
    rules: CustomRuleItem[]
  }
}

/** 一条用户自定义的正则检测规则 */
export interface CustomRuleItem {
  name: string
  /** 占位符类型前缀，[A-Z0-9_]{1,16} */
  type: string
  /** Go RE2 正则 */
  pattern: string
}

/** 危险命令 AI 闸门：命中后先查会话白/黑名单，再由代理调用大模型渠道二次判断 */
export interface DangerousCmdAIGateSettings {
  enabled: boolean
  /** 判断渠道稳定 ID；空串表示按 messages → responses → chat → gemini 自动挑选 */
  channelId: string
  /** 用户决定窗口秒数（3-120，默认 10） */
  waitSeconds: number
}

export type ContentSafetyMode = 'audit' | 'block' | 'mask'
export type SensitiveInfoRule = 'phone' | 'id_card' | 'email' | 'ip_address' | 'bank_card'
export type SensitiveInfoIPMaskScope = 'public' | 'all'
export type CredentialRule = 'api_key' | 'named_secret' | 'private_key' | 'connection_string' | 'high_entropy'
export type DangerousCommandRule =
  | 'destructive'
  | 'download_execute'
  | 'reverse_shell'
  | 'privilege_escalation'
  | 'environment_tampering'
