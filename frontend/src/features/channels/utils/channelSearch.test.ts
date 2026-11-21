import { describe, expect, it } from 'vitest'
import type { Channel } from '@/types'
import { channelSearchResults, filterChannelSearch } from '@/features/channels/utils/channelSearch'

const channel = (patch: Partial<Channel> = {}): Channel => ({
  id: 'primary', index: 0, name: '代码服务', serviceType: 'openai', poolId: 'code', baseUrl: 'https://example.invalid', apiKeys: ['fixture-secret'], status: 'active', ...patch
})
const pools = [{ id: 'code', name: '代码分组', modelMatcher: 'gpt', priority: 0 }]

describe('跨协议渠道搜索', () => {
  it('合并验证组合筛选、同名渠道、敏感字段保护和无效引用提示', () => {
    const results = [
      ...channelSearchResults('messages', [channel()], pools),
      ...channelSearchResults('responses', [channel({ modelMapping: { codex: ['gpt-model'] } })], pools)
    ]
    expect(filterChannelSearch(results, 'Responses 代码分组 GPT-MODEL')).toEqual([expect.objectContaining({ protocol: 'responses', id: 'primary', name: '代码服务', poolName: '代码分组' })])
    expect(filterChannelSearch(results, '代码服务')).toHaveLength(2)
    expect(filterChannelSearch(results, 'codex missing')).toEqual([])

    const states = channelSearchResults('chat', [
      channel({ id: 'inactive', status: 'disabled' }), channel({ id: 'deprecated', status: 'deprecated' }), channel({ id: 'removed', status: 'deleted' })
    ], pools)
    expect(states.map(result => result.location)).toEqual(['备用渠道', '弃用渠道'])
    expect(filterChannelSearch(states, '备用渠道')[0]?.id).toBe('inactive')
    expect(JSON.stringify(states)).not.toContain('fixture-secret')
    expect(JSON.stringify(states)).not.toContain('example.invalid')
    expect(channelSearchResults('chat', [channel()], [])[0]?.poolName).toBe('未登记分组（code）')
    expect(() => channelSearchResults('chat', [channel({ id: undefined })], pools)).toThrow('缺少稳定 ID')
  })
})
