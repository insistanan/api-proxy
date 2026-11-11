// CC Switch deeplink 生成：把本项目渠道转换为 ccswitch://v1/import 链接。
//
// 协议参考 cc-switch 源码 src-tauri/src/deeplink/parser.rs（v1）：
// 必填 resource / app / name；endpoint 支持逗号分隔多 URL（首个为主）。
// CC Switch 不经协议注册也能导入：将链接作为命令行参数传给其 exe
// （single-instance 回调扫描 argv），或粘贴到浏览器地址栏（需已注册协议）。

import type { Channel } from '@/types/channel'

/** CC Switch 支持的 provider 应用类型（本工具涉及的子集）。
 *  claude = Anthropic messages 协议；codex = Responses 协议；
 *  gemini = Gemini 协议；opencode = OpenAI chat completions 兼容。 */
export type CcsApp = 'claude' | 'codex' | 'gemini' | 'opencode'

/** 渠道池 → 默认 app。用户在哪个池操作，意图就是把该协议的供应商加进 CC Switch，
 *  与渠道自身的 serviceType（代理侧协议转换目标）无关——
 *  messages 池里挂 openai 上游是代理在做转换，用户要的仍是 claude。 */
export const CCS_APP_BY_KIND: Record<'messages' | 'responses' | 'gemini' | 'chat' | 'images' | 'embeddings' | 'decisions', CcsApp> = {
  messages: 'claude',
  chat: 'claude',
  responses: 'codex',
  gemini: 'gemini',
  images: 'opencode',
  // 两个透传协议没有专属 CC Switch 应用，按 OpenAI 兼容入口处理；
  // 渠道列表已对它们隐藏 CC Switch 导入入口，这里仅保证类型完整。
  embeddings: 'opencode',
  decisions: 'opencode'
}

/** app 类型的界面文案与提示 */
export const CCS_APP_OPTIONS: Array<{ value: CcsApp; title: string; hint: string }> = [
  { value: 'claude', title: 'Claude (Anthropic)', hint: '写入 ANTHROPIC_BASE_URL / ANTHROPIC_AUTH_TOKEN，供 Claude Code 使用' },
  { value: 'codex', title: 'Codex (Responses)', hint: '写入 config.toml（wire_api=responses），供 Codex CLI 使用' },
  { value: 'gemini', title: 'Gemini', hint: '写入 GOOGLE_GEMINI_BASE_URL / GEMINI_API_KEY' },
  { value: 'opencode', title: 'OpenCode (OpenAI 兼容)', hint: '写入 @ai-sdk/openai-compatible 配置' }
]

/** 生成 ccswitch:// 导入链接。app 由调用方显式传入（弹窗里用户可改）。
 *  不传 enabled：仅导入不自动切换为激活供应商。 */
export function buildCcsDeepLink(channel: Channel, app: CcsApp): string {
  const params = new URLSearchParams({
    resource: 'provider',
    app,
    name: channel.name
  })

  // baseUrl 为主端点，baseUrls 是 failover 备选，逗号拼接 CC Switch 原生支持
  const endpoints = [channel.baseUrl, ...(channel.baseUrls ?? [])]
    .map(url => url.trim())
    .filter(Boolean)
  if (endpoints.length > 0) {
    params.set('endpoint', endpoints.join(','))
  }

  // CC Switch 只收单个 key，取第一个
  const firstKey = channel.apiKeys.find(key => key.trim())?.trim()
  if (firstKey) {
    params.set('apiKey', firstKey)
  }
  if (channel.defaultModel?.trim()) {
    params.set('model', channel.defaultModel.trim())
  }
  if (channel.website?.trim()) {
    params.set('homepage', channel.website.trim())
  }
  if (channel.description?.trim()) {
    params.set('notes', channel.description.trim())
  }

  return `ccswitch://v1/import?${params.toString()}`
}
