import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Channel } from '@/api'
import { formatPromotionRemaining, getWebsiteUrl, isInPromotion } from '@/features/channels/utils/channelMetricsFormat'

const channel: Channel = { index: 0, name: 'test', serviceType: 'openai', baseUrl: '', apiKeys: [] }
beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date('2026-01-01T00:00:00Z')) })
afterEach(() => { vi.useRealTimers() })

describe('促销展示与结束规则', () => {
  it('时间过期后剩余次数不能继续促销', () => {
    expect(isInPromotion({ ...channel, promotionUntil: '2025-12-31T23:59:59Z', promotionCount: 5 })).toBe(false)
  })
  it('同时展示两项剩余限制', () => {
    expect(formatPromotionRemaining('2026-01-01T00:05:00Z', 2)).toBe('5分钟 / 剩 2 次')
  })
})

describe('渠道网站入口', () => {
  it('优先使用官网并保留页面路径，未填写时只打开上游站点', () => {
    expect(getWebsiteUrl({ ...channel, website: 'https://console.example.com/billing', baseUrl: 'https://api.example.com/v1' })).toBe('https://console.example.com/billing')
    expect(getWebsiteUrl({ ...channel, baseUrl: 'https://api.example.com:8443/v1?key=fixture' })).toBe('https://api.example.com:8443')
  })
  it.each(['javascript:alert(1)', 'data:text/html,test', '无效地址'])('不为无效网站生成可点击地址：%s', website => {
    expect(getWebsiteUrl({ ...channel, website })).toBe('')
  })
  it('网站地址不带 HTTP 用户凭据', () => {
    expect(getWebsiteUrl({ ...channel, website: 'https://user:password@example.com/home' })).toBe('https://example.com/home')
  })
})
