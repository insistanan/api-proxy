import { ref, watch, onScopeDispose } from 'vue'
import { fetchHealth } from '@/shared/services/health'
import { useToast } from '@/shared/composables/useToast'
import type { ApiTab } from '@/types'

/**
 * 为客户端配置页提供「连接本代理」的共用逻辑。
 *
 * 用户选择 messages、responses、gemini 或 chat 对话协议后，
 * 页面默认填入本代理地址 http://localhost:{port}，端口从 /health 获取，
 * 地址也可以手动修改。模型列表从 /api/proxy-models 读取，再按所选协议筛选。
 *
 * 获取端口失败时明确报错，允许再次选择后重试。
 */

export interface QuickPickTypeOption {
  title: string
  value: ApiTab
}

/** 默认四协议选项 */
export const defaultQuickPickTypeOptions: QuickPickTypeOption[] = [
  { title: 'Messages', value: 'messages' },
  { title: 'Responses', value: 'responses' },
  { title: 'Gemini', value: 'gemini' },
  { title: 'Chat', value: 'chat' }
]

/** 端口缓存（模块级，多个配置页共享一次 /health 请求） */
let portFetchPromise: Promise<number> | null = null

/**
 * 只合并在途请求，完成后释放，避免服务重启后仍使用旧端口。
 */
export function fetchProxyPort(): Promise<number> {
  if (portFetchPromise) return portFetchPromise
  portFetchPromise = fetchHealth()
    .then(health => {
      if (typeof health.port === 'number' && Number.isInteger(health.port) && health.port > 0 && health.port <= 65535) {
        return health.port
      }
      throw new Error('健康接口未返回有效的代理端口')
    })
    .finally(() => { portFetchPromise = null })
  return portFetchPromise
}

/**
 * 获取本代理默认 Base URL（http://localhost:{port}）。
 * 端口读取失败由页面显示，不能生成猜测地址。
 */
export async function fetchProxyBaseUrl(): Promise<string> {
  const port = await fetchProxyPort()
  return `http://localhost:${port}`
}

/**
 * 协议选择 composable。
 *
 * @param onTypeChange 选定协议后的回调（由调用方实现协议映射与 baseUrl 填充）
 */
export function useProxyProtocolPick(
  onTypeChange?: (type: ApiTab, defaultBaseUrl: Promise<string>) => void | Promise<void>
) {
  const selectedType = ref<ApiTab | null>(null)
  const defaultBaseUrl = ref('')
  const loading = ref(false)
  const { showToast } = useToast()
  let revision = 0
  let disposed = false

  // 初始化默认 Base URL
  fetchProxyBaseUrl().then(url => {
    if (!disposed) defaultBaseUrl.value = url
  }).catch(error => { if (!disposed) showToast(`读取代理端口失败：${error instanceof Error ? error.message : String(error)}`, 'error') })

  watch(selectedType, async (type) => {
    const current = ++revision
    if (!type) { loading.value = false; return }
    loading.value = true
    try {
      const url = await fetchProxyBaseUrl()
      if (disposed || revision !== current) return
      defaultBaseUrl.value = url
      await onTypeChange?.(type, Promise.resolve(url))
    } catch (error) {
      if (disposed || revision !== current) return
      selectedType.value = null
      showToast(`读取代理地址失败：${error instanceof Error ? error.message : String(error)}`, 'error')
    } finally {
      if (revision === current) loading.value = false
    }
  })
  onScopeDispose(() => { disposed = true; revision++ })

  return {
    selectedType,
    defaultBaseUrl,
    loading
  }
}
