import type { CacheKeepaliveConfig, CacheKeepaliveStatus } from '@/types/channel'

export const cacheKeepaliveStateLabels: Record<CacheKeepaliveStatus['state'], string> = {
  active: '真实请求处理中',
  waiting: '等待空闲满一分钟',
  preparing: '正在保存上下文',
  ready: '等待下一次心跳',
  sending: '正在保活',
  stopped: '已停止'
}

type NumericField = Exclude<keyof CacheKeepaliveConfig, 'enabled' | 'prompt'>
export type CacheKeepaliveDraft = Pick<CacheKeepaliveConfig, 'enabled' | 'prompt'> & Record<NumericField, number | string | null>

export function supportsCacheKeepalive(kind: string, service: string): boolean {
  if (kind === 'messages' || kind === 'responses') return ['claude', 'openai', 'responses', 'gemini'].includes(service)
  if (kind === 'chat') return service === 'openai'
  return kind === 'gemini' && ['gemini', 'claude', 'openai'].includes(service)
}

export function defaultCacheKeepalive(): CacheKeepaliveConfig {
  return {
    enabled: false,
    intervalSeconds: 280,
    maxAttempts: 6,
    maxIdleMinutes: 30,
    prompt: '接下来应该怎么做？',
    minCacheHitPercent: 90,
    maxOutputTokens: 16
  }
}

export function validateCacheKeepalive(draft: CacheKeepaliveDraft): string {
  if (!draft.enabled) return ''
  const ranges: [NumericField, number, number, string][] = [
    ['intervalSeconds', 180, 3540, '心跳间隔须为 180–3540 秒'],
    ['maxAttempts', 0, 10000, '最多发送次数须为 0–10000'],
    ['maxIdleMinutes', 0, 1440, '最长持续时间须为 0–1440 分钟'],
    ['minCacheHitPercent', 1, 100, '最低命中保持比例须为 1–100%'],
    ['maxOutputTokens', 1, 4096, '单次输出上限须为 1–4096 Token']
  ]
  for (const [field, min, max, message] of ranges) {
    const value = draft[field]
    const numeric = Number(value)
    if (value === null || String(value).trim() === '' || !Number.isInteger(numeric) || numeric < min || numeric > max) return message
  }
  if (Number(draft.maxAttempts) === 0 && Number(draft.maxIdleMinutes) === 0) return '次数和持续时间至少设置一个上限'
  if (!draft.prompt.trim() || new TextEncoder().encode(draft.prompt).length > 2048) return '心跳内容不能为空，且不能超过 2048 字节'
  return ''
}

export function buildCacheKeepalive(draft: CacheKeepaliveDraft, supported: boolean): CacheKeepaliveConfig {
  const error = validateCacheKeepalive(draft)
  if (error) throw new Error(error)
  // 关闭功能也保留用户的有效参数，重新开启时无需重新填写。
  // 未完成的数字输入不能被 Number('') 或 JSON 的 NaN -> null 静默改写。
  for (const field of ['intervalSeconds', 'maxAttempts', 'maxIdleMinutes', 'minCacheHitPercent', 'maxOutputTokens'] as const) {
    const value = draft[field]
    if (value === null || String(value).trim() === '' || !Number.isInteger(Number(value))) {
      throw new Error('缓存保活参数包含未完成的数字，请补全后保存')
    }
  }
  return {
    enabled: supported && draft.enabled,
    intervalSeconds: Number(draft.intervalSeconds),
    maxAttempts: Number(draft.maxAttempts),
    maxIdleMinutes: Number(draft.maxIdleMinutes),
    prompt: draft.prompt,
    minCacheHitPercent: Number(draft.minCacheHitPercent),
    maxOutputTokens: Number(draft.maxOutputTokens)
  }
}
