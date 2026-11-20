import type { RequestStatsSummary } from '@/types/metrics'

const compactFormatter = new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 })
const numberFormatter = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 })

export const formatCompactNumber = (value?: number): string => compactFormatter.format(value || 0)
export const formatStatNumber = (value?: number): string => numberFormatter.format(value || 0)

export function formatStatMs(value?: number): string {
  if (!value || value <= 0) return '--'
  if (value < 1000) return `${Math.round(value)} ms`
  if (value < 60000) return `${(value / 1000).toFixed(value < 10000 ? 2 : 1)} s`
  return `${(value / 60000).toFixed(1)} min`
}

export const formatStatPercent = (value?: number): string => `${(value || 0).toFixed(1)}%`

export interface MetricComparison {
  text: string
  direction: 'up' | 'down' | 'flat' | 'unknown'
}

/** 只比较真实上一周期；零基线不计算伪无限增幅。 */
export function compareMetric(summary: RequestStatsSummary | undefined, previous: RequestStatsSummary | undefined, key: keyof RequestStatsSummary, points = false): MetricComparison {
  if (!summary || !previous) return { text: '暂无上一周期数据', direction: 'unknown' }
  const current = Number(summary[key] || 0)
  const prior = Number(previous[key] || 0)
  const difference = current - prior
  if (points) {
    return {
      text: `较上一周期 ${difference >= 0 ? '+' : ''}${difference.toFixed(1)} 个百分点`,
      direction: difference > 0 ? 'up' : difference < 0 ? 'down' : 'flat'
    }
  }
  if (prior === 0) {
    if (current === 0) return { text: '与上一周期持平', direction: 'flat' }
    return { text: '上一周期为 0', direction: 'up' }
  }
  const percent = difference / Math.abs(prior) * 100
  return {
    text: `较上一周期 ${percent >= 0 ? '+' : ''}${percent.toFixed(1)}%`,
    direction: percent > 0 ? 'up' : percent < 0 ? 'down' : 'flat'
  }
}
