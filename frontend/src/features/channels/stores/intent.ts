import { defineStore } from 'pinia'
import { shallowRef } from 'vue'
import { intentApi, type IntentConfig, type IntentProtocol } from '@/features/channels/services/intent'
import { ApiError } from '@/shared/services/http'

// 保存后的协议配置由 store 持有；编辑器只使用它的副本。
export const useIntentStore = defineStore('intent', () => {
  const configs = shallowRef<Partial<Record<IntentProtocol, IntentConfig>>>({})
  const revisions: Partial<Record<IntentProtocol, number>> = {}
  const saving = new Set<IntentProtocol>()
  let lifetime = 0
  async function load(protocol: IntentProtocol) {
    const revision = (revisions[protocol] ?? 0) + 1
    revisions[protocol] = revision
    try {
      const value = await intentApi.get(protocol)
      if (revisions[protocol] === revision) configs.value = { ...configs.value, [protocol]: value }
      return value
    } catch (error) {
      if (revisions[protocol] === revision) configs.value = { ...configs.value, [protocol]: undefined }
      throw error
    }
  }
  async function save(protocol: IntentProtocol, value: IntentConfig) {
    if (saving.has(protocol)) throw new Error('当前协议正在保存，请稍后重试')
    const generation = lifetime
    saving.add(protocol)
    revisions[protocol] = (revisions[protocol] ?? 0) + 1
    try {
      const saved = await intentApi.save(protocol, value)
      if (generation === lifetime) configs.value = { ...configs.value, [protocol]: saved }
    } catch (error) {
      if (generation === lifetime && error instanceof ApiError && error.committed) {
        configs.value = { ...configs.value, [protocol]: undefined }
      }
      throw error
    } finally {
      // 保存期间发起的读取也可能返回旧配置，提交后使其失效。
      if (generation === lifetime) {
        revisions[protocol] = (revisions[protocol] ?? 0) + 1
        saving.delete(protocol)
      }
    }
  }
  function reset() {
    lifetime++
    for (const protocol of Object.keys(revisions) as IntentProtocol[]) revisions[protocol] = (revisions[protocol] ?? 0) + 1
    configs.value = {}
    saving.clear()
  }
  return { configs, load, save, reset }
})
