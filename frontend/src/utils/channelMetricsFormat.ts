// channelMetricsFormat 渠道指标格式化与等级判定（R5，方案 8.1：
// ChannelOrchestration 的展示辅助逻辑收敛）。
// 纯函数：输入指标输出展示文本/等级，不持有状态。
import type { Channel, TimeWindowStats } from '@/services/api'

/** 缓存命中率等级（用于着色）。 */
export function getCacheRateLevel(rate?: number): string {
  if (rate === undefined || rate === null || !Number.isFinite(rate)) return 'unknown'
  if (rate >= 50) return 'high'
  if (rate >= 20) return 'medium'
  return 'low'
}

/** 成功率等级（用于着色）。 */
export function getRateLevel(rate?: number): string {
  if (rate === undefined || rate === null) return 'unknown'
  if (rate >= 90) return 'high'
  if (rate >= 70) return 'medium'
  return 'low'
}

/** 失败率（%）：由成功率折算，缺数据时返回 undefined。 */
export function getFailureRate(stats?: TimeWindowStats): number | undefined {
  if (!stats || !stats.requestCount) return undefined
  return (stats.failureCount / stats.requestCount) * 100
}

/** 缓存命中率（%）：优先后端 cacheHitRate，缺省按 token 折算。 */
export function getCacheHitRate(stats?: TimeWindowStats): number | undefined {
  if (!stats) return undefined
  const inputTokens = stats.inputTokens ?? 0
  const cacheReadTokens = stats.cacheReadTokens ?? 0
  const denom = inputTokens + cacheReadTokens

  if (denom <= 0) return undefined
  return stats.cacheHitRate ?? (cacheReadTokens / denom) * 100
}

/** 是否有足够数据展示缓存命中率。 */
export function shouldShowCacheHitRate(stats?: TimeWindowStats): boolean {
  if (!stats || !stats.requestCount) return false
  return getCacheHitRate(stats) !== undefined
}

/** 延迟着色。 */
export function getLatencyColor(latency: number): string {
  if (latency < 500) return 'success'
  if (latency < 1000) return 'warning'
  return 'error'
}

/** 延迟测试结果有效期（5 分钟）。 */
export const LATENCY_VALID_DURATION = 5 * 60 * 1000

/** 判断延迟测试结果是否仍在有效期内（5 分钟）。 */
export function isLatencyValid(channel: Channel, now: number): boolean {
  if (channel.latency === undefined || channel.latency === null) return false
  if (!channel.latencyTestTime) return false
  return now - channel.latencyTestTime < LATENCY_VALID_DURATION
}

/** 判断渠道是否处于促销期。 */
export function isInPromotion(channel: Channel): boolean {
  if (channel.promotionCount && channel.promotionCount > 0) return true
  if (!channel.promotionUntil) return false
  return new Date(channel.promotionUntil) > new Date()
}

/** 格式化促销期剩余时间。 */
export function formatPromotionRemaining(until?: string, count?: number): string {
  if (count && count > 0) return `剩 ${count} 次`
  if (!until) return ''
  const remaining = Math.max(0, new Date(until).getTime() - Date.now())
  const minutes = Math.ceil(remaining / 60000)
  if (minutes <= 0) return '即将结束'
  return `${minutes}分钟`
}

/** 格式化统计数据：有请求显示 "N 请求 (成功 X / 失败 Y)"，无请求显示 "--"。 */
export function formatStats(stats?: TimeWindowStats): string {
  if (!stats || !stats.requestCount) return '--'
  return `${stats.requestCount} 请求 (成功 ${stats.successCount} / 失败 ${stats.failureCount})`
}

/** Token 数量缩写（K/M）。 */
export function formatTokens(num?: number): string {
  const value = num ?? 0
  if (value >= 1000000) return `${(value / 1000000).toFixed(1)}M`
  if (value >= 1000) return `${(value / 1000).toFixed(1)}K`
  return Math.round(value).toString()
}

/** 缓存统计一行文本。 */
export function formatCacheStats(stats?: TimeWindowStats): string {
  if (!stats || !stats.requestCount) return '--'

  const cacheReadTokens = stats.cacheReadTokens ?? 0
  const cacheCreationTokens = stats.cacheCreationTokens ?? 0

  const hitRate = getCacheHitRate(stats)
  if (hitRate === undefined) return '--'
  return `命中 ${hitRate.toFixed(0)}% · 读 ${formatTokens(cacheReadTokens)} · 写 ${formatTokens(cacheCreationTokens)}`
}

/** 官网 URL：优先 website 字段，否则从 baseUrl 提取协议+域名。 */
export function getWebsiteUrl(channel: Channel): string {
  if (channel.website) return channel.website
  try {
    const url = new URL(channel.baseUrl)
    return `${url.protocol}//${url.host}`
  } catch {
    return channel.baseUrl
  }
}
