import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore } from '@/shared/stores/auth'
import { apiRequest } from '@/shared/services/http'

beforeEach(() => {
  setActivePinia(createPinia())
  const entries = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => entries.get(key) ?? null,
    setItem: (key: string, value: string) => entries.set(key, value),
    removeItem: (key: string) => entries.delete(key)
  })
})
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('认证生命周期', () => {
  it('凭据恢复与输入均不会提前发布已认证状态', () => {
    localStorage.setItem('api-proxy-auth', JSON.stringify({ apiKey: 'current' }))
    localStorage.setItem('proxyAccessKey', 'old')
    const auth = useAuthStore()
    auth.initializeAuth()
    expect(auth.apiKey).toBe('current')
    expect(localStorage.getItem('proxyAccessKey')).toBeNull()
    expect(auth.isAuthenticated).toBe(false)
    auth.confirmAuthentication(auth.generation)
    expect(auth.isAuthenticated).toBe(true)
    auth.setApiKey('next')
    expect(auth.isAuthenticated).toBe(false)
  })

  it('锁定到期不依赖响应式时间更新', () => {
    const now = vi.spyOn(Date, 'now').mockReturnValue(1000)
    const auth = useAuthStore()
    auth.rejectAuthentication(false, 1)
    expect(auth.lockoutSeconds()).toBe(300)
    now.mockReturnValue(301001)
    expect(auth.lockoutSeconds()).toBe(0)
    auth.clearExpiredLockout()
    expect(auth.authAttempts).toBe(0)
    expect(auth.authLockoutTime).toBeNull()
  })

  it('旧请求的 401 不能注销新登录', async () => {
    let finish!: (value: Response) => void
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(resolve => { finish = resolve })))
    const auth = useAuthStore()
    auth.setApiKey('old')
    auth.confirmAuthentication(auth.generation)
    const pending = apiRequest('/old')
    const rejected = expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    auth.setApiKey('new')
    auth.confirmAuthentication(auth.generation)
    finish(new Response('{"error":"expired"}', { status: 401 }))
    await rejected
    expect(auth.apiKey).toBe('new')
    expect(auth.isAuthenticated).toBe(true)
  })

  it('注销后，即使正文忽略取消并晚到，也不能返回业务结果', async () => {
    let finish!: (value: string) => void
    const response = new Response('{}')
    vi.spyOn(response, 'text').mockImplementation(() => new Promise(resolve => { finish = resolve }))
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))
    const auth = useAuthStore()
    const pending = apiRequest('/data')
    const rejected = expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    await vi.waitFor(() => expect(response.text).toHaveBeenCalled())
    auth.clearAuth()
    finish('{"secret":"old-session"}')
    await rejected
  })
})

describe('管理 HTTP 响应契约', () => {
  it.each([
    [{ error: '保存失败', code: 'CONFIG_COMMITTED', committed: true }, '保存失败', 'CONFIG_COMMITTED', true],
    [{ error: { message: '上游拒绝' } }, '上游拒绝', undefined, undefined],
    [{ error: null, message: '目标不存在' }, '目标不存在', undefined, undefined],
    ['网关暂时不可用', '网关暂时不可用', undefined, undefined],
    [{ message: '保存失败', code: 1, committed: 'false' }, '保存失败', undefined, undefined]
  ])('保留错误信息与可信的提交标记 %#', async (body, message, code, committed) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(body), { status: 500 })))
    await expect(apiRequest('/data')).rejects.toMatchObject({ message, code, committed, status: 500 })
  })

  it.each(['', '<html>登录页</html>', '{"truncated":', 'null'])('拒绝无效成功体 %s', async body => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(body)))
    await expect(apiRequest('/data')).rejects.toMatchObject({ code: 'INVALID_RESPONSE' })
  })

  it('保留 Headers 和元组头部，204 独立返回空结果', async () => {
    const fetch = vi.fn().mockImplementation(() => Promise.resolve(new Response(null, { status: 204 })))
    vi.stubGlobal('fetch', fetch)
    for (const headers of [new Headers({ 'X-Custom': 'value' }), [['X-Custom', 'value']] as [string, string][]]) {
      await expect(apiRequest('/empty', { headers })).resolves.toBeNull()
      expect((fetch.mock.lastCall?.[1] as RequestInit).headers).toBeInstanceOf(Headers)
      expect(new Headers((fetch.mock.lastCall?.[1] as RequestInit).headers).get('X-Custom')).toBe('value')
    }
  })
})
