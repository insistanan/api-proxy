import { defineStore } from 'pinia'
import { ref, shallowRef, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api, ApiError, API_TABS, channelApiByType, type ApiTab, type Channel, type ChannelsResponse, type ChannelMetrics, type ChannelDashboardResponse } from '@/services/api'

// Dashboard 是只读响应快照，整体替换；不为大量统计段创建深层响应式代理。
interface DashboardCache {
  metrics: ChannelMetrics[]
  recentActivity: ChannelDashboardResponse['recentActivity'] | undefined
}

const perTab = <T>(create: () => T) =>
  Object.fromEntries(API_TABS.map(tab => [tab, create()])) as Record<ApiTab, T>
const emptyChannels = (): ChannelsResponse => ({ channels: [], current: -1, loadBalance: 'failover' })
const emptyDashboard = (): DashboardCache => ({ metrics: [], recentActivity: undefined })

/**
 * 渠道数据管理 Store
 *
 * 职责：
 * - 管理各 API 类型的渠道数据
 * - 管理渠道指标和统计数据
 * - 提供渠道操作方法（添加、编辑、删除、测试延迟等）
 * - 管理自动刷新定时器
 */
export const useChannelStore = defineStore('channel', () => {
  // ===== 状态 =====

  // 当前协议跟随路由参数；非法参数回落到 Messages。
  const router = useRouter()
  const activeTab = ref<ApiTab>('messages')
  watch(() => router.currentRoute.value.params.type, type => {
    activeTab.value = API_TABS.find(tab => tab === type) ?? 'messages'
  }, { immediate: true })

  // 各协议的渠道配置与 Dashboard 快照分别缓存，切换协议时不闪烁。
  const channelsDataMap = ref(perTab(emptyChannels))
  const dashboardCache = shallowRef(perTab(emptyDashboard))

  // 批量延迟测试加载状态
  const isPingingAll = ref(false)
  const isSavingChannel = ref(false)
  const isDeletingChannel = ref(false)

  // 最近一次刷新是否成功；null 表示尚未完成首次刷新。
  const lastRefreshSuccess = ref<boolean | null>(null)

  // 自动刷新定时器（串行 setTimeout，避免重入）
  let autoRefreshTimer: ReturnType<typeof setTimeout> | null = null
  let autoRefreshRunning = false
  const AUTO_REFRESH_INTERVAL = 2000 // 2秒

  // 刷新按发起协议排队；每次调用仅接收自己协议的结果，某项失败不阻断后续刷新。
  let refreshLoopPromise: Promise<void> | null = null
  const queuedRefreshes = new Map<ApiTab, Promise<void>>()

  // ===== 计算属性 =====

  // 根据当前 Tab 返回对应的渠道数据
  const currentChannelsData = computed(() => channelsDataMap.value[activeTab.value])

  // 各类型对应的统一 API 方法集（收敛：store 内不再散落各协议命名方法）
  const tabApi = (tab: ApiTab = activeTab.value) => channelApiByType(tab)

  // 根据当前 Tab 返回对应的 Dashboard 数据（独立缓存，避免切换闪烁）
  const currentDashboardMetrics = computed(() => dashboardCache.value[activeTab.value].metrics)
  const currentDashboardRecentActivity = computed(() => dashboardCache.value[activeTab.value].recentActivity)

  // 活跃渠道数（仅 active 状态）
  const activeChannelCount = computed(() => {
    return currentChannelsData.value.channels.filter(ch => !ch.excludeFromConversation && (ch.status === 'active' || ch.status === undefined || ch.status === '')).length
  })

  // 参与故障转移的渠道数（active + suspended）
  const failoverChannelCount = computed(() => {
    return currentChannelsData.value.channels.filter(ch => !ch.excludeFromConversation && ch.status !== 'disabled' && ch.status !== 'deprecated' && ch.status !== 'deleted').length
  })

  // ===== 辅助方法 =====

  // 合并渠道数据，保留本地的延迟测试结果
  const LATENCY_VALID_DURATION = 5 * 60 * 1000 // 5 分钟有效期

  function mergeChannelsWithLocalData(newChannels: Channel[], existingChannels: Channel[] | undefined): Channel[] {
    if (!existingChannels) return newChannels

    const now = Date.now()
    const existingByID = new Map(existingChannels.filter(channel => channel.id).map(channel => [channel.id, channel]))
    const mergedChannels = newChannels.map(newCh => {
      const existingCh = newCh.id ? existingByID.get(newCh.id) : undefined
      let merged = newCh
      // 只有在 5 分钟有效期内才保留本地延迟测试结果
      if (existingCh?.latencyTestTime && (now - existingCh.latencyTestTime) < LATENCY_VALID_DURATION) {
        merged = {
          ...newCh,
          latency: existingCh.latency,
          latencyTestTime: existingCh.latencyTestTime
        }
      }
      // 渠道是 JSON 配置；内容未变时复用对象，避免统计刷新触发全部渠道表单和菜单更新。
      return existingCh && JSON.stringify(merged) === JSON.stringify(existingCh) ? existingCh : merged
    })
    return mergedChannels.length === existingChannels.length && mergedChannels.every((channel, index) => channel === existingChannels[index])
      ? existingChannels
      : mergedChannels
  }

  // ===== 操作方法 =====

  /**
   * 刷新渠道数据
   */
  async function refreshChannels(tab: ApiTab = activeTab.value) {
    const queued = queuedRefreshes.get(tab)
    if (queued) return queued

    const doRefresh = async (tab: ApiTab) => {
      try {
        const dashboard = await api.getChannelDashboard(tab)

        const previous = channelsDataMap.value[tab]
        const channels = mergeChannelsWithLocalData(dashboard.channels, previous.channels)
        if (channels !== previous.channels || dashboard.loadBalance !== previous.loadBalance) {
          channelsDataMap.value[tab] = {
            channels,
            current: previous.current, // 保留当前选中状态
            loadBalance: dashboard.loadBalance
          }
        }
        // shallowRef 必须替换根对象；其余协议继续复用各自快照。
        dashboardCache.value = {
          ...dashboardCache.value,
          [tab]: { metrics: dashboard.metrics, recentActivity: dashboard.recentActivity }
        }

        lastRefreshSuccess.value = true
      } catch (error) {
        lastRefreshSuccess.value = false
        throw error
      }
    }

    const runRefresh = () => {
      queuedRefreshes.delete(tab)
      return doRefresh(tab)
    }
    // 前项成功或失败都继续排队，但失败仍由前项自己的调用方接收。
    const request = (refreshLoopPromise ?? Promise.resolve()).then(runRefresh, runRefresh)
    const completion = request.finally(() => {
      if (refreshLoopPromise === completion) refreshLoopPromise = null
    })
    queuedRefreshes.set(tab, completion)
    refreshLoopPromise = completion
    return completion
  }

  /**
   * 保存渠道（添加或更新）
   */
  async function saveChannel(
    channel: Omit<Channel, 'id' | 'index' | 'latency'>,
    editingChannelIndex: number | null,
    options?: { isQuickAdd?: boolean },
    tab: ApiTab = activeTab.value
  ): Promise<{ message: string; quickAddMessage?: string; warning?: string }> {
    const ca = tabApi(tab)

    if (editingChannelIndex !== null) {
      // 更新现有渠道
      await ca.updateChannel(editingChannelIndex, channel)
      return { message: '渠道更新成功' }
    } else {
      // 添加新渠道
      const createdChannel = await ca.addChannel(channel)

      // 创建与促销是两个提交点，后一步失败不能让用户重复创建渠道。
      if (options?.isQuickAdd) {
        try {
          const channelIndex = createdChannel.channel.index
          await ca.setPromotion(channelIndex, 300, 0)

          return {
            message: '渠道添加成功',
            quickAddMessage: `渠道 ${channel.name} 已设置促销（5分钟内优先使用，仍受健康检查与熔断限制）`
          }
        } catch (err) {
          const message = err instanceof Error ? err.message : '未知错误'
          const detail = err instanceof ApiError && err.committed ? '促销已写入，但收尾失败' : '促销设置失败'
          return { message: '渠道添加成功', warning: `${detail}: ${message}。渠道已创建，请勿重复添加` }
        }
      }

      return { message: '渠道添加成功' }
    }
  }

  /**
   * 删除渠道
   */
  async function deleteChannel(channelId: number, tab: ApiTab = activeTab.value) {
    await tabApi(tab).deleteChannel(channelId)
    return { success: true, message: '渠道删除成功' }
  }

  /**
   * 测试单个渠道延迟
   */
  async function pingChannel(channelId: number) {
    const tab = activeTab.value
    const targetId = channelsDataMap.value[tab].channels.find(c => c.index === channelId)?.id
    const result = await tabApi(tab).pingChannel(channelId)

    const channel = targetId === undefined ? undefined : channelsDataMap.value[tab].channels.find(c => c.id === targetId)
    if (channel) {
      channel.latency = result.latency
      channel.latencyTestTime = Date.now()
    }

    return { success: true }
  }

  /**
   * 批量测试所有渠道延迟
   */
  async function pingAllChannels() {
    if (isPingingAll.value) return { success: false, message: '正在测试中' }

    const tab = activeTab.value
    const targets = new Map(channelsDataMap.value[tab].channels.map(c => [c.index, c.id]))
    isPingingAll.value = true
    try {
      const results = await tabApi(tab).pingAllChannels()

      const now = Date.now()
      results.forEach(result => {
        const targetId = targets.get(result.id)
        const channel = targetId === undefined ? undefined : channelsDataMap.value[tab].channels.find(c => c.id === targetId)
        if (channel) {
          channel.latency = result.latency
          channel.latencyTestTime = now  // 记录测试时间，用于 5 分钟后清除
        }
      })

      return { success: true }
    } finally {
      isPingingAll.value = false
    }
  }

  /**
   * 启动自动刷新定时器
   */
  function startAutoRefresh() {
    stopAutoRefresh()
    autoRefreshRunning = true

    const tick = async () => {
      if (!autoRefreshRunning) return
      try {
        // 看板隐藏到托盘或浏览器后台时暂停刷新，代理和危险命令通知仍独立运行。
        if (typeof document === 'undefined' || !document.hidden) await refreshChannels()
      } catch (error) {
        console.warn('自动刷新失败:', error)
      } finally {
        if (autoRefreshRunning) {
          autoRefreshTimer = setTimeout(() => {
            void tick()
          }, AUTO_REFRESH_INTERVAL)
        }
      }
    }

    autoRefreshTimer = setTimeout(() => {
      void tick()
    }, AUTO_REFRESH_INTERVAL)
  }

  /**
   * 停止自动刷新定时器
   */
  function stopAutoRefresh() {
    autoRefreshRunning = false
    if (!autoRefreshTimer) return
    clearTimeout(autoRefreshTimer)
    autoRefreshTimer = null
  }

  /**
   * 清空所有渠道数据（用于注销）
   */
  function clearChannels() {
    channelsDataMap.value = perTab(emptyChannels)
    dashboardCache.value = perTab(emptyDashboard)
    lastRefreshSuccess.value = null
    isPingingAll.value = false
  }

  // ===== 返回公开接口 =====
  return {
    // 状态
    activeTab,
    channelsDataMap,
    isPingingAll,
    isSavingChannel,
    isDeletingChannel,
    lastRefreshSuccess,

    // 计算属性
    currentChannelsData,
    currentDashboardMetrics,
    currentDashboardRecentActivity,
    activeChannelCount,
    failoverChannelCount,

    // 方法
    refreshChannels,
    saveChannel,
    deleteChannel,
    pingChannel,
    pingAllChannels,
    startAutoRefresh,
    stopAutoRefresh,
    clearChannels,
  }
})
