import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { compareVersions, releaseDownload, versionService } from '@/shared/services/version'

describe('正式发布版本与平台下载', () => {
  it.each([
    ['v3.3.9', 'v3.3.10', -1],
    ['3.4.0-rc.10', '3.4.0-rc.2', 1],
    ['3.4.0-rc.1', '3.4.0', -1],
    ['3.4.0+build.1', 'v3.4.0+build.2', 0],
    ['3.4.0-alpha.1', '3.4.0-alpha.beta', -1],
  ] as const)('正确比较 %s 与 %s', (current, latest, expected) => {
    expect(compareVersions(current, latest)).toBe(expected)
  })
  it.each(['dev', 'v1.02.3', '1.2.3-rc.01'])('显式拒绝无效版本 %s', value => {
    expect(() => compareVersions(value, '1.2.3')).toThrow()
  })
  it('六个平台使用后端标识和固定附件名，不依赖 tag', () => {
    for (const os of ['windows', 'linux', 'darwin']) {
      for (const arch of ['amd64', 'arm64']) {
        const asset = `api-proxy-${os}-${arch}${os === 'windows' ? '.exe' : ''}`
        expect(releaseDownload({ os, arch })).toMatchObject({ asset, url: `https://github.com/insistanan/api-proxy/releases/latest/download/${asset}` })
      }
    }
    expect(() => releaseDownload({ os: 'linux', arch: '386' })).toThrow('尚未发布')
  })
})

describe('更新检查和附件完整性', () => {
  beforeEach(() => {
    vi.stubGlobal('localStorage', { getItem: vi.fn().mockReturnValue(null), setItem: vi.fn(), removeItem: vi.fn() })
    versionService.setCurrentVersion('v3.3.19')
  })
  afterEach(() => { vi.unstubAllGlobals() })
  it('检查同一个 latest，只有已上传附件才参与下载选择', async () => {
    const fetcher = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ tag_name: 'v3.3.20', html_url: 'https://github.com/insistanan/api-proxy/releases/tag/v3.3.20', assets: [{ name: 'api-proxy-linux-amd64', state: 'uploaded' }, { name: 'api-proxy-windows-amd64.exe', state: 'new' }] }) })
    vi.stubGlobal('fetch', fetcher)
    const result = await versionService.checkForUpdates()
    expect(fetcher.mock.calls[0]?.[0]).toBe('https://api.github.com/repos/insistanan/api-proxy/releases/latest')
    expect(result.hasUpdate).toBe(true)
    expect(result.assets).toEqual(['api-proxy-linux-amd64'])
  })
  it('检查失败显示错误而非伪装最新', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 403 }))
    const result = await versionService.checkForUpdates()
    expect(result).toMatchObject({ status: 'error', hasUpdate: false, isLatest: false })
    expect(result.error).toContain('403')
  })
})
