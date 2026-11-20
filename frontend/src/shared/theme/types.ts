export interface ThemeSettings {
  schemaVersion: 1
  mode: 'light' | 'dark' | 'system'
  palette: 'ink' | 'ocean' | 'forest' | 'sunset' | 'rose'
  surface: 'flat' | 'soft' | 'raised' | 'glass'
  radius: 'square' | 'standard' | 'rounded'
  decoration: 'none' | 'waves' | 'glow'
  opacity: 85 | 92 | 100
}

export type ThemePreset = { id: string; name: string; settings: Omit<ThemeSettings, 'schemaVersion' | 'mode'> }
