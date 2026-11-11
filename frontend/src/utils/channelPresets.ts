import type { ApiTab } from '@/services/channel-api'
import type { Channel } from '@/types/channel'

export type PresetProtocol = 'messages' | 'responses' | 'chat' | 'gemini'
export interface ChannelPresetEndpoint {
  protocol: PresetProtocol
  serviceType: Channel['serviceType']
  url: string
  useFullUrl: boolean
}
export interface ChannelPreset {
  id: string
  name: string
  mark: string
  region?: string
  website: string
  docs: string
  endpoints: ChannelPresetEndpoint[]
}

const endpoint = (protocol: PresetProtocol, url: string, useFullUrl = true): ChannelPresetEndpoint => ({
  protocol, url, useFullUrl,
  serviceType: protocol === 'messages' ? 'claude' : protocol === 'chat' ? 'openai' : protocol
})

// 只维护官方公开接口，不绑定具体模型。完整路径避免自动拼接出错误的 /v1；Gemini 路径由模型动态生成。
// 接口依据各条 docs 的官方文档核对，更新厂商接入方式时修改此表即可。
export const CHANNEL_PRESETS: ChannelPreset[] = [
  {
    id: 'deepseek', name: 'DeepSeek', mark: 'D', website: 'https://platform.deepseek.com',
    docs: 'https://api-docs.deepseek.com/',
    endpoints: [endpoint('chat', 'https://api.deepseek.com/chat/completions'), endpoint('messages', 'https://api.deepseek.com/anthropic/v1/messages'), endpoint('responses', 'https://api.deepseek.com/responses')]
  },
  {
    id: 'openai', name: 'OpenAI', mark: 'O', website: 'https://platform.openai.com',
    docs: 'https://developers.openai.com/api/docs/quickstart',
    endpoints: [endpoint('responses', 'https://api.openai.com/v1/responses'), endpoint('chat', 'https://api.openai.com/v1/chat/completions')]
  },
  {
    id: 'anthropic', name: 'Anthropic', mark: 'A', website: 'https://platform.claude.com',
    docs: 'https://platform.claude.com/docs/en/api/overview',
    endpoints: [endpoint('messages', 'https://api.anthropic.com/v1/messages')]
  },
  {
    id: 'google', name: 'Google Gemini', mark: 'G', website: 'https://aistudio.google.com',
    docs: 'https://ai.google.dev/gemini-api/docs/openai',
    endpoints: [endpoint('gemini', 'https://generativelanguage.googleapis.com/v1beta', false), endpoint('chat', 'https://generativelanguage.googleapis.com/v1beta/openai/chat/completions')]
  },
  {
    id: 'moonshot', name: 'Kimi · Moonshot', mark: 'K', region: '国内', website: 'https://platform.moonshot.cn',
    docs: 'https://platform.moonshot.cn/docs/api',
    endpoints: [endpoint('chat', 'https://api.moonshot.cn/v1/chat/completions'), endpoint('messages', 'https://api.moonshot.cn/anthropic/v1/messages')]
  },
  {
    id: 'zhipu', name: '智谱 GLM', mark: 'Z', region: '按量 API', website: 'https://open.bigmodel.cn',
    docs: 'https://docs.bigmodel.cn/cn/guide/develop/claude/introduction',
    endpoints: [endpoint('chat', 'https://open.bigmodel.cn/api/paas/v4/chat/completions'), endpoint('messages', 'https://open.bigmodel.cn/api/anthropic/v1/messages')]
  },
  {
    id: 'qwen', name: '通义千问 · 百炼', mark: 'Q', region: '北京', website: 'https://bailian.console.aliyun.com',
    docs: 'https://help.aliyun.com/zh/model-studio/base-url',
    endpoints: [endpoint('chat', 'https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions'), endpoint('messages', 'https://dashscope.aliyuncs.com/apps/anthropic/v1/messages')]
  }
]

export const presetProtocolLabels: Record<PresetProtocol, string> = {
  messages: 'Messages', responses: 'Responses', chat: 'Chat', gemini: 'Gemini'
}

// 能力按现有转换器声明；不能把 Chat 入口伪装成已经支持 Messages/Responses 上游。
const supportedProtocols: Partial<Record<ApiTab, PresetProtocol[]>> = {
  messages: ['messages', 'responses', 'chat', 'gemini'],
  responses: ['responses', 'messages', 'chat'],
  gemini: ['gemini', 'messages', 'chat'],
  chat: ['chat']
}

export function resolveChannelPreset(preset: ChannelPreset, entry: ApiTab): ChannelPresetEndpoint | undefined {
  const supported = supportedProtocols[entry] ?? []
  return preset.endpoints.find(item => item.protocol === entry)
    ?? preset.endpoints.find(item => supported.includes(item.protocol))
}
