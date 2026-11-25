import { defineStore } from 'pinia'
import { ref, shallowRef } from 'vue'
import { ApiError, apiRequest } from '@/shared/services/http'
import type { ForestDay, ForestGroup, ForestIsland, ForestIslandView, ForestTimer, ForestTree, ForestWorld } from '@/types/forest'

export const kinds = ['usage', 'focus', 'rest'] as const
export const labels = { usage: '用量树', focus: '专注树', rest: '休息树' } as const
export const styles = {
  usage: ['圆冠树', '松树', '白桦', '花冠树'],
  focus: ['银枝树', '塔冠树', '双冠树', '灯芯树'],
  rest: ['打呼噜树', '倒立树', '拖鞋树', '躺平树']
}
export const islandName = (island: ForestIsland) => island.name || `岛屿 ${island.id}`
export const treeName = (tree: ForestTree) => tree.name || styles[tree.kind][tree.style]
export const groupOf = (island: number) => Math.floor((island - 1) / 8)

export const useForest = defineStore('usage-forest', () => {
  const world = shallowRef<ForestWorld>()
  const timer = shallowRef<ForestTimer>()
  const receivedAt = ref(0)
  const syncError = ref('')
  let request = 0
  function acceptTimer(value: ForestTimer) {
    const old = timer.value
    if (!old || value.revision > old.revision || (value.revision === old.revision && value.serverNow >= old.serverNow)) {
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
    const value = await apiRequest<ForestTimer>('/forest/timer', {
      method: 'POST', body: JSON.stringify({ action, minutes, revision: timer.value.revision })
    })
    acceptTimer(value)
  }
  async function refreshTimer() {
    try {
      acceptTimer(await apiRequest<ForestTimer>('/forest/timer'))
    } catch (cause) {
      if (!(cause instanceof ApiError && cause.status === 503) || timer.value) {
        syncError.value = `计时同步暂时不可用。 ${cause instanceof Error ? cause.message : String(cause)}`
      }
      throw cause
    }
  }
  return { world, timer, receivedAt, syncError, load, command, refreshTimer }
})

export const forestApi = {
  groups: () => apiRequest<ForestGroup[]>('/forest/groups'),
  islands: (id: number) => apiRequest<ForestIsland[]>(`/forest/groups/${id}`),
  island: (id: number) => apiRequest<ForestIslandView>(`/forest/islands/${id}`),
  day: (date: string, after = 0) => apiRequest<ForestDay>(`/forest/days/${encodeURIComponent(date)}?after=${after}`),
  treeEdit: (id: number, input: { name?: string; style?: number; slot?: number }) =>
    apiRequest<null>(`/forest/trees/${id}`, { method: 'PATCH', body: JSON.stringify(input) }),
  islandEdit: (id: number, input: { name: string; favorite: boolean }) =>
    apiRequest<null>(`/forest/islands/${id}`, { method: 'PATCH', body: JSON.stringify(input) }),
  redeem: (id: string) => apiRequest<null>(`/forest/ornaments/${encodeURIComponent(id)}`, { method: 'POST' })
}
