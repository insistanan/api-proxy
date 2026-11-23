import { describe, expect, it } from 'vitest'
import { runTranslationBatch } from '@/features/extensions/skills/composables/translationBatch'

describe('批量 Skill 翻译', () => {
  it('严格串行，走完全部首轮后才重试失败项，并保留再次失败的结果', async () => {
    const calls: string[] = []
    const attempts = new Map<string, number>()
    let concurrent = 0
    let maxConcurrent = 0
    const result = await runTranslationBatch(['a', 'b', 'c'], async skill => {
      calls.push(skill)
      concurrent++
      maxConcurrent = Math.max(maxConcurrent, concurrent)
      await Promise.resolve()
      concurrent--
      const attempt = (attempts.get(skill) || 0) + 1
      attempts.set(skill, attempt)
      if (skill === 'a' && attempt === 1) throw new Error('临时失败')
      if (skill === 'b') throw new Error('持续失败')
    }, () => {})
    expect(calls).toEqual(['a', 'b', 'c', 'a', 'b'])
    expect(maxConcurrent).toBe(1)
    expect(result.succeeded).toBe(2)
    expect(result.failures).toHaveLength(1)
    expect(result.failures[0].target).toBe('b')
    expect(result.failures[0].error).toEqual(new Error('持续失败'))
  })

  it('已落盘错误不重复执行，也不会阻断后续条目', async () => {
    const committedError = new Error('已落盘，请刷新核对')
    const calls: string[] = []
    const result = await runTranslationBatch(['a', 'b'], async skill => {
      calls.push(skill)
      if (skill === 'a') throw committedError
    }, () => {}, error => error !== committedError)
    expect(calls).toEqual(['a', 'b'])
    expect(result.succeeded).toBe(1)
    expect(result.failures).toEqual([{ target: 'a', error: committedError }])
  })
})
