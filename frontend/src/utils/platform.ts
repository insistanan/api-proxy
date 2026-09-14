/**
 * 检测操作系统
 */
export function detectOS(): string {
  if (typeof navigator === 'undefined') return 'Unknown'
  const ua = navigator.userAgent
  if (ua.includes('Windows')) return 'Windows'
  if (ua.includes('Mac')) return 'macOS'
  if (ua.includes('Linux')) return 'Linux'
  if (ua.includes('Android')) return 'Android'
  if (ua.includes('iOS') || ua.includes('iPhone')) return 'iOS'
  return 'Unknown'
}

/**
 * 检测系统架构
 */
export function detectArch(): string {
  if (typeof navigator === 'undefined') return 'Unknown'
  // @ts-expect-error - userAgentData may not be available in all browsers
  const uaData = navigator.userAgentData
  if (uaData?.architecture) return uaData.architecture
  const ua = navigator.userAgent
  if (ua.includes('x86_64') || ua.includes('x64') || ua.includes('Win64')) return 'x64'
  if (ua.includes('arm64') || ua.includes('aarch64')) return 'arm64'
  if (ua.includes('i686') || ua.includes('i386')) return 'x86'
  return 'Unknown'
}
