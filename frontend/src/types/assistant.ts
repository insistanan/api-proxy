// AI 配置助手类型（会话 / 消息 / 行动执行结果）。

export interface AssistantSession {
  id: number
  title: string
  createdAt: string
  updatedAt: string
}

export interface AssistantActionRecord {
  action: string
  success: boolean
  detail: string
}

export interface AssistantMessage {
  id: number
  sessionId: number
  role: 'user' | 'assistant'
  content: string
  actions?: AssistantActionRecord[]
  createdAt: string
}

/** 助手当前会命中的渠道（status 探测结果） */
export interface AssistantStatusChannel {
  /** messages / responses / chat / gemini */
  kind: string
  name: string
  id: string
}

/** 渠道选择器候选项（按 messages → responses → chat → gemini 顺序） */
export interface AssistantChannelOption {
  kind: string
  id: string
  name: string
}

export interface AssistantStatusResponse {
  available: boolean
  reason?: string
  /** 配置的模型 ID（分组匹配词）；空串表示自动（兜底分组） */
  model?: string
  /** 配置的显式渠道 ID；空串表示自动（按四协议优先级编排） */
  channelId?: string
  channel?: AssistantStatusChannel | null
  /** 渠道选择器全部候选 */
  channels?: AssistantChannelOption[]
}

export interface AssistantChatOutcome {
  userMessage: AssistantMessage
  assistantMessage: AssistantMessage
  actions: AssistantActionRecord[]
}
