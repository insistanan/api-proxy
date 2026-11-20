import { computed, onMounted, ref } from 'vue'
import { getActivePinia } from 'pinia'
import { useThemeStore } from './store'

export const chartTokens = {
  indigo: '--chart-1',
  copper: '--chart-2',
  jade: '--chart-3',
  violet: '--chart-4',
  rose: '--chart-5',
  ochre: '--chart-6',
  teal: '--chart-7',
  coral: '--chart-8',
  olive: '--chart-9',
  blue: '--chart-10',
  slate: '--chart-slate',
  danger: '--chart-danger',
  success: '--chart-success',
  warning: '--chart-warning',
  failure: '--chart-failure',
  info: '--chart-info',
  text: '--chart-text',
  grid: '--chart-grid',
  tooltipBg: '--chart-tooltip',
  tooltipBorder: '--border'
} as const

type ChartRole = keyof typeof chartTokens
export const chartColor = (role: ChartRole) => `var(${chartTokens[role]})`

export function useChartColors() {
  const pinia = getActivePinia()
  const theme = pinia ? useThemeStore(pinia) : undefined
  const mounted = ref(false)
  onMounted(() => {
    mounted.value = true
  })

  return computed(() => {
    const settings = theme?.settings
    const isDark = theme?.isDark ?? false
    const styles = mounted.value && settings ? getComputedStyle(document.documentElement) : undefined
    const colors = Object.fromEntries(
      Object.keys(chartTokens).map(key => {
        const role = key as ChartRole
        const value = styles?.getPropertyValue(chartTokens[role]).trim()
        return [role, value || chartColor(role)]
      })
    ) as Record<ChartRole, string>
    return {
      ...colors,
      isDark,
      series: [
        colors.indigo,
        colors.copper,
        colors.jade,
        colors.violet,
        colors.rose,
        colors.ochre,
        colors.teal,
        colors.coral,
        colors.olive,
        colors.blue
      ]
    }
  })
}
