// 内容安全闸门与助手 API：复用统一 HTTP 基础层，保留端点与返回契约。
import { apiRequest } from './http'
import type {
  AssistantChatOutcome,
  AssistantMessage,
  AssistantSession,
  AssistantStatusResponse,
  GateDecision,
  GateDecisionResponse,
  GatePending,
  GatePendingResponse
} from '@/types'

export const assistantApi = {
  // 危险命令 AI 闸门：待确认列表 / 用户决定 / 会话级白黑名单
  async getGatePending(signal?: AbortSignal): Promise<GatePendingResponse> {
    return apiRequest('/security/gate/pending', { signal })
  },

  async decideGatePending(
    id: number,
    decision: 'allow' | 'block',
    signal?: AbortSignal
  ): Promise<{ pending: GatePending }> {
    return apiRequest(`/security/gate/pending/${id}/decision`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ decision }),
      signal
    })
  },

  async listGateDecisions(): Promise<GateDecisionResponse> {
    return apiRequest('/security/gate/decisions')
  },

  async addGateDecision(body: {
    conversationId: string
    decision: 'allow' | 'block'
    commandHash: string
    rule?: string
    command?: string
  }): Promise<{ decision: GateDecision }> {
    return apiRequest('/security/gate/decisions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    })
  },

  async removeGateDecisions(
    conversationId: string,
    commandHash: string
  ): Promise<{ removed: number }> {
    return apiRequest(
      `/security/gate/decisions/${encodeURIComponent(conversationId)}?commandHash=${encodeURIComponent(commandHash)}`,
      { method: 'DELETE' }
    )
  },

  // AI 配置助手
  async getAssistantStatus(): Promise<AssistantStatusResponse> {
    return apiRequest('/assistant/status')
  },

  async createAssistantSession(title?: string): Promise<AssistantSession> {
    const response: { session: AssistantSession } = await apiRequest('/assistant/sessions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: title ?? '' })
    })
    return response.session
  },

  async listAssistantSessions(): Promise<AssistantSession[]> {
    const response: { sessions: AssistantSession[] } = await apiRequest('/assistant/sessions')
    return response.sessions || []
  },

  async deleteAssistantSession(id: number): Promise<void> {
    await apiRequest(`/assistant/sessions/${id}`, { method: 'DELETE' })
  },

  async getAssistantMessages(sessionId: number): Promise<AssistantMessage[]> {
    const response: { messages: AssistantMessage[] } = await apiRequest(`/assistant/sessions/${sessionId}/messages`)
    return response.messages || []
  },

  async sendAssistantMessage(sessionId: number, content: string): Promise<AssistantChatOutcome> {
    return apiRequest(`/assistant/sessions/${sessionId}/messages`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ content })
    })
  },
}
