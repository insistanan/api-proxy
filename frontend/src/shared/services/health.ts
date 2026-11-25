// 公开健康端点：用于版本与运行状态查询。
// 健康检查响应类型
export interface HealthResponse {
  /** 平台来自网关进程；远程访问时不使用浏览器设备推断安装包。 */
  platform?: { os: string; arch: string }
  version?: {
    version: string
    buildTime: string
    gitCommit: string
  }
  timestamp: string
  uptime: number
  mode: string
  /** 本代理监听端口，用于客户端配置页拼装默认 Base URL（http://localhost:{port}） */
  port?: number
}

/**
 * 获取健康检查信息（包含版本号）
 * 注意：/health 端点不需要认证，直接请求根路径
 */
export const fetchHealth = async (): Promise<HealthResponse> => {
  const baseUrl = import.meta.env.PROD ? '' : import.meta.env.VITE_BACKEND_URL || ''
  const response = await fetch(`${baseUrl}/health`)
  if (!response.ok) {
    throw new Error(`Health check failed: ${response.status}`)
  }
  return response.json()
}
