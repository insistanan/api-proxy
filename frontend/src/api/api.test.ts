import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { api, channelApiByType } from '@/api'

vi.mock('@/shared/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))

beforeEach(() => { setActivePinia(createPinia()) })
afterEach(() => { vi.unstubAllGlobals() })

describe('调用聚合 API', () => {
  it('传递服务端筛选和游标，尝试接口只携带调用标识', async () => {
    const fetch = vi.fn().mockImplementation(() => Promise.resolve(new Response('{"calls":[]}', { status: 200 })))
    vi.stubGlobal('fetch', fetch)
    await api.getLogCalls({ type: 'responses', q: '日志_100%', status: 'failed', from: '2026-11-01T00:00:00Z', cursor: 'cursor+value', limit: 50 })
    const params = new URL(String(fetch.mock.calls[0][0]), 'http://localhost').searchParams
    expect(params.get('q')).toBe('日志_100%')
    expect(params.get('status')).toBe('failed')
    expect(params.get('cursor')).toBe('cursor+value')
    expect(params.get('from')).toBe('2026-11-01T00:00:00Z')
    await api.getLogCallAttempts('request/id+value')
    expect(fetch.mock.calls[1][0]).toBe('/api/log-call-attempts?requestId=request%2Fid%2Bvalue')
  })
})

describe('本代理模型目录', () => {
  it('向后端传递入口协议，省略时保留全量目录', async () => {
    const fetch = vi.fn().mockImplementation(() => Promise.resolve(new Response('{"object":"list","data":[]}', { status: 200 })))
    vi.stubGlobal('fetch', fetch)
    await api.getProxyModels('messages')
    expect(fetch.mock.calls[0][0]).toBe('/api/proxy-models?type=messages')
    await api.getProxyModels('responses')
    expect(fetch.mock.calls[1][0]).toBe('/api/proxy-models?type=responses')
    await api.getProxyModels()
    expect(fetch.mock.calls[2][0]).toBe('/api/proxy-models')
  })
})

it('故障转移设置只发送指定协议的开关', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }))
  vi.stubGlobal('fetch', fetch)
  await api.setFuzzyMode('responses', false)
  expect(fetch.mock.calls[0][0]).toBe('/api/settings/fuzzy-mode')
  expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ protocol: 'responses', enabled: false })
})

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

describe('扩展与 Skill 同步 API', () => {
  it('正确调用 Skill Agent 开关、全量同步与 Git 同步端点', async () => {
    const fetch = vi.fn().mockImplementation(() => Promise.resolve(new Response('{}', { status: 200 })))
    vi.stubGlobal('fetch', fetch)

    await api.toggleSkillAgent('demo-skill', 'claude-code', true)
    expect(fetch.mock.calls[0][0]).toBe('/api/skills/toggle')
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ name: 'demo-skill', agent: 'claude-code', enabled: true })

    await api.syncSkills()
    expect(fetch.mock.calls[1][0]).toBe('/api/skills/sync')
    expect(fetch.mock.calls[1][1].method).toBe('POST')

    await api.getExtensionsGitStatus()
    expect(fetch.mock.calls[2][0]).toBe('/api/extensions/git/status')

    await api.initExtensionsGit('git@github.com:org/repo.git')
    expect(fetch.mock.calls[3][0]).toBe('/api/extensions/git/init')
    expect(JSON.parse(fetch.mock.calls[3][1].body)).toEqual({ repoUrl: 'git@github.com:org/repo.git' })

    await api.cloneExtensionsGit('https://example.com/repo.git')
    expect(fetch.mock.calls[4][0]).toBe('/api/extensions/git/clone')
    expect(JSON.parse(fetch.mock.calls[4][1].body)).toEqual({ repoUrl: 'https://example.com/repo.git' })

    await api.pullExtensionsGit()
    expect(fetch.mock.calls[5][0]).toBe('/api/extensions/git/pull')
    expect(fetch.mock.calls[5][1].method).toBe('POST')

    await api.pushExtensionsGit('sync skills', 'preview-revision')
    expect(fetch.mock.calls[6][0]).toBe('/api/extensions/git/push')
    expect(JSON.parse(fetch.mock.calls[6][1].body)).toEqual({ message: 'sync skills', exportRevision: 'preview-revision' })
  })
})
