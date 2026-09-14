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
    /** 当前模型下可用的渠道候选（按 messages → responses → chat → gemini 顺序） */
    channels: [] as AssistantChannelOption[],
    statusChecked: false,
    statusLoading: false,
    statusRequestId: 0,
    channelSaving: false,
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
      const requestId = ++this.statusRequestId
      this.statusLoading = true
      try {
        const status = await api.getAssistantStatus()
        // 打开面板、展开菜单与切换渠道都可能刷新状态，只接收最新一次结果。
        if (requestId !== this.statusRequestId) return
        this.available = status.available
        this.unavailableReason = status.reason ?? ''
        this.model = status.model ?? ''
        this.channelId = status.channelId ?? ''
        this.channel = status.channel ?? null
        this.channels = status.channels ?? []
        this.statusChecked = true
      } catch (error) {
        if (requestId !== this.statusRequestId) return
        this.available = false
        this.unavailableReason = `助手状态查询失败：${error instanceof Error ? error.message : String(error)}`
        // 查询失败不代表配置已改为自动，保留上次确认的模型与渠道 ID。
        this.channel = null
        this.channels = []
      } finally {
        if (requestId === this.statusRequestId) {
          this.statusLoading = false
        }
      }
    },
    // 切换助手渠道：即时保存并刷新状态。model 与 channelId 必须同时提交，
    // 否则未提交的字段会被设置接口按零值覆盖。
    async selectChannel(channelId: string) {
      if (this.channelSaving || this.statusLoading || !this.statusChecked) return
      this.channelSaving = true
      // 保存开始后，之前发出的查询不能再覆盖状态或清除保存失败的提示。
      this.statusRequestId++
      this.statusLoading = false
      try {
        await api.updateSettings({ assistant: { model: this.model, channelId } })
        this.channelId = channelId
        await this.checkStatus()
      } catch (error) {
        this.unavailableReason = `切换助手渠道失败：${error instanceof Error ? error.message : String(error)}`
      } finally {
        this.channelSaving = false
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
