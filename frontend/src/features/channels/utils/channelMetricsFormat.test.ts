import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Channel } from '@/api'
import { formatPromotionRemaining, getWebsiteUrl, isInPromotion } from '@/features/channels/utils/channelMetricsFormat'

const channel: Channel = { index: 0, name: 'test', serviceType: 'openai', baseUrl: '', apiKeys: [] }
beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date('2026-01-01T00:00:00Z')) })
afterEach(() => vi.useRealTimers())

describe('渠道促销与网站展示', () => {
  it('正确展示剩余条件并在促销时间过期后结束', () => {
    expect(isInPromotion({ ...channel, promotionUntil: '2025-12-31T23:59:59Z', promotionCount: 5 })).toBe(false)
    expect(formatPromotionRemaining('2026-01-01T00:05:00Z', 2)).toBe('5分钟 / 剩 2 次')
  })

  it('优先展示官网、净化凭据并拒绝危险或无效网站', () => {
    expect(getWebsiteUrl({ ...channel, website: 'https://console.example.com/billing', baseUrl: 'https://api.example.com/v1' })).toBe('https://console.example.com/billing')
    expect(getWebsiteUrl({ ...channel, baseUrl: 'https://api.example.com:8443/v1?key=fixture' })).toBe('https://api.example.com:8443')
    expect(getWebsiteUrl({ ...channel, website: 'https://user:password@example.com/home' })).toBe('https://example.com/home')
    for (const website of ['javascript:alert(1)', 'data:text/html,test', '无效地址']) {
      expect(getWebsiteUrl({ ...channel, website }), website).toBe('')
    }
  })
})
