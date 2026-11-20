// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'
import { installTheme } from './runtime'
import { DEFAULT_THEME } from './catalog'
import { useThemeStore } from './store'
import { THEME_STORAGE_KEY } from './bootstrap'

const stopHandlers: Array<() => void> = []
afterEach(() => {
  stopHandlers.splice(0).forEach(stop => stop())
  window.localStorage.clear()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

function setupTheme() {
  const listeners = new Set<() => void>()
  const media = {
    matches: false,
    addEventListener: (_event: string, listener: () => void) => listeners.add(listener),
    removeEventListener: (_event: string, listener: () => void) => listeners.delete(listener)
  }
  vi.stubGlobal('matchMedia', () => media)
  const pinia = createPinia()
  stopHandlers.push(installTheme(pinia))
  return {
    theme: useThemeStore(pinia),
    system: (dark: boolean) => {
      media.matches = dark
      listeners.forEach(listener => listener())
    }
  }
}

describe('主题偏好', () => {
  it.each(['pop', 'breeze'])('新增主题 %s 支持保存、恢复及独立的明暗切换', preset => {
    const { theme, system } = setupTheme()
    theme.applyPreset(preset)
    expect(theme.preset).toBe(preset)
    const saved = JSON.parse(localStorage.getItem(THEME_STORAGE_KEY)!)
    expect(saved.palette).toBe(preset)
    system(true)
    expect(document.documentElement.dataset).toMatchObject({ themeMode: 'dark', palette: preset })
    window.dispatchEvent(new StorageEvent('storage', { key: THEME_STORAGE_KEY, newValue: JSON.stringify(saved) }))
    expect(theme.preset).toBe(preset)
    theme.update({ mode: 'light' })
    expect(document.documentElement.dataset).toMatchObject({ themeMode: 'light', palette: preset })
  })
  it('首次启动迁移明暗偏好并保留其他设置，应用预设不改变明暗', () => {
    localStorage.setItem('api-proxy-preferences', JSON.stringify({ darkModePreference: 'dark', showGlobalStats: true }))
    const { theme, system } = setupTheme()
    expect(document.documentElement.dataset.themeMode).toBe('dark')
    expect(JSON.parse(localStorage.getItem('api-proxy-preferences')!)).toEqual({ showGlobalStats: true })
    theme.applyPreset('ocean')
    system(false)
    expect(document.documentElement.dataset).toMatchObject({ themeMode: 'dark', palette: 'ocean', decoration: 'waves' })
    expect(JSON.parse(localStorage.getItem(THEME_STORAGE_KEY)!)).toMatchObject({ mode: 'dark', palette: 'ocean' })
    theme.update({ mode: 'system' })
    expect(document.documentElement.dataset.themeMode).toBe('light')
    system(true)
    expect(document.documentElement.dataset.themeMode).toBe('dark')
  })

  it('跨标签页同步已保存的外观，且不回写或接受损坏的配置', () => {
    const { theme } = setupTheme()
    const write = vi.spyOn(Storage.prototype, 'setItem')
    window.dispatchEvent(
      new StorageEvent('storage', {
        key: THEME_STORAGE_KEY,
        newValue: JSON.stringify({ ...DEFAULT_THEME, palette: 'forest', radius: 'square' })
      })
    )
    expect(document.documentElement.dataset).toMatchObject({ palette: 'forest', radius: 'square' })
    expect(write).not.toHaveBeenCalled()
    window.dispatchEvent(new StorageEvent('storage', { key: THEME_STORAGE_KEY, newValue: '{broken' }))
    expect(theme.settings.palette).toBe('forest')
    expect(theme.error).toContain('主题偏好未能保存或恢复')
  })

  it('存储写入失败时仍更新当前页面并显式提示失败', () => {
    const { theme } = setupTheme()
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('存储不可用')
    })
    theme.applyPreset('glass')
    expect(document.documentElement.dataset).toMatchObject({ surface: 'glass', opacity: '92' })
    expect(theme.error).toContain('存储不可用')
  })
})
