// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { pendingProjectTranslations } from '@/features/extensions/skills/composables/translationBatch'
import type { ManagedSkill } from '@/types'

function skill(name: string, overrides: Partial<ManagedSkill> = {}): ManagedSkill {
  return {
    name, locationKey: 'project', agent: '项目 Skills', sourceType: 'project', valid: true,
    description: '说明', path: `/skills/${name}`, size: 0, modifiedAt: '', readOnly: false, ...overrides
  }
}

describe('批量 Skill 翻译', () => {
  it('预览只计入有效的项目技能，按标识去重并跳过已有译文', () => {
    const targets = pendingProjectTranslations([
      skill('only-local', { locationKey: 'codex' }),
      skill('invalid', { valid: false }),
      skill('pending'),
      skill('pending'),
      skill('completed'),
      skill('completed', { hasTranslation: true }),
      skill('pending', { locationKey: 'codex', hasTranslation: true })
    ])
    expect(targets.map(item => item.name)).toEqual(['pending'])
  })
})
