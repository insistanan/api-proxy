// 渠道页按协议独立管理的自动故障转移（Fuzzy）与客户端伪装开关。
// 开关本体保存在服务端，preferences store 只镜像最近一次已知状态供多处渲染；
// 加载失败时保持“未知”并禁止切换，不用本地缓存冒充服务端配置。
import { computed, ref } from 'vue'
import { api, ApiError } from '@/services/api'
import { API_TABS, type ApiTab } from '@/services/channel-api'
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

  const fuzzySaving = ref<Partial<Record<ApiTab, boolean>>>({})
  const fuzzyLoaded = ref(false)
  const fuzzyLoading = computed(() => !!fuzzySaving.value[channelStore.activeTab])
  const fuzzyUnknown = computed(() => !fuzzyLoaded.value || preferencesStore.fuzzyModeByProtocol[channelStore.activeTab] === undefined)
  const fuzzyEnabled = computed(() => preferencesStore.fuzzyModeByProtocol[channelStore.activeTab] === true)
  const disguiseLoading = ref(false)
  const disguiseUnknown = ref(false)

  const loadFuzzyMode = async () => {
    try {
      const { fuzzyModeByProtocol } = await api.getFuzzyMode()
      if (API_TABS.some(protocol => typeof fuzzyModeByProtocol?.[protocol] !== 'boolean')) {
        throw new Error('服务端未返回完整的协议故障转移状态')
      }
      for (const protocol of API_TABS) preferencesStore.setFuzzyMode(protocol, fuzzyModeByProtocol[protocol])
      fuzzyLoaded.value = true
    } catch (error) {
      fuzzyLoaded.value = false
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
    if (fuzzyUnknown.value || fuzzyLoading.value) return
    const protocol = channelStore.activeTab
    const next = !fuzzyEnabled.value
    fuzzySaving.value[protocol] = true
    try {
      await api.setFuzzyMode(protocol, next)
      preferencesStore.setFuzzyMode(protocol, next)
      notify(`${protocol} 自动故障转移已${next ? '启用' : '关闭'}`, 'success')
    } catch (error) {
      if (error instanceof ApiError && error.committed) await loadFuzzyMode()
      notify(`切换自动故障转移失败: ${errorText(error)}`, 'error')
    } finally {
      fuzzySaving.value[protocol] = false
    }
  }

  const fuzzyTooltip = computed(() => {
    if (fuzzyUnknown.value) return '加载失败，请刷新页面'
    return fuzzyEnabled.value
      ? '当前协议已启用故障转移：请求失败时按分组规则尝试后续渠道'
      : '当前协议已关闭故障转移：固定首个渠道，仅重试其 Key 与地址'
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
    fuzzyEnabled, fuzzyLoading, fuzzyUnknown, fuzzyTooltip, toggleFuzzyMode,
    disguiseProtocol, disguiseEnabled, disguiseLabel, disguiseTooltip,
    disguiseLoading, disguiseUnknown, toggleClientDisguise
  }
}
