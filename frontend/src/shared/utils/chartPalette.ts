// 图表调色板唯一来源：低饱和的"砚与铜"同调色，明暗主题共用一套中间明度值，
// 切换主题时不需要重算系列色。GlobalStatsChart / KeyTrendChart / 渠道日志趋势共用。
export const CHART_PALETTE = {
  indigo: '#5C6BC8',
  copper: '#C1804A',
  jade: '#2FA478',
  violet: '#8A72C0',
  rose: '#C46A92',
  ochre: '#B99436',
  teal: '#3E93A8',
  coral: '#CA6072',
  olive: '#7E9C46',
  slate: '#7B8494',
  blue: '#6E77CE',
  danger: '#D05B5B',
} as const

/** 渠道运行状态使用低饱和色，避免小面积柱状图和指标条过亮。 */
export const CHART_STATUS_COLORS = {
  success: '#73998B',
  warning: '#B29A76',
  failure: '#B27E83',
  info: '#809EAD',
} as const

/** 多渠道/多 Key 系列色环（十色）。 */
export const CHART_SERIES_COLORS: string[] = [
  CHART_PALETTE.indigo,
  CHART_PALETTE.copper,
  CHART_PALETTE.jade,
  CHART_PALETTE.violet,
  CHART_PALETTE.rose,
  CHART_PALETTE.ochre,
  CHART_PALETTE.teal,
  CHART_PALETTE.coral,
  CHART_PALETTE.olive,
  CHART_PALETTE.blue,
]

/** 全局统计图各视图的语义色：流量=请求/成功，Token=输入/输出。 */
export const GLOBAL_STATS_COLORS = {
  traffic: {
    primary: CHART_PALETTE.indigo,
    success: CHART_PALETTE.jade,
  },
  tokens: {
    input: CHART_PALETTE.violet,
    output: CHART_PALETTE.copper,
  },
} as const

/**
 * 图表主题派生色：文本与网格对齐 Vuetify 的 on-surface-variant / outline-variant，
 * tooltip 底色取略深于 surface 的值。集中在此避免主题调整后图表仍用旧字面量。
 */
export const CHART_THEME_COLORS = {
  light: {
    text: '#555E6E',
    grid: 'rgba(0,0,0,0.06)',
    tooltipBg: '#FFFFFF',
    tooltipBorder: '#C9CFDA',
  },
  dark: {
    text: '#A2AAB8',
    grid: 'rgba(255,255,255,0.06)',
    tooltipBg: '#151A22',
    tooltipBorder: '#39414E',
  },
} as const

export type ChartThemeColors = (typeof CHART_THEME_COLORS)[keyof typeof CHART_THEME_COLORS]

/** 按明暗主题取图表派生色。 */
export const chartThemeColors = (isDark: boolean): ChartThemeColors =>
  isDark ? CHART_THEME_COLORS.dark : CHART_THEME_COLORS.light

/** 缓存命中率分档色：良好 / 一般 / 偏低。 */
export const cacheHitRateColor = (rate: number): string => {
  if (rate >= 50) return CHART_PALETTE.jade
  if (rate >= 20) return CHART_PALETTE.indigo
  return CHART_PALETTE.copper
}
