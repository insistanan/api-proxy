import { defineStore } from 'pinia'
import { ref, shallowRef } from 'vue'
import { ApiError, apiRequest } from '@/shared/services/http'
import type {
  ForestDay,
  ForestEvent,
  ForestEventFeed,
  ForestEventEffects,
  ForestGroup,
  ForestIsland,
  ForestIslandView,
  ForestResources,
  ForestTimer,
  ForestTree,
  ForestWorld
} from '@/types/forest'

export const kinds = ['usage', 'focus', 'rest'] as const
export const labels = { usage: '常青林', focus: '月光林', rest: '奇趣林' } as const
export const origins = {
  usage: '在灵感流动时萌芽',
  focus: '长在认真投入的时光里',
  rest: '在好好休息之后来到岛上'
} as const
export const styles = {
  usage: ['圆冠树', '松树', '白桦', '花冠树', '枫糖树', '星垂柳'],
  focus: ['银枝树', '塔冠树', '双冠树', '灯芯树', '月灯树'],
  rest: ['打呼噜树', '倒立树', '拖鞋树', '躺平树', '甜甜圈树']
}
export const islandName = (island: ForestIsland) => island.name || `岛屿 ${island.id}`
export const treeName = (tree: ForestTree) => tree.name || styles[tree.kind][tree.style]
export const conditionNames = { healthy: '枝叶舒展', damaged: '枝叶受损', destroyed: '只剩残桩' } as const
export const resourceText = (value: ForestResources) =>
  [value.wood && `${value.wood} 枝木`, value.seeds && `${value.seeds} 花种`, value.dew && `${value.dew} 晨露`]
    .filter(Boolean)
    .join(' · ')
export function effectItems(effects: ForestEventEffects) {
  return [
    { value: effects.points, label: '成就点' },
    { value: effects.wood, label: '枝木' },
    { value: effects.seeds, label: '花种' },
    { value: effects.dew, label: '晨露' },
    { value: -effects.damaged, label: '棵树受损' },
    { value: -effects.destroyed, label: '棵树被毁' },
    { value: effects.restored, label: '棵树恢复' }
  ]
    .filter(item => item.value)
    .map(item => ({
      ...item,
      loss: item.value < 0,
      text: `${item.value > 0 ? '+' : '−'}${Math.abs(item.value)} ${item.label}`
    }))
}
export const groupOf = (island: number) => Math.floor((island - 1) / 8)

export const useForest = defineStore('usage-forest', () => {
  const world = shallowRef<ForestWorld>()
  const timer = shallowRef<ForestTimer>()
  const receivedAt = ref(0)
  const syncError = ref('')
  const newsError = ref('')
  let request = 0
  let lifetime = 0
  function acceptTimer(value: ForestTimer) {
    const old = timer.value
    if (
      !old ||
      value.revision > old.revision ||
      (value.revision === old.revision && value.serverNow >= old.serverNow)
    ) {
      timer.value = value
      receivedAt.value = Date.now()
      syncError.value = ''
    }
  }
  async function load() {
    const current = ++request
    const value = await apiRequest<ForestWorld>('/forest/world')
    if (current === request) {
      acceptTimer(value.timer)
      world.value = value
    }
    return value
  }
  async function command(action: string, minutes = 0) {
    if (!timer.value) throw new Error('计时状态尚未加载')
    const generation = lifetime
    const value = await apiRequest<ForestTimer>('/forest/timer', {
      method: 'POST',
      body: JSON.stringify({ action, minutes, revision: timer.value.revision })
    })
    if (generation === lifetime) acceptTimer(value)
  }
  async function refreshTimer() {
    const generation = lifetime
    try {
      const value = await apiRequest<ForestTimer>('/forest/timer')
      if (generation === lifetime) acceptTimer(value)
    } catch (cause) {
      if (generation === lifetime && (!(cause instanceof ApiError && cause.status === 503) || timer.value)) {
        syncError.value = `计时同步暂时不可用。 ${cause instanceof Error ? cause.message : String(cause)}`
      }
      throw cause
    }
  }
  function reset() {
    lifetime++
    request++
    world.value = undefined
    timer.value = undefined
    receivedAt.value = 0
    syncError.value = ''
    newsError.value = ''
  }
  return { world, timer, receivedAt, syncError, newsError, load, command, refreshTimer, reset }
})

export const forestApi = {
  visit: () => apiRequest<null>('/forest/visit', { method: 'POST' }),
  recover: (tree: ForestTree) =>
    apiRequest<null>(`/forest/trees/${tree.id}/recover`, {
      method: 'POST',
      body: JSON.stringify({ revision: tree.conditionRevision })
    }),
  events: (after = 0) => apiRequest<ForestEventFeed>(`/forest/events?after=${after}`),
  event: (id: string) => apiRequest<ForestEvent>(`/forest/events/${encodeURIComponent(id)}`),
  tree: (id: number) => apiRequest<ForestTree>(`/forest/trees/${id}`),
  groups: () => apiRequest<ForestGroup[]>('/forest/groups'),
  islands: (id: number) => apiRequest<ForestIsland[]>(`/forest/groups/${id}`),
  island: (id: number) => apiRequest<ForestIslandView>(`/forest/islands/${id}`),
  day: (date: string, after = 0) => apiRequest<ForestDay>(`/forest/days/${encodeURIComponent(date)}?after=${after}`),
  treeEdit: (id: number, input: { name?: string; style?: number; slot?: number; accessory?: string }) =>
    apiRequest<null>(`/forest/trees/${id}`, { method: 'PATCH', body: JSON.stringify(input) }),
  islandEdit: (id: number, input: { name: string; favorite: boolean }) =>
    apiRequest<null>(`/forest/islands/${id}`, { method: 'PATCH', body: JSON.stringify(input) }),
  redeem: (id: string) => apiRequest<null>(`/forest/ornaments/${encodeURIComponent(id)}`, { method: 'POST' })
}
