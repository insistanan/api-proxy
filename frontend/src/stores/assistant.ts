import { defineStore } from 'pinia'
import {
  api,
  type AssistantChannelOption,
  type AssistantMessage,
  type AssistantSession,
  type AssistantStatusChannel,
} from '@/services/api'

// AI 配置助手的会话状态：全局浮窗跨页面共享，切页不丢上下文。
export const useAssistantStore = defineStore('assistant', {
  state: () => ({
    available: false,
    unavailableReason: '',
    /** 配置的模型 ID（分组匹配词）；空串表示自动（兜底分组） */
    model: '',
    /** 配置的显式渠道 ID；空串表示自动（按四协议优先级编排） */
    channelId: '',
    /** 当前配置下实际会命中的渠道（status 探测结果） */
    channel: null as AssistantStatusChannel | null,
    /** 渠道选择器全部候选（按 messages → responses → chat → gemini 顺序） */
    channels: [] as AssistantChannelOption[],
    statusChecked: false,
    sessions: [] as AssistantSession[],
    activeSessionId: null as number | null,
    messages: [] as AssistantMessage[],
    sending: false,
    panelOpen: false,
  }),
  getters: {
    activeSession(state): AssistantSession | null {
      return state.sessions.find(session => session.id === state.activeSessionId) ?? null
    },
  },
  actions: {
    async checkStatus() {
      try {
        const status = await api.getAssistantStatus()
        this.available = status.available
        this.unavailableReason = status.reason ?? ''
        this.model = status.model ?? ''
        this.channelId = status.channelId ?? ''
        this.channel = status.channel ?? null
        this.channels = status.channels ?? []
      } catch {
        this.available = false
        this.unavailableReason = '助手状态查询失败'
        this.model = ''
        this.channelId = ''
        this.channel = null
        this.channels = []
      } finally {
        this.statusChecked = true
      }
    },
    // 切换助手渠道：即时保存并刷新状态。model 与 channelId 必须同时提交，
    // 否则未提交的字段会被设置接口按零值覆盖。
    async selectChannel(channelId: string) {
      try {
        await api.updateSettings({ assistant: { model: this.model, channelId } })
        await this.checkStatus()
      } catch (error) {
        this.unavailableReason = error instanceof Error ? error.message : '切换助手渠道失败'
      }
    },
    async loadSessions() {
      this.sessions = await api.listAssistantSessions()
      if (this.activeSessionId === null || !this.sessions.some(session => session.id === this.activeSessionId)) {
        this.activeSessionId = this.sessions[0]?.id ?? null
      }
      if (this.activeSessionId !== null) {
        await this.loadMessages(this.activeSessionId)
      } else {
        this.messages = []
      }
    },
    async loadMessages(sessionId: number) {
      this.messages = await api.getAssistantMessages(sessionId)
    },
    async newSession() {
      const session = await api.createAssistantSession()
      this.sessions.unshift(session)
      this.activeSessionId = session.id
      this.messages = []
    },
    async selectSession(sessionId: number) {
      if (this.activeSessionId === sessionId) return
      this.activeSessionId = sessionId
      await this.loadMessages(sessionId)
    },
    async deleteSession(sessionId: number) {
      await api.deleteAssistantSession(sessionId)
      this.sessions = this.sessions.filter(session => session.id !== sessionId)
      if (this.activeSessionId === sessionId) {
        this.activeSessionId = this.sessions[0]?.id ?? null
        if (this.activeSessionId !== null) {
          await this.loadMessages(this.activeSessionId)
        } else {
          this.messages = []
        }
      }
    },
    async send(content: string) {
      if (this.activeSessionId === null) {
        await this.newSession()
        if (this.activeSessionId === null) return
      }
      const sessionId = this.activeSessionId
      // 乐观追加用户消息，发送失败时回滚。
      const optimistic: AssistantMessage = {
        id: -Date.now(),
        sessionId,
        role: 'user',
        content,
        createdAt: new Date().toISOString(),
      }
      this.messages.push(optimistic)
      this.sending = true
      try {
        const outcome = await api.sendAssistantMessage(sessionId, content)
        this.messages.pop()
        this.messages.push(outcome.userMessage, outcome.assistantMessage)
        // 首条消息后刷新会话列表标题（标题由后端按首条消息维护）。
        if (this.messages.filter(message => message.role === 'user').length === 1) {
          await this.loadSessions().catch(() => undefined)
          this.activeSessionId = sessionId
          await this.loadMessages(sessionId)
        }
      } catch (error) {
        this.messages.pop()
        throw error
      } finally {
        this.sending = false
      }
    },
    togglePanel() {
      this.panelOpen = !this.panelOpen
      if (!this.panelOpen) return
      // 每次打开都刷新状态：设置中的分组变化要立即反映到路由信息；
      // 会话列表只在尚无会话时加载（已有会话时切页不重置上下文）。
      void this.checkStatus().then(() => {
        if (!this.available) return
        if (this.sessions.length === 0) return this.loadSessions()
      })
    },
  },
})
