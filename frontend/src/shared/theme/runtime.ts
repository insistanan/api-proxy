import { watch } from 'vue'
import type { Pinia } from 'pinia'
import { useThemeStore } from '@/shared/theme/store'
import { parseTheme, readTheme, THEME_STORAGE_KEY } from '@/shared/theme/bootstrap'

export function installTheme(pinia: Pinia) {
  const store = useThemeStore(pinia)
  const query = window.matchMedia('(prefers-color-scheme: dark)')
  store.systemDark = query.matches
  function report(error: unknown) {
    store.error = `主题偏好未能保存或恢复：${error instanceof Error ? error.message : String(error)}`
  }
  try {
    store.settings = readTheme(window.localStorage)
  } catch (error) {
    report(error)
  }
  const stop = watch(
    () => [store.settings, store.isDark] as const,
    () => {
      const root = document.documentElement
      root.dataset.themeMode = store.isDark ? 'dark' : 'light'
      for (const key of ['palette', 'surface', 'radius', 'decoration', 'opacity'] as const)
        root.dataset[key] = String(store.settings[key])
      root.style.colorScheme = store.isDark ? 'dark' : 'light'
    },
    { deep: true, immediate: true, flush: 'sync' }
  )
  let syncing = false
  const persist = watch(
    () => store.settings,
    () => {
      if (syncing) return
      try {
        window.localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify(store.settings))
        store.error = ''
      } catch (error) {
        report(error)
      }
    },
    { deep: true, flush: 'sync' }
  )
  const followSystem = () => {
    store.systemDark = query.matches
  }
  const sync = (event: StorageEvent) => {
    if (event.key !== THEME_STORAGE_KEY) return
    try {
      if (event.newValue === null) throw new Error('另一标签页移除了主题偏好，请重新选择主题')
      // 避免 storage 回写在两个标签页之间反复触发。
      const next = parseTheme(event.newValue)
      syncing = true
      store.settings = next
      store.error = ''
    } catch (error) {
      report(error)
    } finally {
      syncing = false
    }
  }
  query.addEventListener('change', followSystem)
  window.addEventListener('storage', sync)
  return () => {
    stop()
    persist()
    query.removeEventListener('change', followSystem)
    window.removeEventListener('storage', sync)
  }
}
