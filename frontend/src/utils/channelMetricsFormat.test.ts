import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Channel } from '@/services/api'
import { formatPromotionRemaining, isInPromotion } from './channelMetricsFormat'

const channel: Channel = { index: 0, name: 'test', serviceType: 'openai', baseUrl: '', apiKeys: [] }
beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date('2026-01-01T00:00:00Z')) })
afterEach(() => { vi.useRealTimers() })

describe('促销展示与结束规则', () => {
  it('时间过期后剩余次数不能继续促销', () => {
    expect(isInPromotion({ ...channel, promotionUntil: '2025-12-31T23:59:59Z', promotionCount: 5 })).toBe(false)
  })
  it('只限时长与只限次数都可以促销', () => {
    expect(isInPromotion({ ...channel, promotionUntil: '2026-01-01T00:05:00Z', promotionCount: 0 })).toBe(true)
    expect(isInPromotion({ ...channel, promotionCount: 2 })).toBe(true)
    expect(isInPromotion({ ...channel, promotionCount: 0 })).toBe(false)
  })
  it('同时展示两项剩余限制', () => {
    expect(formatPromotionRemaining('2026-01-01T00:05:00Z', 2)).toBe('5分钟 / 剩 2 次')
  })
})
