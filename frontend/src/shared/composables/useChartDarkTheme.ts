import { computed, inject } from 'vue'
import type { useTheme } from 'vuetify'

/** 跟随管理台实际主题，未装配 Vuetify 时使用浅色。 */
export function useChartDarkTheme() {
  const theme = inject<ReturnType<typeof useTheme> | null>(Symbol.for('vuetify:theme'), null)
  return computed(() => theme?.global.current.value.dark === true)
}
