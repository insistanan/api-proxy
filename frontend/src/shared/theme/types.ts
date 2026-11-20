export interface ThemeSettings {
  schemaVersion: 1
  mode: 'light' | 'dark' | 'system'
  palette: 'ink' | 'ocean' | 'forest' | 'sunset' | 'rose' | 'pop' | 'breeze'
  surface: 'flat' | 'soft' | 'raised' | 'glass' | 'bold'
  radius: 'square' | 'standard' | 'rounded'
  decoration: 'none' | 'waves' | 'glow' | 'confetti'
  opacity: 85 | 92 | 100
}

export type ThemePreset = { id: string; name: string; description?: string; settings: Omit<ThemeSettings, 'schemaVersion' | 'mode'> }
