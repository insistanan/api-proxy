// useChannelActivity 渠道实时活跃度可视化（R5，方案 8.1：
// ChannelOrchestration 的活跃度数据处理收敛到 composable）。
// 数据本体（recentActivity）由父级持有传入；本文件负责
// 活跃度索引、历史峰值与波形柱的派生计算。
import { computed, ref, watch, type Ref } from 'vue'
import type { ChannelRecentActivity } from '@/services/api'

/** 波形柱的健康档位，与成功率颜色对应。 */
export type ActivityLevel = 'idle' | 'ok' | 'warn' | 'bad'

export type ActivityBar = {
  x: number
  y: number
  width: number
  height: number
  radius: number
  color: string
  level: ActivityLevel
  gradientKey: string
  latest: boolean
}

/**
 * @param recentActivity 父级持有的活跃度数据（每渠道一列）。
 */
export function useChannelActivity(recentActivity: Ref<ChannelRecentActivity[]>) {
  // 活跃度数据 Map 缓存（避免线性查找）
  const activityMap = computed(() => {
    const map = new Map<number, ChannelRecentActivity>()
    for (const a of recentActivity.value) {
      map.set(a.channelIndex, a)
    }
    return map
  })

  // 每个渠道的历史最大请求数（用于固定柱状图高度比例）
  const maxRequestsHistory = ref(new Map<number, number>())

  // 更新历史最大值（只在当前最大值更大时更新，保持历史峰值）
  watch(activityMap, newMap => {
    for (const channelIndex of maxRequestsHistory.value.keys()) {
      if (!newMap.has(channelIndex)) maxRequestsHistory.value.delete(channelIndex)
    }
    for (const [channelIndex, activity] of newMap.entries()) {
      if (!activity.segments || activity.segments.length === 0) continue

      const currentMax = Math.max(...activity.segments.map(s => s.requestCount), 0)
      const historicalMax = maxRequestsHistory.value.get(channelIndex) ?? 0

      if (currentMax > historicalMax) {
        maxRequestsHistory.value.set(channelIndex, currentMax)
      }
    }
  }, { immediate: true })

  // 获取渠道的活跃度数据
  const getChannelActivity = (channelIndex: number): ChannelRecentActivity | undefined => {
    return activityMap.value.get(channelIndex)
  }

  // 缓存所有渠道的柱状图数据（避免在模板中重复计算）
  const activityBarsCache = computed<Map<number, ActivityBar[]>>(previous => {
    const cache = new Map<number, ActivityBar[]>()
    const storeBars = (channelIndex: number, bars: ActivityBar[]) => {
      const oldBars = previous?.get(channelIndex)
      const unchanged = oldBars && oldBars.length === bars.length && bars.every((bar, index) => {
        const old = oldBars[index]
        return bar.x === old.x && bar.y === old.y && bar.width === old.width && bar.height === old.height &&
          bar.radius === old.radius && bar.color === old.color && bar.level === old.level &&
          bar.gradientKey === old.gradientKey && bar.latest === old.latest
      })
      // 保留未变化的派生数组，让波形组件跳过与自身无关的指标刷新。
      cache.set(channelIndex, oldBars && unchanged ? oldBars : bars)
    }

    for (const [channelIndex, activity] of activityMap.value.entries()) {
      if (!activity || !activity.segments || activity.segments.length === 0) {
        storeBars(channelIndex, [])
        continue
      }

      const segments = activity.segments
      const numSegments = segments.length // 150（后端已聚合为每 6 秒一段）

      // 每个段一个柱子
      const barWidth = 150 / numSegments
      const barGap = barWidth * 0.2 // 20% 间隙
      const actualBarWidth = barWidth - barGap

      // 使用历史最大值作为归一化基准（避免高流量段离开后柱子突然变高）
      const maxRequests =
        maxRequestsHistory.value.get(channelIndex) ?? Math.max(...segments.map(s => s.requestCount), 1)

      const bars: ActivityBar[] = []

      for (let i = 0; i < numSegments; i++) {
        const segment = segments[i]
        const requests = segment.requestCount
        // 空段仍通过原始下标保留时间位置，但不生成不可见的 SVG 节点。
        if (requests === 0) continue

        // 计算柱子高度（最小高度 2，避免完全消失）
        const heightPercent = requests / maxRequests
        const height = Math.max(heightPercent * 85, requests > 0 ? 2 : 0)
        const y = 100 - height

        // 根据该 6 秒段的成功率计算颜色（7 档分级：极端档位 + 整数档位）
        let color = 'rgb(52, 211, 153)' // 默认绿色（无请求或 100% 成功）
        let level: ActivityLevel = 'idle'

        if (requests > 0) {
          const successCount = requests - segment.failureCount
          const successRate = (successCount / requests) * 100

          if (successRate < 5) {
            color = 'rgb(220, 38, 38)' // 0-5%：深红色（极端故障）
            level = 'bad'
          } else if (successRate < 20) {
            color = 'rgb(248, 113, 113)' // 5-20%：红色（严重失败）
            level = 'bad'
          } else if (successRate < 40) {
            color = 'rgb(251, 146, 60)' // 20-40%：深橙色（高失败率）
            level = 'bad'
          } else if (successRate < 60) {
            color = 'rgb(250, 204, 21)' // 40-60%：黄色（中等失败率）
            level = 'warn'
          } else if (successRate < 80) {
            color = 'rgb(163, 230, 53)' // 60-80%：黄绿色（轻微失败）
            level = 'warn'
          } else if (successRate < 95) {
            color = 'rgb(74, 222, 128)' // 80-95%：亮绿色（良好）
            level = 'ok'
          } else {
            color = 'rgb(52, 211, 153)' // 95-100%：翠绿色（优秀）
            level = 'ok'
          }
        }

        bars.push({
          x: i * barWidth + barGap / 2,
          y,
          width: actualBarWidth,
          height,
          radius: Math.min(actualBarWidth / 2, 1.5),
          color,
          level,
          gradientKey: color.replace(/[^0-9]+/g, '-'),
          latest: i === numSegments - 1
        })
      }

      storeBars(channelIndex, bars)
    }

    return cache
  })

  // 生成波形柱状图数据（从缓存中读取）
  const getActivityBars = (channelIndex: number): ActivityBar[] => {
    return activityBarsCache.value.get(channelIndex) ?? []
  }

  // 格式化 RPM 显示
  const formatRPM = (channelIndex: number): string => {
    const activity = getChannelActivity(channelIndex)
    if (!activity || activity.rpm === 0) return '--'
    if (activity.rpm >= 10) return activity.rpm.toFixed(0)
    return activity.rpm.toFixed(1)
  }

  // 格式化 TPM 显示
  const formatTPM = (channelIndex: number): string => {
    const activity = getChannelActivity(channelIndex)
    if (!activity || activity.tpm === 0) return '--'
    if (activity.tpm >= 1000000) return `${(activity.tpm / 1000000).toFixed(1)}M`
    if (activity.tpm >= 1000) return `${(activity.tpm / 1000).toFixed(1)}K`
    return activity.tpm.toFixed(0)
  }

  // 判断渠道是否有活跃度数据
  const hasActivityData = (channelIndex: number): boolean => {
    const activity = getChannelActivity(channelIndex)
    if (!activity) return false
    return activity.rpm > 0 || activity.tpm > 0
  }

  return {
    getActivityBars,
    formatRPM,
    formatTPM,
    hasActivityData
  }
}
