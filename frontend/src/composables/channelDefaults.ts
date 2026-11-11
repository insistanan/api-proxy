import type { ApiTab } from '@/services/api'

/**
 * 客户端配置"连接本代理 + 一键导入模型"的统一默认值表。
 *
 * 设计取舍：
 * - 思考默认开、等级默认 high：减少用户配置成本，按需可调低。
 * - 识图默认开：本项目有完备的图片理解层兜底（visionlayer），即使上游不原生支持也能自动分流。
 * - 按协议给 contextLimit / outputLimit 合理默认值，避免每页各自硬编码。
 */

export interface ProtocolDefaults {
  /** 上下文窗口（tokens） */
  contextLimit: number
  /** 最大输出（tokens） */
  outputLimit: number
  /** 最大输入（tokens），OpenCode 用 */
  inputLimit: number
}

/** 按渠道协议类型的默认值表 */
export const PROTOCOL_DEFAULTS: Record<ApiTab, ProtocolDefaults> = {
  messages: { contextLimit: 220000, outputLimit: 32000, inputLimit: 200000 },
  responses: { contextLimit: 220000, outputLimit: 32000, inputLimit: 200000 },
  gemini: { contextLimit: 1000000, outputLimit: 65536, inputLimit: 900000 },
  chat: { contextLimit: 220000, outputLimit: 32000, inputLimit: 200000 },
  images: { contextLimit: 220000, outputLimit: 32000, inputLimit: 200000 },
  // 透传协议的模型没有统一上下文窗口概念，沿用通用对话默认值。
  embeddings: { contextLimit: 220000, outputLimit: 32000, inputLimit: 200000 },
  decisions: { contextLimit: 220000, outputLimit: 32000, inputLimit: 200000 }
}
