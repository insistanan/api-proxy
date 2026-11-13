import { shallowRef, watch } from 'vue'

export const CHART_DURATIONS = ['1h', '6h', '24h', 'today'] as const
export type ChartDuration = (typeof CHART_DURATIONS)[number]

const isChartDuration = (value: string): value is ChartDuration =>
  (CHART_DURATIONS as readonly string[]).includes(value)

const storageAvailable = (): boolean => {
  try { return typeof window !== 'undefined' && window.localStorage !== undefined }
  catch { return false }
}

const readPreference = (key: string): string | null => {
  if (!storageAvailable()) return null
  try { return window.localStorage.getItem(key) } catch { return null }
}

const writePreference = (key: string, value: string): void => {
  if (!storageAvailable()) return
  try { window.localStorage.setItem(key, value) } catch { /* 存储不可用时偏好退化为默认值 */ }
}

/**
 * 图表视图与时间范围的本地偏好。
 *
 * 收敛背景：GlobalStatsChart 与 KeyTrendChart 各自维护一套 localStorage
 * 读写与合法性校验逻辑。本 composable 统一键格式
 * `${namespace}:${scope}:{viewMode|duration}`，并在选中值变化时自动落盘。
 *
 * @param namespace 存储命名空间，如 'globalStats'、'keyTrendChart'
 * @param scope 作用域，通常返回组件的 apiType / channelType（使用 getter 以响应切换）
 * @param views 合法视图列表，用于过滤历史残留值
 * @param defaults 非法或缺失时采用的默认视图与时间范围
 */
export function useChartPreference<V extends string>(
  namespace: string,
  scope: () => string,
  views: readonly V[],
  defaults: { view: V; duration: ChartDuration },
) {
  const keys = () => {
    const prefix = `${namespace}:${scope()}`
    return { view: `${prefix}:viewMode`, duration: `${prefix}:duration` }
  }

  const readPreferences = (): { view: V; duration: ChartDuration } => {
    const savedView = readPreference(keys().view) as V | null
    const savedDuration = readPreference(keys().duration)
    return {
      view: savedView && views.includes(savedView) ? savedView : defaults.view,
      duration: savedDuration && isChartDuration(savedDuration) ? savedDuration : defaults.duration,
    }
  }

  const initial = readPreferences()
  const selectedView = shallowRef<V>(initial.view)
  const selectedDuration = shallowRef<ChartDuration>(initial.duration)

  watch(selectedView, (value) => writePreference(keys().view, value))
  watch(selectedDuration, (value) => writePreference(keys().duration, value))

  /** 作用域变化时重新读取偏好；返回结果供调用方判断是否需要刷新数据。 */
  const loadPreferences = () => {
    const preferences = readPreferences()
    selectedView.value = preferences.view
    selectedDuration.value = preferences.duration
    return preferences
  }

  return { selectedView, selectedDuration, loadPreferences }
}
