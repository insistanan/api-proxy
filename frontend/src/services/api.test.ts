import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { channelApiByType } from './api'

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))

beforeEach(() => { setActivePinia(createPinia()) })
afterEach(() => { vi.unstubAllGlobals() })

describe('促销输入校验', () => {
  it.each([-1, 1.5, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1])('非法限制 %s 不发送请求', value => {
    const fetch = vi.fn()
    vi.stubGlobal('fetch', fetch)
    expect(() => channelApiByType('messages').setPromotion(0, value, 1)).toThrow('非负整数')
    expect(() => channelApiByType('messages').setPromotion(0, 60, value)).toThrow('非负整数')
    expect(fetch).not.toHaveBeenCalled()
  })
  it('同时发送时间与次数，省略次数则表示不限次数', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }))
    vi.stubGlobal('fetch', fetch)
    await channelApiByType('responses').setPromotion(0, 300, 2)
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ duration: 300, count: 2 })
    fetch.mockResolvedValue(new Response('{}', { status: 200 }))
    await channelApiByType('messages').setPromotion(0, 300)
    expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({ duration: 300, count: 0 })
  })
})
