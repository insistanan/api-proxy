import { defineStore } from 'pinia'
import { ref } from 'vue'
import { freeChannelsApi, type FreeChannelsConfig } from '@/services/free-channels'

// 四个协议入口共用这份已保存配置，禁止在协议页复制并独立修改。
export const useFreeChannelsStore = defineStore('freeChannels', () => {
  const config = ref<FreeChannelsConfig | null>(null)
  const busy = ref(false)
  let revision = 0

  async function refresh() {
    const current = ++revision
    const snapshot = await freeChannelsApi.get()
    if (current === revision) config.value = snapshot
  }

  async function update(action: () => Promise<FreeChannelsConfig>) {
    if (busy.value) throw new Error('免费分组正在更新，请稍候')
    busy.value = true
    ++revision
    try {
      const snapshot = await action()
      ++revision
      config.value = snapshot
    } finally {
      busy.value = false
    }
  }

  return {
    config, busy, refresh,
    setEnabled: (enabled: boolean) => update(() => freeChannelsApi.setEnabled(enabled)),
    syncModels: () => update(freeChannelsApi.syncModels)
  }
})
