import { describe, expect, it } from 'vitest'
import { createSSRApp, defineComponent, h } from 'vue'
import { renderToString } from '@vue/server-renderer'
import IntentMatchTrace from './IntentMatchTrace.vue'
import type { IntentDecision, IntentEvent } from '@/services/intent'

const decision = (): IntentDecision => ({
  task: 'debugging', tier: 2, poolId: 'original', routeId: 'auto_free', score: 1, reason: '收益不足，保持原组', durationMs: 12,
  trace: {
    input: '<script>任务输入</script>', truncated: false, classified: true, strategy: 'auto', taskSource: 'heuristic', tierSource: 'heuristic',
    taskEvidence: '内置任务规则命中：排查', tierEvidence: '内置难度规则命中：并发', suggestedPoolId: 'builtin-free',
    groups: [
      { poolId: 'original', name: '当时的原组名称', tasks: ['debugging'], tiers: [2], status: 'candidate', reason: '任务类型与难度均满足', cost: 1 },
      { poolId: 'builtin-free', name: '免费分组', tasks: ['debugging'], tiers: [2], status: 'selected', reason: '能力匹配', cost: 0 }
    ]
  }
})
async function render(value: IntentDecision, target?: IntentEvent['target']) {
  const app = createSSRApp(IntentMatchTrace, { decision: value, tasks: [{ id: 'debugging', name: '调试排错', description: '' }], target })
  app.component('VBtn', defineComponent({ setup(_props, { slots }) { return () => h('button', slots.default?.()) } }))
  return renderToString(app)
}

describe('匹配过程快照', () => {
  it('区分分类建议、路由保护和真实执行目标，不把规则分数显示成置信度', async () => {
    const html = await render(decision(), { poolId: 'original', channelId: 'actual-channel', model: 'actual-model' })
    expect(html).toContain('分类建议：免费分组')
    expect(html).toContain('保护后路由：当时的原组名称')
    expect(html).toContain('actual-model')
    expect(html).toContain('actual-channel')
    expect(html).toContain('匹配结果回放 · 非实时推理')
    expect(html).toContain('&lt;script&gt;任务输入&lt;/script&gt;')
    expect(html).not.toContain('<script>任务输入</script>')
    expect(html).not.toContain('100%')
  })
  it('旧记录不按现配置伪造匹配，新驻留轮次明确未分类', async () => {
    const legacy = decision()
    delete legacy.trace
    expect(await render(legacy)).toContain('旧记录未保存输入和匹配过程')
    const residence = decision()
    residence.trace!.classified = false
    residence.trace!.suggestedPoolId = undefined
    const html = await render(residence)
    expect(html).toContain('本轮未重新分类')
    expect(html).toContain('分类建议：未执行分类')
  })
})
