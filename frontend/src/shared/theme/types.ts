export interface ThemeSettings {
  schemaVersion: 1
  mode: 'light' | 'dark' | 'system'
  palette: 'ink' | 'ocean' | 'forest' | 'sunset' | 'rose' | 'pop' | 'breeze' | 'sultan'
  surface: 'flat' | 'soft' | 'raised' | 'glass' | 'bold' | 'antique'
  radius: 'square' | 'standard' | 'rounded'
  decoration: 'none' | 'plain' | 'waves' | 'glow' | 'confetti' | 'arabesque'
  opacity: 60 | 70 | 85 | 92 | 100
}

export type ThemePreset = { id: string; name: string; description?: string; settings: Omit<ThemeSettings, 'schemaVersion' | 'mode'> }
