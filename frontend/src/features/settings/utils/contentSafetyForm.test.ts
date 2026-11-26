import { describe, expect, it } from 'vitest'
import { defaultContentSafety, normalizeContentSafety } from '@/features/settings/utils/contentSafetyForm'

describe('内容安全总开关', () => {
  it('默认关闭，与处置模式分开保存', () => {
    const settings = defaultContentSafety()
    expect(settings.enabled).toBe(false)
    settings.sensitiveData.mode = 'mask'
    expect(normalizeContentSafety(settings).enabled).toBe(false)
  })

  it('关闭后保留规则，重新开启不丢失草稿', () => {
    const settings = defaultContentSafety()
    settings.enabled = true
    settings.sensitiveData.mode = 'mask'
    settings.dangerousCmd.enabled = true
    settings.dangerousCmd.enabledRules = ['destructive']
    settings.dangerousCmd.aiGate.enabled = true
    const configured = normalizeContentSafety(settings)
    configured.enabled = false
    const disabled = normalizeContentSafety(configured)
    expect(disabled.enabled).toBe(false)
    expect(disabled.dangerousCmd).toEqual(settings.dangerousCmd)
    expect(disabled.sensitiveData.mode).toBe('mask')
    disabled.enabled = true
    expect(normalizeContentSafety(disabled)).toEqual({ ...configured, enabled: true })
  })
})
