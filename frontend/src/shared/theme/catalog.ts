import type { ThemePreset, ThemeSettings } from './types'

export const DEFAULT_THEME: ThemeSettings = {
  schemaVersion: 1,
  mode: 'system',
  palette: 'ink',
  surface: 'soft',
  radius: 'standard',
  decoration: 'none',
  opacity: 100
}
export const THEME_OPTIONS = {
  mode: { light: '亮色', dark: '暗色', system: '跟随系统' },
  palette: { ink: '砚墨', ocean: '海蓝', forest: '森林', sunset: '暖橙', rose: '玫瑰', pop: '跃色撞色', breeze: '清岚青蓝', sultan: '苏丹暗金' },
  surface: { flat: '扁平', soft: '柔和', raised: '厚重', glass: '玻璃', bold: '描边撞色', antique: '鎏金文书' },
  radius: { square: '方角', standard: '标准', rounded: '圆润' },
  decoration: { none: '纯色', waves: '波浪', glow: '光晕', confetti: '几何彩点', arabesque: '八角星暗纹' },
  opacity: { 60: '60%', 70: '70%', 85: '85%', 92: '92%', 100: '100%' }
} as const

export const THEME_PRESETS: ThemePreset[] = [
  {
    id: 'pop',
    name: '跃色',
    description: '莓紫 × 青柠，描边与几何彩点',
    settings: { palette: 'pop', surface: 'bold', radius: 'rounded', decoration: 'confetti', opacity: 100 }
  },
  {
    id: 'breeze',
    name: '清岚',
    description: '雾白 × 青蓝，轻盈而有序',
    settings: { palette: 'breeze', surface: 'soft', radius: 'standard', decoration: 'none', opacity: 100 }
  },
  {
    id: 'ink',
    name: '砚墨',
    settings: { palette: 'ink', surface: 'soft', radius: 'standard', decoration: 'none', opacity: 100 }
  },
  {
    id: 'ocean',
    name: '海浪',
    settings: { palette: 'ocean', surface: 'flat', radius: 'standard', decoration: 'waves', opacity: 100 }
  },
  {
    id: 'forest',
    name: '森林',
    settings: { palette: 'forest', surface: 'soft', radius: 'rounded', decoration: 'none', opacity: 100 }
  },
  {
    id: 'sunset',
    name: '落日',
    settings: { palette: 'sunset', surface: 'raised', radius: 'rounded', decoration: 'waves', opacity: 100 }
  },
  {
    id: 'glass',
    name: '流光玻璃',
    settings: { palette: 'ink', surface: 'glass', radius: 'rounded', decoration: 'glow', opacity: 70 }
  },
  {
    id: 'studio',
    name: '方正工作台',
    settings: { palette: 'rose', surface: 'raised', radius: 'square', decoration: 'none', opacity: 100 }
  },
  {
    id: 'sultan',
    name: '苏丹秘卷',
    description: '暗金 × 血红，羊皮纸与八角星暗纹',
    settings: { palette: 'sultan', surface: 'antique', radius: 'square', decoration: 'arabesque', opacity: 100 }
  }
]
