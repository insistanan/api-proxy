import type { ApiTab, Channel, ChannelPool } from '@/types'
import { API_TAB_LABELS } from '@/features/channels/services/channel-api'

export interface ChannelSearchResult {
  id: string
  protocol: ApiTab
  protocolLabel: string
  poolName: string
  name: string
  models: string[]
  location: string
}

export function channelAnchorId(protocol: ApiTab, id: string) {
  return `channel-${protocol}-${encodeURIComponent(id)}`
}

export function channelSearchResults(protocol: ApiTab, channels: Channel[], pools: ChannelPool[]): ChannelSearchResult[] {
  return channels.filter(channel => channel.status !== 'deleted').map(channel => {
    if (!channel.id) throw new Error(`渠道“${channel.name}”缺少稳定 ID，无法定位`)
    const poolId = channel.poolId || 'default'
    const poolName = pools.find(pool => pool.id === poolId)?.name ?? `未登记分组（${poolId}）`
    const location = channel.builtinProvider ? '免费分组'
      : channel.excludeFromConversation ? '公用纯图片理解池'
        : channel.status === 'disabled' ? '备用渠道'
          : channel.status === 'deprecated' ? '弃用渠道' : ''
    const models = [...new Set([
      channel.defaultModel, channel.responsesModel,
      ...Object.keys(channel.modelMapping ?? {}), ...Object.values(channel.modelMapping ?? {}).flat()
    ].filter((value): value is string => !!value))]
    return { id: channel.id, protocol, protocolLabel: API_TAB_LABELS[protocol], poolName, name: channel.name, models, location }
  })
}

export function filterChannelSearch(results: ChannelSearchResult[], query: string) {
  const tokens = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean)
  if (!tokens.length) return results
  return results.filter(result => {
    const searchable = [result.name, result.protocolLabel, result.poolName, result.location, ...result.models].join(' ').toLocaleLowerCase()
    return tokens.every(token => searchable.includes(token))
  }).sort((left, right) => {
    const nameRank = (result: ChannelSearchResult) => tokens.every(token => result.name.toLocaleLowerCase().includes(token)) ? 0 : 1
    return nameRank(left) - nameRank(right)
  })
}
