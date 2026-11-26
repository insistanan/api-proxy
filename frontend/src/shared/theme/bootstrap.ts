import { DEFAULT_THEME, THEME_OPTIONS } from '@/shared/theme/catalog'
import type { ThemeSettings } from '@/shared/theme/types'

export const THEME_STORAGE_KEY = 'api-proxy-theme'

export function parseTheme(text: string): ThemeSettings {
  const value: unknown = JSON.parse(text)
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('主题配置必须是对象')
  const settings = value as Record<string, unknown>
  if (settings.schemaVersion !== 1) throw new Error('不支持的主题配置版本')
  for (const [key, options] of Object.entries(THEME_OPTIONS)) {
    if (!Object.prototype.hasOwnProperty.call(options, String(settings[key]))) throw new Error(`无效的主题选项：${key}`)
  }
  if (typeof settings.opacity !== 'number') throw new Error('主题透明度必须是数字')
  return Object.fromEntries(
    ['schemaVersion', ...Object.keys(THEME_OPTIONS)].map(key => [key, settings[key]])
  ) as unknown as ThemeSettings
}

export function readTheme(storage: Pick<Storage, 'getItem' | 'setItem'>): ThemeSettings {
  const saved = storage.getItem(THEME_STORAGE_KEY)
  if (saved !== null) return parseTheme(saved)
  const legacy = storage.getItem('api-proxy-preferences')
  if (legacy === null) return { ...DEFAULT_THEME }
  const old: unknown = JSON.parse(legacy)
  if (!old || typeof old !== 'object') throw new Error('旧偏好配置格式无效')
  const preference = (old as Record<string, unknown>).darkModePreference
  if (preference === undefined) return { ...DEFAULT_THEME }
  if (preference !== 'auto' && preference !== 'dark' && preference !== 'light') throw new Error('旧明暗偏好无效')
  const migrated: ThemeSettings = {
    ...DEFAULT_THEME,
    mode: preference === 'auto' ? 'system' : (preference as 'light' | 'dark')
  }
  // 写入新配置成功后才清理旧字段，保留认证以外的其他偏好。
  storage.setItem(THEME_STORAGE_KEY, JSON.stringify(migrated))
  const { darkModePreference: _removed, ...rest } = old as Record<string, unknown>
  storage.setItem('api-proxy-preferences', JSON.stringify(rest))
  return migrated
}
