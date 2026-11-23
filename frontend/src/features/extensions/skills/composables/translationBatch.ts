import { ref } from 'vue'

// 页面切换后任务仍可完成；共享锁防止再次进入页面时启动第二个翻译任务。
export const translationInProgress = ref(false)
export const batchRunning = ref(false)

export class TranslationFailure extends Error {
  constructor(message: string, readonly cause: unknown) {
    super(message)
    this.name = 'TranslationFailure'
  }
}

export async function runTranslationBatch<T>(
  targets: readonly T[],
  translate: (target: T) => Promise<void>,
  progress: (target: T, index: number, total: number, retry: boolean) => void,
  shouldRetry: (error: unknown) => boolean = () => true
): Promise<{ succeeded: number; failures: Array<{ target: T; error: unknown }> }> {
  let succeeded = 0
  let pending = [...targets]
  const failures = new Map<T, unknown>()
  for (let pass = 0; pass < 2; pass++) {
    for (const [index, target] of pending.entries()) {
      progress(target, index + 1, pending.length, pass === 1)
      try {
        await translate(target)
        failures.delete(target)
        succeeded++
      } catch (error) { failures.set(target, error) }
    }
    pending = [...failures].filter(([, error]) => shouldRetry(error)).map(([target]) => target)
  }
  return { succeeded, failures: [...failures].map(([target, error]) => ({ target, error })) }
}
