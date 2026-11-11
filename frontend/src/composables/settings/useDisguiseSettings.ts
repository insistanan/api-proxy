// disguise 设置的草稿与操作编排；草稿仅供本设置页使用，保存以服务端响应回填。
import { computed, onUnmounted, ref, watch, type Ref } from 'vue'
import { api } from '@/services/api'
import { usePreferencesStore } from '@/stores/preferences'
import type { ClientDisguiseStatus } from '@/types'
import type { SettingsNotice as Notice, SettingsSection } from './types'

export function useDisguiseSettings(saving: Ref<boolean>, activeSection: Ref<SettingsSection>) {
  // 全局伪装开关与渠道页顶部快捷开关共用同一 Pinia 状态，任一处切换后另一处即时同步。
  const preferencesStore = usePreferencesStore()
  // ===== 客户端伪装设置 =====
  const disguiseFormRef = ref()
  const disguiseClaudeVersion = ref('')
  const disguiseCodexVersion = ref('')
  const disguiseAutoUpdate = ref(true)
  const savedDisguiseClaudeVersion = ref('')
  const savedDisguiseCodexVersion = ref('')
  const savedDisguiseAutoUpdate = ref(true)
  const disguiseNotice = ref<Notice>(null)
  const disguiseInfo = ref<ClientDisguiseStatus | null>(null)
  const disguiseStatusError = ref('')
  const disguiseChecking = ref(false)
  const disguiseVersionRule = (value: string) => {
    const version = value.trim()
    return !version || (version.length <= 64 && /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$/.test(version)) || '请输入版本号，例如 2.1.238 或 0.150.1-beta.1'
  }
  const formatSyncTime = (timestamp?: number) => timestamp ? new Date(timestamp).toLocaleString('zh-CN') : '尚未成功查询'

  const disguiseDirty = computed(() =>
    disguiseClaudeVersion.value.trim() !== savedDisguiseClaudeVersion.value ||
    disguiseCodexVersion.value.trim() !== savedDisguiseCodexVersion.value ||
    disguiseAutoUpdate.value !== savedDisguiseAutoUpdate.value
  )

  // 开关本体持久化在服务端配置中，这里只镜像到 preferences store 供两个页面共同渲染。
  const claudeCodeDisguiseEnabled = computed(() => preferencesStore.claudeCodeDisguiseEnabled)
  const codexDisguiseEnabled = computed(() => preferencesStore.codexDisguiseEnabled)
  const disguiseToggleLoading = ref<'messages' | 'responses' | null>(null)

  const loadDisguiseStatus = async () => {
    try {
      const info = await api.getClientDisguise()
      disguiseInfo.value = info
      preferencesStore.setClientDisguise('messages', info.claudeCodeDisguiseEnabled)
      preferencesStore.setClientDisguise('responses', info.codexDisguiseEnabled)
      disguiseStatusError.value = ''
    } catch (error) {
      disguiseInfo.value = null
      disguiseStatusError.value = error instanceof Error ? error.message : '读取伪装状态失败'
    }
  }

  // 设置页开关与渠道页顶部按钮调用同一接口，保证两条入口没有独立状态。
  const setClientDisguiseEnabled = async (protocol: 'messages' | 'responses', enabled: boolean) => {
    if (disguiseToggleLoading.value) return
    const previous = protocol === 'messages' ? claudeCodeDisguiseEnabled.value : codexDisguiseEnabled.value
    disguiseNotice.value = null
    disguiseToggleLoading.value = protocol
    try {
      await api.setClientDisguise(protocol, enabled)
      preferencesStore.setClientDisguise(protocol, enabled)
      if (disguiseInfo.value) {
        if (protocol === 'messages') {
          disguiseInfo.value.claudeCodeDisguiseEnabled = enabled
        } else {
          disguiseInfo.value.codexDisguiseEnabled = enabled
        }
      }
      disguiseNotice.value = {
        type: 'success',
        message: `${protocol === 'messages' ? 'Claude Code' : 'Codex'} 伪装已${enabled ? '启用' : '关闭'}`
      }
    } catch (error) {
      // 保存失败时回写切换前的状态，避免按钮与服务端实际配置不一致。
      preferencesStore.setClientDisguise(protocol, previous)
      disguiseNotice.value = {
        type: 'error',
        message: error instanceof Error ? error.message : '切换客户端伪装失败'
      }
    } finally {
      disguiseToggleLoading.value = null
    }
  }

  const refreshDisguiseVersions = async () => {
    disguiseChecking.value = true
    disguiseStatusError.value = ''
    try {
      await api.refreshClientDisguise()
      disguiseNotice.value = { type: 'success', message: '版本查询已排队，结果会自动刷新' }
      await loadDisguiseStatus()
    } catch (error) {
      disguiseStatusError.value = error instanceof Error ? error.message : '请求版本查询失败'
    } finally {
      disguiseChecking.value = false
    }
  }

  // 仅在伪装设置页刷新后台状态，离开页面时释放定时器。
  let disguisePollTimer: ReturnType<typeof setInterval> | undefined
  watch(activeSection, section => {
    if (disguisePollTimer) clearInterval(disguisePollTimer)
    disguisePollTimer = undefined
    if (section === 'disguise') {
      void loadDisguiseStatus()
      disguisePollTimer = setInterval(() => { void loadDisguiseStatus() }, 5000)
    }
  }, { immediate: true })
  onUnmounted(() => {
    if (disguisePollTimer) clearInterval(disguisePollTimer)
  })

  const saveDisguiseSettings = async () => {
    const { valid } = await disguiseFormRef.value.validate()
    if (!valid) return
    disguiseNotice.value = null
    saving.value = true
    try {
      const settings = await api.updateSettings({
        disguise: {
          claudeCodeVersion: disguiseClaudeVersion.value.trim(),
          codexVersion: disguiseCodexVersion.value.trim(),
          autoUpdateFromRegistry: disguiseAutoUpdate.value
        }
      })
      if (!settings.disguise) throw new Error('服务端未返回伪装设置，请刷新确认保存状态')
      const saved = settings.disguise
      savedDisguiseClaudeVersion.value = saved.claudeCodeVersion
      savedDisguiseCodexVersion.value = saved.codexVersion
      savedDisguiseAutoUpdate.value = saved.autoUpdateFromRegistry
      disguiseClaudeVersion.value = savedDisguiseClaudeVersion.value
      disguiseCodexVersion.value = savedDisguiseCodexVersion.value
      disguiseAutoUpdate.value = savedDisguiseAutoUpdate.value
      disguiseNotice.value = { type: 'success', message: '客户端伪装设置已保存' }
      await loadDisguiseStatus()
    } catch (error) {
      disguiseNotice.value = { type: 'error', message: error instanceof Error ? error.message : '保存伪装设置失败' }
    } finally {
      saving.value = false
    }
  }

  return {
    disguiseFormRef,
    disguiseClaudeVersion,
    disguiseCodexVersion,
    disguiseAutoUpdate,
    claudeCodeDisguiseEnabled,
    codexDisguiseEnabled,
    disguiseToggleLoading,
    setClientDisguiseEnabled,
    savedDisguiseClaudeVersion,
    savedDisguiseCodexVersion,
    savedDisguiseAutoUpdate,
    disguiseNotice,
    disguiseInfo,
    disguiseStatusError,
    disguiseChecking,
    disguiseVersionRule,
    formatSyncTime,
    disguiseDirty,
    loadDisguiseStatus,
    refreshDisguiseVersions,
    saveDisguiseSettings,
    saving
  }
}
