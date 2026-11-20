const colors: Record<string, string> = {
  primary: 'text-primary',
  secondary: 'text-secondary',
  success: 'text-success',
  warning: 'text-warning',
  error: 'text-destructive',
  destructive: 'text-destructive',
  info: 'text-info',
  accent: 'text-accent'
}
export function colorClass(color?: string) {
  return color ? (colors[color] ?? '') : ''
}
export function toneClass(color = 'primary', solid = false) {
  const tones: Record<string, string[]> = {
    primary: ['bg-primary/12 text-primary border-primary/25', 'bg-primary text-primary-foreground border-primary'],
    secondary: [
      'bg-secondary/12 text-secondary border-secondary/25',
      'bg-secondary text-secondary-foreground border-secondary'
    ],
    success: ['bg-success/12 text-success border-success/25', 'bg-success text-success-foreground border-success'],
    warning: ['bg-warning/12 text-warning border-warning/25', 'bg-warning text-warning-foreground border-warning'],
    error: [
      'bg-destructive/12 text-destructive border-destructive/25',
      'bg-destructive text-destructive-foreground border-destructive'
    ],
    info: ['bg-info/12 text-info border-info/25', 'bg-info text-info-foreground border-info']
  }
  return (tones[color] ?? tones.primary)[solid ? 1 : 0]
}
