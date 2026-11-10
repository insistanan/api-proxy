// HTTP 基础层：统一鉴权、响应解析和已提交错误语义；业务端点在领域服务中声明。
import { useAuthStore } from '@/stores/auth'

export class ApiError extends Error {
  readonly status: number
  readonly details?: unknown
  /** 业务错误码（如 CONFIG_COMMITTED：配置已原子提交但收尾失败）。 */
  readonly code?: string
  /**
   * 配置写操作是否已过提交点（文件已写入，内存与磁盘一致）。
   * 为 true 时不得按"未提交"自动重试（一次新增会被重试成两次），
   * 应提示用户刷新查看实际状态（R3 committed 语义，与后端配套）。
   */
  readonly committed?: boolean

  constructor(
    message: string,
    status: number,
    details?: unknown,
    code?: string,
    committed?: boolean
  ) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.details = details
    this.code = code
    this.committed = committed
  }
}

// 从环境变量读取配置
const getApiBase = () => {
  // 在生产环境中，API调用会直接请求当前域名
  if (import.meta.env.PROD) {
    return '/api'
  }

  // 在开发环境中，支持从环境变量配置后端地址
  const backendUrl = import.meta.env.VITE_BACKEND_URL
  const apiBasePath = import.meta.env.VITE_API_BASE_PATH || '/api'

  if (backendUrl) {
    return `${backendUrl}${apiBasePath}`
  }

  // fallback到默认配置
  return '/api'
}

const API_BASE = getApiBase()

// 打印当前API配置（仅开发环境）
if (import.meta.env.DEV) {
  console.log('🔗 API Configuration:', {
    API_BASE,
    BACKEND_URL: import.meta.env.VITE_BACKEND_URL,
    IS_DEV: import.meta.env.DEV,
    IS_PROD: import.meta.env.PROD
  })
}

function getApiKey(): string | null {
  const authStore = useAuthStore()
  return authStore.apiKey
}

async function parseResponseBody(response: Response): Promise<unknown> {
  const text = await response.text()
  if (!text) return null
  try {
    return JSON.parse(text)
  } catch {
    return text
  }
}

export async function apiRequest<T = unknown>(url: string, options: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options.headers as Record<string, string>)
  }

  // 从 AuthStore 获取 API 密钥并添加到请求头
  const apiKey = getApiKey()
  if (apiKey) {
    headers['x-api-key'] = apiKey
  }

  const response = await fetch(`${API_BASE}${url}`, {
    ...options,
    headers
  })

  if (!response.ok) {
    const errorBody = await parseResponseBody(response)
    const errorMessage =
      (typeof errorBody === 'object' &&
      errorBody &&
      'error' in errorBody &&
      typeof (errorBody as { error?: unknown }).error === 'string'
        ? (errorBody as { error: string }).error
        : typeof errorBody === 'object' &&
            errorBody &&
            'error' in errorBody &&
            typeof (errorBody as { error?: { message?: unknown } }).error?.message === 'string'
          ? (errorBody as { error: { message: string } }).error.message
          : typeof errorBody === 'object' &&
              errorBody &&
              'message' in errorBody &&
              typeof (errorBody as { message?: unknown }).message === 'string'
            ? (errorBody as { message: string }).message
            : typeof errorBody === 'string'
              ? errorBody
              : null) || `Request failed (${response.status})`

    // 如果是401错误，清除认证信息并提示用户重新登录
    if (response.status === 401) {
      const authStore = useAuthStore()
      authStore.clearAuth()
      // 记录认证失败(前端日志)
      if (import.meta.env.DEV) {
        console.warn('🔒 认证失败 - 时间:', new Date().toISOString())
      }
      throw new ApiError('认证失败，请重新输入访问密钥', response.status, errorBody)
    }

    // 提取后端的业务错误码与已提交标记（R3 committed 语义）。
    const extractField = <T>(field: string): T | undefined => {
      if (typeof errorBody === 'object' && errorBody !== null && field in errorBody) {
        return (errorBody as Record<string, unknown>)[field] as T
      }
      return undefined
    }
    const errorCode = extractField<string>('code')
    const committed = extractField<boolean>('committed')

    throw new ApiError(errorMessage, response.status, errorBody, errorCode, committed)
  }

  if (response.status === 204) return null as T
  return await parseResponseBody(response) as T
}
