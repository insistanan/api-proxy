import { describe, expect, it } from 'vitest'
import { compareMetric, formatStatMs } from './statsFormat'
import type { RequestStatsSummary } from '@/types/metrics'

const summary = (values: Partial<RequestStatsSummary>): RequestStatsSummary => ({
  totalRequests: 0, totalCalls: 0, totalSuccess: 0, totalFailure: 0, totalRetried: 0,
  retryRate: 0, totalInputTokens: 0, totalOutputTokens: 0, totalCacheCreationTokens: 0,
  totalCacheReadTokens: 0, avgSuccessRate: 0, cacheHitRate: 0, avgDurationMs: 0,
  p50DurationMs: 0, p95DurationMs: 0, maxDurationMs: 0, avgFirstTokenMs: 0,
  p50FirstTokenMs: 0, p95FirstTokenMs: 0, avgTps: 0, durationSamples: 0,
  firstTokenSamples: 0, duration: '', ...values
})

describe('statsFormat', () => {
  it('上一周期缺失或零基线时不生成伪百分比', () => {
    expect(compareMetric(summary({ totalRequests: 5 }), undefined, 'totalRequests').direction).toBe('unknown')
    expect(compareMetric(summary({ totalRequests: 5 }), summary({}), 'totalRequests').text).toBe('上一周期为 0')
    expect(compareMetric(summary({}), summary({}), 'totalRequests').direction).toBe('flat')
  })

  it('分别计算数量环比和百分点变化', () => {
    expect(compareMetric(summary({ totalRequests: 125 }), summary({ totalRequests: 100 }), 'totalRequests').text).toBe('较上一周期 +25.0%')
    expect(compareMetric(summary({ avgSuccessRate: 92 }), summary({ avgSuccessRate: 95 }), 'avgSuccessRate', true).text).toBe('较上一周期 -3.0 个百分点')
  })

  it('没有有效耗时样本时显示占位符', () => {
    expect(formatStatMs(0)).toBe('--')
    expect(formatStatMs(1250)).toBe('1.25 s')
  })
})
