// 渠道页的两个全局路由开关：自动故障转移（Fuzzy）与客户端伪装。
// 开关本体保存在服务端，preferences store 只镜像最近一次已知状态供多处渲染；
// 加载失败时保持“未知”并禁止切换，不用本地缓存冒充服务端配置。
import { computed, ref } from 'vue'
import { api, ApiError } from '@/services/api'
import { useChannelStore } from '@/stores/channel'
import { usePreferencesStore } from '@/stores/preferences'
import type { ToastType } from './useToast'

type DisguiseProtocol = 'messages' | 'responses'
const disguiseClients: Record<DisguiseProtocol, { label: string; client: string }> = {
  messages: { label: 'Claude 伪装', client: 'Claude Code' },
  responses: { label: 'Codex 伪装', client: 'Codex' }
}

const errorText = (error: unknown) => error instanceof Error ? error.message : '未知错误'
// 401 由认证流程统一提示，这里不再重复弹出加载失败。
const isAuthFailure = (error: unknown) => error instanceof ApiError && error.status === 401

export function useRoutingModes(notify: (message: string, type: ToastType) => void) {
  const channelStore = useChannelStore()
  const preferencesStore = usePreferencesStore()

  const fuzzyLoading = ref(false)
  const fuzzyUnknown = ref(false)
  const disguiseLoading = ref(false)
  const disguiseUnknown = ref(false)

  const loadFuzzyMode = async () => {
    try {
      preferencesStore.setFuzzyMode((await api.getFuzzyMode()).fuzzyModeEnabled)
      fuzzyUnknown.value = false
    } catch (error) {
      fuzzyUnknown.value = true
      if (!isAuthFailure(error)) notify('加载自动故障转移状态失败，请刷新页面重试', 'warning')
    }
  }

  const loadClientDisguise = async () => {
    try {
      const status = await api.getClientDisguise()
      preferencesStore.setClientDisguise('messages', status.claudeCodeDisguiseEnabled)
      preferencesStore.setClientDisguise('responses', status.codexDisguiseEnabled)
      disguiseUnknown.value = false
    } catch (error) {
      disguiseUnknown.value = true
      if (!isAuthFailure(error)) notify('加载客户端伪装状态失败，请刷新页面重试', 'warning')
    }
  }

  const loadModes = () => Promise.all([loadFuzzyMode(), loadClientDisguise()])

  const toggleFuzzyMode = async () => {
    const next = !preferencesStore.fuzzyModeEnabled
    fuzzyLoading.value = true
    try {
      await api.setFuzzyMode(next)
      preferencesStore.setFuzzyMode(next)
      notify(`自动故障转移已${next ? '启用' : '关闭'}`, 'success')
    } catch (error) {
      notify(`切换自动故障转移失败: ${errorText(error)}`, 'error')
    } finally {
      fuzzyLoading.value = false
    }
  }

  const fuzzyTooltip = computed(() => {
    if (fuzzyUnknown.value) return '加载失败，请刷新页面'
    return preferencesStore.fuzzyModeEnabled
      ? 'Fuzzy 模式已启用：模糊处理错误，自动尝试所有渠道'
      : 'Fuzzy 模式已关闭：固定首个渠道，仅重试其 Key 与地址'
  })

  // 客户端伪装只对 Messages / Responses 入口有意义，其余协议不显示开关。
  const disguiseProtocol = computed<DisguiseProtocol | null>(() =>
    channelStore.activeTab === 'messages' || channelStore.activeTab === 'responses' ? channelStore.activeTab : null
  )
  const disguiseEnabled = computed(() => {
    if (disguiseProtocol.value === 'messages') return preferencesStore.claudeCodeDisguiseEnabled
    if (disguiseProtocol.value === 'responses') return preferencesStore.codexDisguiseEnabled
    return false
  })
  const disguiseLabel = computed(() => disguiseProtocol.value ? disguiseClients[disguiseProtocol.value].label : '')
  const disguiseTooltip = computed(() => {
    if (!disguiseProtocol.value) return ''
    if (disguiseUnknown.value) return '加载失败，请刷新页面'
    const { client } = disguiseClients[disguiseProtocol.value]
    return disguiseEnabled.value
      ? `${client} 伪装已启用：规范化客户端身份头，保留已有会话和协议能力字段`
      : `${client} 伪装已关闭：透明转发客户端身份头`
  })

  const toggleClientDisguise = async () => {
    const protocol = disguiseProtocol.value
    if (!protocol) return
    const next = !disguiseEnabled.value
    disguiseLoading.value = true
    try {
      await api.setClientDisguise(protocol, next)
      preferencesStore.setClientDisguise(protocol, next)
      notify(`${disguiseClients[protocol].client} 伪装已${next ? '启用' : '关闭'}`, 'success')
    } catch (error) {
      notify(`切换客户端伪装失败: ${errorText(error)}`, 'error')
    } finally {
      disguiseLoading.value = false
    }
  }

  return {
    loadModes,
    fuzzyLoading, fuzzyUnknown, fuzzyTooltip, toggleFuzzyMode,
    disguiseProtocol, disguiseEnabled, disguiseLabel, disguiseTooltip,
    disguiseLoading, disguiseUnknown, toggleClientDisguise
  }
}
