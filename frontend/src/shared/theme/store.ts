import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { DEFAULT_THEME, THEME_PRESETS } from '@/shared/theme/catalog'
import { parseTheme } from '@/shared/theme/bootstrap'
import type { ThemeSettings } from '@/shared/theme/types'

export const useThemeStore = defineStore('theme', () => {
  const settings = ref<ThemeSettings>({ ...DEFAULT_THEME })
  const systemDark = ref(false)
  const error = ref('')
  const isDark = computed(
    () => settings.value.mode === 'dark' || (settings.value.mode === 'system' && systemDark.value)
  )
  const preset = computed(
    () =>
      THEME_PRESETS.find(item =>
        Object.entries(item.settings).every(([key, value]) => settings.value[key as keyof ThemeSettings] === value)
      )?.id ?? 'custom'
  )
  function update(patch: Partial<ThemeSettings>) {
    settings.value = parseTheme(JSON.stringify({ ...settings.value, ...patch }))
  }
  function applyPreset(id: string) {
    const item = THEME_PRESETS.find(item => item.id === id)
    if (!item) throw new Error(`未知主题预设：${id}`)
    update(item.settings)
  }
  function reset() {
    settings.value = { ...DEFAULT_THEME }
  }
  return { settings, systemDark, error, isDark, preset, update, applyPreset, reset }
})
