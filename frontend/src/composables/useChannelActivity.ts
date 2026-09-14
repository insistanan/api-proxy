// useChannelActivity 渠道实时活跃度可视化（R5，方案 8.1：
// ChannelOrchestration 的活跃度数据处理收敛到 composable）。
// 数据本体（recentActivity）由父级持有传入；本文件负责
// 活跃度索引、历史峰值、波形柱与平滑曲线的派生计算。
import { computed, ref, watch, type Ref } from 'vue'
import type { ChannelRecentActivity } from '@/services/api'

/** 波形柱的健康档位 — 驱动 CSS 动效强度（越差动得越急）。 */
export type ActivityLevel = 'idle' | 'ok' | 'warn' | 'bad'

export type ActivityBar = {
  x: number
  y: number
  width: number
  height: number
  radius: number
  color: string
  level: ActivityLevel
}

/** Catmull-Rom 样条转 SVG 贝塞尔路径。 */
export function catmullRomToPath(points: { x: number; y: number }[]): string {
  if (points.length < 2) return ''

  const path: string[] = []
  path.push(`M ${points[0].x} ${points[0].y}`)

  // 张力参数（0.3 = 较低张力，曲线更贴近原始点）
  const tension = 0.3

  for (let i = 0; i < points.length - 1; i++) {
    const p0 = points[Math.max(0, i - 1)]
    const p1 = points[i]
    const p2 = points[i + 1]
    const p3 = points[Math.min(points.length - 1, i + 2)]

    const cp1x = p1.x + ((p2.x - p0.x) / 6) * tension
    const cp1y = p1.y + ((p2.y - p0.y) / 6) * tension
    const cp2x = p2.x - ((p3.x - p1.x) / 6) * tension
    const cp2y = p2.y - ((p3.y - p1.y) / 6) * tension

    path.push(`C ${cp1x} ${cp1y}, ${cp2x} ${cp2y}, ${p2.x} ${p2.y}`)
  }

  return path.join(' ')
}

/**
 * @param recentActivity 父级持有的活跃度数据（每渠道一列）。
 * @param updateTick 父级的时间刻度 ref，触发曲线动画重算。
 */
export function useChannelActivity(
  recentActivity: Ref<ChannelRecentActivity[]>,
  updateTick: Ref<number>
) {
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
    for (const [channelIndex, activity] of newMap.entries()) {
      if (!activity.segments || activity.segments.length === 0) continue

      const currentMax = Math.max(...activity.segments.map(s => s.requestCount), 0)
      const historicalMax = maxRequestsHistory.value.get(channelIndex) ?? 0

      if (currentMax > historicalMax) {
        maxRequestsHistory.value.set(channelIndex, currentMax)
      }
    }
  })

  // 获取渠道的活跃度数据
  const getChannelActivity = (channelIndex: number): ChannelRecentActivity | undefined => {
    return activityMap.value.get(channelIndex)
  }

  // 缓存所有渠道的柱状图数据（避免在模板中重复计算）
  const activityBarsCache = computed(() => {
    const cache = new Map<number, ActivityBar[]>()

    // 使用 updateTick 触发响应式更新
    const _ = updateTick.value

    for (const [channelIndex, activity] of activityMap.value.entries()) {
      if (!activity || !activity.segments || activity.segments.length === 0) {
        cache.set(channelIndex, [])
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
          level
        })
      }

      cache.set(channelIndex, bars)
    }

    return cache
  })

  // 生成波形柱状图数据（从缓存中读取）
  const getActivityBars = (channelIndex: number): ActivityBar[] => {
    return activityBarsCache.value.get(channelIndex) ?? []
  }

  // 生成平滑曲线路径（使用移动平均 + Catmull-Rom 样条）
  const getActivityPath = (channelIndex: number): string => {
    const activity = getChannelActivity(channelIndex)
    if (!activity || !activity.segments || activity.segments.length === 0) return ''

    // 使用 updateTick 触发响应式更新
    const _ = updateTick.value

    const segments = activity.segments
    const numSegments = segments.length // 150（后端已聚合为每 6 秒一段）

    // 找到最大请求数用于归一化
    const maxRequests = Math.max(...segments.map(s => s.requestCount), 1)

    // 应用移动平均平滑数据（窗口大小 5 = 10 秒）
    const windowSize = 5
    const smoothedData: number[] = []

    for (let i = 0; i < numSegments; i++) {
      const start = Math.max(0, i - Math.floor(windowSize / 2))
      const end = Math.min(numSegments, i + Math.ceil(windowSize / 2))
      let sum = 0
      let count = 0

      for (let j = start; j < end; j++) {
        sum += segments[j].requestCount
        count++
      }

      smoothedData.push(count > 0 ? sum / count : 0)
    }

    // 生成平滑后的点
    const points: { x: number; y: number }[] = []
    for (let i = 0; i < numSegments; i++) {
      const x = i
      const y = 100 - (smoothedData[i] / maxRequests) * 85
      points.push({ x, y })
    }

    if (points.length < 2) return ''

    return catmullRomToPath(points)
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
    getChannelActivity,
    getActivityBars,
    getActivityPath,
    formatRPM,
    formatTPM,
    hasActivityData
  }
}
