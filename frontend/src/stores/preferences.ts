import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { ApiTab } from '@/services/channel-api'

/**
 * 用户偏好设置 Store
 *
 * 职责：
 * - 管理暗色模式偏好（light/dark/auto）
 * - 管理 Fuzzy 模式开关
 * - 管理全局统计面板展开状态
 * - 自动持久化到 localStorage
 */
export type ThemePreference = 'light' | 'dark' | 'auto'

export const usePreferencesStore = defineStore('preferences', () => {
  // ===== 状态 =====

  // 暗色模式偏好
  const darkModePreference = ref<ThemePreference>('auto')

  // 服务端开关按入口协议镜像；加载前保持未知，不使用旧的全局缓存。
  const fuzzyModeByProtocol = ref<Partial<Record<ApiTab, boolean>>>({})

  // 服务端持久化的客户端伪装开关
  const claudeCodeDisguiseEnabled = ref(false)
  const codexDisguiseEnabled = ref(false)

  // 全局统计面板展开状态
  const showGlobalStats = ref(false)

  // ===== 操作方法 =====

  /**
   * 设置暗色模式
   */
  function setDarkMode(mode: ThemePreference) {
    darkModePreference.value = mode
  }

  /**
   * 设置 Fuzzy 模式
   */
  function setFuzzyMode(protocol: ApiTab, enabled: boolean) {
    fuzzyModeByProtocol.value[protocol] = enabled
  }

  function setClientDisguise(protocol: 'messages' | 'responses', enabled: boolean) {
    if (protocol === 'messages') {
      claudeCodeDisguiseEnabled.value = enabled
    } else {
      codexDisguiseEnabled.value = enabled
    }
  }

  return {
    // 状态
    darkModePreference,
    fuzzyModeByProtocol,
    claudeCodeDisguiseEnabled,
    codexDisguiseEnabled,
    showGlobalStats,

    // 方法
    setDarkMode,
    setFuzzyMode,
    setClientDisguise,
  }
}, {
  // 持久化配置
  persist: {
    key: 'api-proxy-preferences',
    // 使用条件判断避免在非浏览器环境（SSR、Node 测试）中崩溃
    storage: typeof window !== 'undefined' ? localStorage : undefined,
  },
})
