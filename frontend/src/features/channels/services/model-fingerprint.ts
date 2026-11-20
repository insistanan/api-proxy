import { apiRequest } from '@/shared/services/http'
import type { FingerprintTask } from '@/types/model-fingerprint'

export function startModelFingerprint(
  channelId: string,
  input: { model: string; urlIndex: number; keyIndex: number; effort: string },
  signal: AbortSignal
): Promise<FingerprintTask> {
  return apiRequest<FingerprintTask>(`/channels/${encodeURIComponent(channelId)}/fingerprint`, {
    method: 'POST',
    body: JSON.stringify(input),
    signal
  })
}

export function getModelFingerprint(channelId: string, signal: AbortSignal): Promise<FingerprintTask | null> {
  return apiRequest<FingerprintTask | null>(`/channels/${encodeURIComponent(channelId)}/fingerprint`, { signal })
}

export function stopModelFingerprint(channelId: string, taskId: string, signal: AbortSignal): Promise<FingerprintTask> {
  return apiRequest<FingerprintTask>(`/channels/${encodeURIComponent(channelId)}/fingerprint/${encodeURIComponent(taskId)}`, {
    method: 'DELETE', signal
  })
}

export function retryModelFingerprintSample(channelId: string, taskId: string, index: number, signal: AbortSignal): Promise<FingerprintTask> {
  return apiRequest<FingerprintTask>(`/channels/${encodeURIComponent(channelId)}/fingerprint/${encodeURIComponent(taskId)}/samples/${index}/retry`, {
    method: 'POST', signal
  })
}

export function retryFailedModelFingerprint(channelId: string, taskId: string, signal: AbortSignal): Promise<FingerprintTask> {
  return apiRequest<FingerprintTask>(`/channels/${encodeURIComponent(channelId)}/fingerprint/${encodeURIComponent(taskId)}/retry`, {
    method: 'POST', signal
  })
}
