import { describe, expect, it, vi } from 'vitest'
import { ApiError } from './http'
import { intentPreviewFailure, type IntentDecision } from './intent'
import { defaultContentSafety, normalizeContentSafety } from '@/utils/contentSafetyForm'

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))

describe('意图诊断与 Decisions 兼容', () => {
  it('仅从明确的分类失败响应提取后端快照，不对其他错误补造过程', () => {
    const decision: IntentDecision = { poolId: '', routeId: '', reason: '辅助分类失败', score: 0, tier: -1, durationMs: 20 }
    expect(intentPreviewFailure(new ApiError('分类失败', 502, { decision }, 'INTENT_CLASSIFIER_FAILED'))).toBe(decision)
    expect(intentPreviewFailure(new ApiError('配置无效', 400, { decision }))).toBeUndefined()
    expect(intentPreviewFailure(new Error('网络失败'))).toBeUndefined()
  })
  it('命令闸门旧 System One 配置在草稿中迁移到 Decisions，不修改已保存对象', () => {
    const saved = defaultContentSafety()
    saved.dangerousCmd.aiGate.protocol = 'systemone'
    const draft = normalizeContentSafety(saved)
    expect(draft.dangerousCmd.aiGate.protocol).toBe('decisions')
    expect(saved.dangerousCmd.aiGate.protocol).toBe('systemone')
  })
})
