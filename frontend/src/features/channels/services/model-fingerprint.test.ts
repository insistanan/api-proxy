import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiRequest } from '@/shared/services/http'
import { getModelFingerprint, startModelFingerprint, stopModelFingerprint, retryModelFingerprintSample, retryFailedModelFingerprint } from '@/features/channels/services/model-fingerprint'

vi.mock('@/shared/services/http', () => ({ apiRequest: vi.fn() }))

const input = { model: 'selected', urlIndex: 1, keyIndex: 2, effort: 'low' }
const result = { id: 'task-id', status: 'running' }

beforeEach(() => { vi.mocked(apiRequest).mockReset() })

describe('指纹检测后台任务', () => {
  it('提交固定采样目标后立即取得任务，不依赖持续连接', async () => {
    vi.mocked(apiRequest).mockResolvedValue(result)
    const controller = new AbortController()
    expect(await startModelFingerprint('stable-id', input, controller.signal)).toEqual(result)
    expect(apiRequest).toHaveBeenCalledWith('/channels/stable-id/fingerprint', expect.objectContaining({
      method: 'POST', signal: controller.signal, body: JSON.stringify(input)
    }))
  })

  it('重新查看使用 GET，不重复提交采样', async () => {
    vi.mocked(apiRequest).mockResolvedValue(result)
    const signal = new AbortController().signal
    expect(await getModelFingerprint('stable-id', signal)).toEqual(result)
    expect(apiRequest).toHaveBeenCalledWith('/channels/stable-id/fingerprint', { signal })
  })

  it('仅显式停止操作取消指定任务', async () => {
    vi.mocked(apiRequest).mockResolvedValue({ ...result, status: 'cancelled' })
    const signal = new AbortController().signal
    await stopModelFingerprint('stable-id', 'task-id', signal)
    expect(apiRequest).toHaveBeenCalledWith('/channels/stable-id/fingerprint/task-id', { method: 'DELETE', signal })
  })

  it('读取失败显式抛错，不把任务当作完成或不存在', async () => {
    vi.mocked(apiRequest).mockRejectedValue(new Error('读取任务失败'))
    await expect(getModelFingerprint('stable-id', new AbortController().signal)).rejects.toThrow('读取任务失败')
  })

  it('重试指定样本使用原任务 ID，不提交新的模型、密钥或档位', async () => {
    vi.mocked(apiRequest).mockResolvedValue(result)
    const signal = new AbortController().signal
    await retryModelFingerprintSample('stable-id', 'task-id', 1, signal)
    expect(apiRequest).toHaveBeenCalledWith('/channels/stable-id/fingerprint/task-id/samples/1/retry', { method: 'POST', signal })
  })

  it('批量补测使用原任务的失败项入口，不创建新任务或提交当前表单', async () => {
    vi.mocked(apiRequest).mockResolvedValue(result)
    const signal = new AbortController().signal
    await retryFailedModelFingerprint('stable-id', 'task-id', signal)
    expect(apiRequest).toHaveBeenCalledWith('/channels/stable-id/fingerprint/task-id/retry', { method: 'POST', signal })
    expect(apiRequest).toHaveBeenCalledTimes(1)
  })
})
