import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/shared/services/http'
import { intentApi, intentPreviewFailure, type IntentDecision } from '@/features/channels/services/intent'
import { defaultContentSafety, normalizeContentSafety } from '@/features/settings/utils/contentSafetyForm'

vi.mock('@/shared/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))
afterEach(() => { vi.unstubAllGlobals() })

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
  it('预设安装仅发送 ID，409 保留向量引导错误码与绝对安装路径', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ready: true }), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await intentApi.installPreset('cpu')
    expect(fetchMock).toHaveBeenCalledWith('/api/intent/local/install-preset', expect.objectContaining({ method: 'POST', body: '{"id":"cpu"}' }))
    const details = { code: 'VECTOR_MODEL_REQUIRED', message: '模型尚未就绪', installPath: 'D:\\gateway\\.config\\intent-embedding', presetsUrl: 'https://example.invalid/presets.json' }
    fetchMock.mockResolvedValue(new Response(JSON.stringify(details), { status: 409 }))
    await expect(intentApi.localAction('start')).rejects.toMatchObject({ status: 409, code: 'VECTOR_MODEL_REQUIRED', details })
  })
})
