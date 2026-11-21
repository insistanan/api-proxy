import { describe, expect, it } from 'vitest'
import type { Channel } from '@/types'
import { channelSearchResults, filterChannelSearch } from '@/features/channels/utils/channelSearch'

const channel = (patch: Partial<Channel> = {}): Channel => ({
  id: 'primary', index: 0, name: '代码服务', serviceType: 'openai', poolId: 'code',
  baseUrl: 'https://example.invalid', apiKeys: ['fixture-secret'], status: 'active', ...patch
})
const pools = [{ id: 'code', name: '代码分组', modelMatcher: 'gpt', priority: 0 }]

describe('跨协议渠道搜索', () => {
  it('支持组合协议、分组及映射模型搜索，并保留同名渠道各自的位置', () => {
    const results = [
      ...channelSearchResults('messages', [channel()], pools),
      ...channelSearchResults('responses', [channel({ modelMapping: { codex: ['gpt-model'] } })], pools)
    ]
    expect(filterChannelSearch(results, 'Responses 代码分组 GPT-MODEL')).toEqual([expect.objectContaining({
      protocol: 'responses', id: 'primary', name: '代码服务', poolName: '代码分组'
    })])
    expect(filterChannelSearch(results, '代码服务')).toHaveLength(2)
    expect(filterChannelSearch(results, 'codex missing')).toEqual([])
  })

  it('排除已删除渠道，标注备用及弃用位置，搜索对象不包含连接和密钥', () => {
    const results = channelSearchResults('chat', [
      channel({ id: 'inactive', status: 'disabled' }),
      channel({ id: 'deprecated', status: 'deprecated' }),
      channel({ id: 'removed', status: 'deleted' })
    ], pools)
    expect(results.map(result => result.location)).toEqual(['备用渠道', '弃用渠道'])
    expect(filterChannelSearch(results, '备用渠道')[0]?.id).toBe('inactive')
    expect(JSON.stringify(results)).not.toContain('fixture-secret')
    expect(JSON.stringify(results)).not.toContain('example.invalid')
  })

  it('未登记分组及缺少稳定标识时明确暴露问题', () => {
    expect(channelSearchResults('chat', [channel()], [])[0]?.poolName).toBe('未登记分组（code）')
    expect(() => channelSearchResults('chat', [channel({ id: undefined })], pools)).toThrow('缺少稳定 ID')
  })
})
