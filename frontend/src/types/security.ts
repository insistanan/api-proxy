// 危险命令 AI 闸门类型（待确认列表 / 会话级白黑名单）。

/** 一条等待用户决定的危险命令命中 */
export interface GatePending {
  id: number
  createdAt: string
  conversationId: string
  requestId: string
  apiType: string
  model?: string
  rule: string
  command: string
  /** code_block / tool */
  source: string
  /** AI 裁决：allow（已放行待否决）/ block（等待放行） */
  aiVerdict: 'allow' | 'block'
  aiReason?: string
  expiresAt: string
  status: string
}

/** 会话级白/黑名单决定（每会话取最新一条生效） */
export interface GateDecision {
  id: number
  conversationId: string
  decision: 'allow' | 'block'
  rule?: string
  commandSnippet?: string
  commandHash?: string
  createdAt: string
}

export interface GatePendingResponse {
  pending: GatePending[]
}

export interface GateDecisionResponse {
  decisions: GateDecision[]
}
