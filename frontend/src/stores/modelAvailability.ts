import { defineStore } from 'pinia'
import { shallowRef } from 'vue'
import type { ApiTab } from '@/services/channel-api'
import type { ModelAvailabilityResponse } from '@/types/model-availability'

// 观测快照按协议隔离；刷新与取消由面板持有，不纳入渠道页的 2 秒轮询。
export const useModelAvailabilityStore = defineStore('modelAvailability', () => {
  const snapshots = shallowRef<Partial<Record<ApiTab, ModelAvailabilityResponse>>>({})

  function replace(type: ApiTab, snapshot: ModelAvailabilityResponse) {
    snapshots.value = { ...snapshots.value, [type]: snapshot }
  }

  return { snapshots, replace }
})
