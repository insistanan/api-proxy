/**
 * 版本检查服务
 * 参考 gpt-load 项目实现
 */

const CACHE_KEY = 'api-proxy-version-info-v2'
const CACHE_DURATION = 30 * 60 * 1000 // 30分钟缓存
const ERROR_CACHE_DURATION = 5 * 60 * 1000 // 错误状态缓存5分钟，避免频繁请求
const GITHUB_API_TIMEOUT = 10000 // 10秒超时

export interface GitHubRelease {
  tag_name: string
  html_url: string
  published_at: string
  name: string
  prerelease?: boolean
  draft?: boolean
  assets: { name: string; state: string }[]
}

export interface VersionInfo {
  currentVersion: string
  latestVersion: string | null
  isLatest: boolean
  hasUpdate: boolean
  releaseUrl: string | null
  lastCheckTime: number
  status: 'checking' | 'latest' | 'update-available' | 'error'
  assets?: string[]
  error?: string
}

const REPOSITORY = 'https://github.com/insistanan/api-proxy'

export function releaseDownload(platform: { os: string; arch: string }): { asset: string; url: string; label: string } {
  const systems: Record<string, string> = { windows: 'Windows', linux: 'Linux', darwin: 'macOS' }
  const architectures: Record<string, string> = { amd64: 'x64', arm64: 'ARM64' }
  if (!Object.prototype.hasOwnProperty.call(systems, platform.os) || !Object.prototype.hasOwnProperty.call(architectures, platform.arch)) throw new Error(`尚未发布 ${platform.os}/${platform.arch} 的安装包`)
  const asset = `api-proxy-${platform.os}-${platform.arch}${platform.os === 'windows' ? '.exe' : ''}`
  return { asset, url: `${REPOSITORY}/releases/latest/download/${asset}`, label: `${systems[platform.os]} ${architectures[platform.arch]}` }
}

// 同时比较正式版本和预发布标识，构建元数据不参与版本先后判断。
export function compareVersions(current: string, latest: string): number {
  const parse = (value: string) => {
    const match = /^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.exec(value)
    if (!match) throw new Error(`无法识别版本号：${value}`)
    const parts = [Number(match[1]), Number(match[2]), Number(match[3])]
    if (parts.some(part => !Number.isSafeInteger(part))) throw new Error(`版本号超出有效范围：${value}`)
    const pre = match[4]?.split('.')
    if (pre?.some(part => /^0\d+$/.test(part))) throw new Error(`预发布版本号不能有前导零：${value}`)
    return { parts, pre }
  }
  const a = parse(current)
  const b = parse(latest)
  for (let i = 0; i < 3; i++) {
    if (a.parts[i] !== b.parts[i]) return a.parts[i]! < b.parts[i]! ? -1 : 1
  }
  if (!a.pre || !b.pre) return a.pre ? -1 : b.pre ? 1 : 0
  for (let i = 0; i < Math.max(a.pre.length, b.pre.length); i++) {
    const left = a.pre[i]
    const right = b.pre[i]
    if (left === right) continue
    if (left === undefined || right === undefined) return left === undefined ? -1 : 1
    const leftNumeric = /^\d+$/.test(left)
    const rightNumeric = /^\d+$/.test(right)
    if (leftNumeric && rightNumeric) return BigInt(left) < BigInt(right) ? -1 : BigInt(left) > BigInt(right) ? 1 : 0
    if (leftNumeric !== rightNumeric) return leftNumeric ? -1 : 1
    return left < right ? -1 : 1
  }
  return 0
}

// 任意 SemVer 预发布标识都不作为正式更新，构建元数据中的连字符不受影响。
const PRERELEASE_PATTERN = /^v?\d+\.\d+\.\d+-/

class VersionService {
  private currentVersion: string = ''

  /**
   * 检查是否为预发布版本
   */
  private isPrerelease(version: string): boolean {
    return PRERELEASE_PATTERN.test(version)
  }

  /**
   * 设置当前版本（从 /health 端点获取）
   */
  setCurrentVersion(version: string): void {
    this.currentVersion = version
  }

  /**
   * 获取当前版本
   */
  getCurrentVersion(): string {
    return this.currentVersion
  }

