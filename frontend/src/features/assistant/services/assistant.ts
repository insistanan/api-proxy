// 配置助手 API：复用统一 HTTP 基础层，保留端点与返回契约。
import { apiRequest } from '@/shared/services/http'
import type {
  AssistantChatOutcome,
  AssistantMessage,
  AssistantSession,
  AssistantStatusResponse
} from '@/types'

export const assistantApi = {
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
