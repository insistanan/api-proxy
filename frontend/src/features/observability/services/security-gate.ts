// 命令审核 API：待确认通知、审核决定与可用渠道。
import { apiRequest } from '@/shared/services/http'
import type { GateChannelOption, GateDecision, GateDecisionResponse, GatePending, GatePendingResponse } from '@/types'

export const securityGateApi = {
  async getGateChannels(): Promise<{ channels: GateChannelOption[] }> {
    return apiRequest('/security/gate/channels')
  },
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

}