  /**
   * 从缓存获取版本信息
   */
  private getCachedVersionInfo(): VersionInfo | null {
    try {
      const cached = localStorage.getItem(CACHE_KEY)
      if (!cached) {
        return null
      }

      const versionInfo: VersionInfo = JSON.parse(cached)
      const now = Date.now()

      if (!Number.isFinite(versionInfo.lastCheckTime) || !['latest', 'update-available', 'error'].includes(versionInfo.status)) return null
      if (versionInfo.status !== 'error') {
        if (!versionInfo.latestVersion || !Array.isArray(versionInfo.assets)) return null
        compareVersions(versionInfo.currentVersion, versionInfo.latestVersion)
      }

      // 根据状态选择不同的缓存时长
      const cacheDuration = versionInfo.status === 'error'
        ? ERROR_CACHE_DURATION
        : CACHE_DURATION

      // 检查缓存是否过期
      if (now - versionInfo.lastCheckTime > cacheDuration) {
        return null
      }

      // 检查缓存中的版本号是否与当前应用版本号一致
      if (versionInfo.currentVersion !== this.currentVersion) {
        this.clearCache()
        return null
      }

      return versionInfo
    } catch (error) {
      console.warn('Failed to parse cached version info:', error)
      localStorage.removeItem(CACHE_KEY)
      return null
    }
  }

  /**
   * 保存版本信息到缓存
   */
  private setCachedVersionInfo(info: VersionInfo): void {
    try {
      localStorage.setItem(CACHE_KEY, JSON.stringify(info))
    } catch (error) {
      console.warn('Failed to cache version info:', error)
    }
  }

  /**
   * 清除缓存
   */
  clearCache(): void {
    localStorage.removeItem(CACHE_KEY)
  }

  /**
   * 版本比较
   * @returns -1: current < latest (有更新), 0: 相等, 1: current > latest
   */
  private compareVersions(current: string, latest: string): number {
    return compareVersions(current, latest)
  }

  /**
   * 从 GitHub API 获取最新正式版本（过滤预发布版本）
   */
  private async fetchLatestVersion(): Promise<GitHubRelease | null> {
    const controller = new AbortController()
    const timeoutId = setTimeout(() => controller.abort(), GITHUB_API_TIMEOUT)
    try {
      // 与固定下载入口使用同一个 latest 正式发布，草稿和预发布不作为更新。
      const response = await fetch(
        'https://api.github.com/repos/insistanan/api-proxy/releases/latest',
        {
          headers: {
            Accept: 'application/vnd.github.v3+json',
          },
          signal: controller.signal,
        }
      )

      if (!response.ok) throw new Error(`GitHub 版本检查失败（HTTP ${response.status}）`)
      const release: GitHubRelease = await response.json()
      if (release.draft || release.prerelease || this.isPrerelease(release.tag_name) || !Array.isArray(release.assets)) throw new Error('GitHub latest 未提供完整的正式发布信息')
      return release
    } finally {
      clearTimeout(timeoutId)
    }
  }

  /**
   * 检查更新
   */
  async checkForUpdates(): Promise<VersionInfo> {
    // 如果没有当前版本，返回错误状态
    if (!this.currentVersion) {
      return {
        currentVersion: '',
        latestVersion: null,
        isLatest: false,
        hasUpdate: false,
        releaseUrl: null,
        lastCheckTime: Date.now(),
        status: 'error',
      }
    }

    // 先检查缓存
    const cached = this.getCachedVersionInfo()
    if (cached) {
      return cached
    }

    // 创建初始状态
    const versionInfo: VersionInfo = {
      currentVersion: this.currentVersion,
      latestVersion: null,
      isLatest: false,
      hasUpdate: false,
      releaseUrl: null,
      lastCheckTime: Date.now(),
      status: 'checking',
    }

    // 获取最新版本
    try {
      const release = await this.fetchLatestVersion()

      if (release) {
        const comparison = this.compareVersions(this.currentVersion, release.tag_name)

        versionInfo.latestVersion = release.tag_name
        versionInfo.releaseUrl = release.html_url
        versionInfo.assets = release.assets.filter(asset => asset.state === 'uploaded').map(asset => asset.name)
        versionInfo.isLatest = comparison >= 0
        versionInfo.hasUpdate = comparison < 0
        versionInfo.status = comparison < 0 ? 'update-available' : 'latest'

        // 成功时缓存结果（30分钟）
        this.setCachedVersionInfo(versionInfo)
      } else {
        versionInfo.status = 'error'
        // 错误时也缓存（5分钟），避免频繁请求 GitHub API
        this.setCachedVersionInfo(versionInfo)
      }
    } catch (error) {
      console.warn('Version check failed:', error)
      versionInfo.status = 'error'
      versionInfo.error = error instanceof Error ? error.message : String(error)
      // 错误时也缓存（5分钟），避免频繁请求 GitHub API
      this.setCachedVersionInfo(versionInfo)
    }

    return versionInfo
  }
}

// 导出单例
export const versionService = new VersionService()
