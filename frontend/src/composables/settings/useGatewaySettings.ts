// gateway 设置的草稿与操作编排；草稿仅供本设置页使用，保存以服务端响应回填。
import { computed, ref, type Ref } from 'vue'
import { api } from '@/services/api'
import type { GatewaySettings } from '@/types'
import type { SettingsNotice as Notice } from './types'

export function useGatewaySettings(saving: Ref<boolean>) {
  // ===== 请求、日志与存储策略 =====
  const gatewayFormRef = ref()
  const gateway = ref<GatewaySettings | null>(null)
  const savedGateway = ref('')
  const gatewayNotice = ref<Notice>(null)
  const gatewayDirty = computed(() => gateway.value !== null && JSON.stringify(gateway.value) !== savedGateway.value)
  const gatewayNumberFields: Array<{
    key: 'requestTimeout' | 'responseHeaderTimeout' | 'streamIdleTimeout' | 'maxRequestBodySizeMB'
    label: string
    min: number
    max: number
  }> = [
    { key: 'requestTimeout', label: '普通请求超时（毫秒）', min: 1, max: 86400000 },
    { key: 'responseHeaderTimeout', label: '等待响应头超时（秒）', min: 30, max: 300 },
    { key: 'streamIdleTimeout', label: '流式空闲超时（秒）', min: 30, max: 3600 },
    { key: 'maxRequestBodySizeMB', label: '请求体最大大小（MB）', min: 1, max: 1024 }
  ]
  const integerRangeRule = (min: number, max: number) => (value: unknown) => {
    const number = Number(value)
    return value !== '' && value !== null && Number.isInteger(number) && number >= min && number <= max || `请输入 ${min} 至 ${max} 之间的整数`
  }
  const nonEmptyHeaderRule = (value: string) => !!value.trim() && !value.includes('\r') && !value.includes('\n') && !value.includes('\0') || '不能为空或包含控制字符'

  const saveGatewaySettings = async () => {
    const current = gateway.value
    if (!current) {
      gatewayNotice.value = { type: 'error', message: '请求与日志设置尚未加载' }
      return
    }
    const { valid } = await gatewayFormRef.value.validate()
    if (!valid) return
    saving.value = true
    gatewayNotice.value = null
    try {
      const settings = await api.updateSettings({ gateway: { ...current } })
      if (!settings.gateway) throw new Error('服务端未返回请求与日志设置，请刷新确认保存状态')
      gateway.value = settings.gateway
      savedGateway.value = JSON.stringify(settings.gateway)
      gatewayNotice.value = { type: 'success', message: '设置已保存；日志路径和指标持久化开关在重启后生效' }
    } catch (error) {
      gatewayNotice.value = { type: 'error', message: error instanceof Error ? error.message : '保存请求与日志设置失败' }
    } finally {
      saving.value = false
    }
  }

  return {
    gatewayFormRef,
    gateway,
    savedGateway,
    gatewayNotice,
    gatewayDirty,
    gatewayNumberFields,
    integerRangeRule,
    nonEmptyHeaderRule,
    saveGatewaySettings,
    saving
  }
}
