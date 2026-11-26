// HTTP 基础层：统一鉴权、响应解析和已提交错误语义；业务端点在领域服务中声明。
import { useAuthStore } from '@/shared/stores/auth'

export class ApiError extends Error {
  readonly status: number
  readonly details?: unknown
  /** 业务错误码（如 CONFIG_COMMITTED：配置已原子提交但收尾失败）。 */
  readonly code?: string
  /**
   * 配置写操作是否已过提交点（文件已写入，内存与磁盘一致）。
   * 为 true 时不得按"未提交"自动重试（一次新增会被重试成两次），
   * 应提示用户刷新查看实际状态。
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

async function parseResponseBody(response: Response, strict = false): Promise<unknown> {
  const text = await response.text()
  if (!text && !strict) return null
  try {
    const value: unknown = JSON.parse(text)
    if (strict && (value === null || typeof value !== 'object')) throw new Error('期望 JSON 对象或数组')
    return value
  } catch {
    if (strict) throw new ApiError('接口未返回有效的 JSON 对象或数组，请刷新核对服务状态', response.status, undefined, 'INVALID_RESPONSE')
    return text
  }
}

// 管理接口既有字符串错误，也有协议风格的 error.message；只在这一处解析。
function responseErrorMessage(body: unknown): string | undefined {
  if (typeof body === 'string') return body
  if (!body || typeof body !== 'object') return undefined
  if ('error' in body) {
    const error = body.error
    if (typeof error === 'string') return error
    if (error && typeof error === 'object' && 'message' in error && typeof error.message === 'string') {
      return error.message
    }
  }
  if ('message' in body && typeof body.message === 'string') return body.message
  return undefined
}

/** 管理接口的原始响应入口，流式请求也复用相同的鉴权和错误处理。 */
export async function apiStreamRequest(url: string, options: RequestInit = {}): Promise<Response> {
  const authStore = useAuthStore()
  const generation = authStore.generation
  const apiKey = authStore.apiKey
  const headers = new Headers(options.headers)
  if (!headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  const signals = [options.signal, authStore.requestSignal].filter((signal): signal is AbortSignal => !!signal)
  const signal = signals.length ? AbortSignal.any(signals) : undefined
  const assertCurrent = () => {
    signal?.throwIfAborted()
    if (generation !== authStore.generation || apiKey !== authStore.apiKey) throw new DOMException('认证会话已更改', 'AbortError')
  }

  // 从 AuthStore 获取 API 密钥并添加到请求头
  if (apiKey) {
    headers.set('x-api-key', apiKey)
  }

  assertCurrent()
  const response = await fetch(`${API_BASE}${url}`, {
    ...options,
    headers,
    signal
  })
  assertCurrent()

  if (!response.ok) {
    const errorBody = await parseResponseBody(response)
    assertCurrent()
    const errorMessage = responseErrorMessage(errorBody) || `Request failed (${response.status})`

    // 如果是401错误，清除认证信息并提示用户重新登录
    if (response.status === 401) {
      // 待验证凭据交给认证流程处理；业务会话失效统一触发清理。
      if (authStore.isAuthenticated) authStore.clearAuth()
      // 记录认证失败(前端日志)
      if (import.meta.env.DEV) {
        console.warn('🔒 认证失败 - 时间:', new Date().toISOString())
      }
      throw new ApiError('认证失败，请重新输入访问密钥', response.status, errorBody)
    }

    // 提取后端的业务错误码与已提交标记。
    const fields = errorBody && typeof errorBody === 'object' ? errorBody as Record<string, unknown> : {}
    const errorCode = typeof fields.code === 'string' ? fields.code : undefined
    const committed = typeof fields.committed === 'boolean' ? fields.committed : undefined

    throw new ApiError(errorMessage, response.status, errorBody, errorCode, committed)
  }

  return response
}

export async function apiRequest<T = unknown>(url: string, options: RequestInit = {}): Promise<T> {
  const auth = useAuthStore()
  const generation = auth.generation
  const key = auth.apiKey
  const reading = !options.method || ['GET', 'HEAD'].includes(options.method.toUpperCase())
  const signals = [options.signal, auth.requestSignal, reading ? AbortSignal.timeout(30_000) : undefined]
    .filter((signal): signal is AbortSignal => !!signal)
  const signal = signals.length ? AbortSignal.any(signals) : undefined
  const response = await apiStreamRequest(url, { ...options, signal })
  const result = response.status === 204 ? null : await parseResponseBody(response, true)
  signal?.throwIfAborted()
  if (auth.generation !== generation || auth.apiKey !== key) throw new DOMException('认证会话已更改', 'AbortError')
  return result as T
}

/** 写入已完成和读取刷新失败必须在提示中保留提交边界。 */
export function apiErrorText(error: unknown): string {
  let message = error instanceof Error ? error.message : String(error)
  if (error instanceof ApiError && error.details && typeof error.details === 'object' && 'detail' in error.details && typeof error.details.detail === 'string') message += `：${error.details.detail}`
  return error instanceof ApiError && error.committed
    ? `${message}（变更已写入，请刷新核对实际状态，请勿重复提交）`
    : message
}
