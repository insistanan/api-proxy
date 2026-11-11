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
    id: 'moonshot', name: 'Kimi · Moonshot', mark: 'K', website: 'https://platform.moonshot.cn',
    docs: 'https://platform.moonshot.cn/docs/api',
    endpoints: [endpoint('chat', 'https://api.moonshot.cn/v1/chat/completions'), endpoint('messages', 'https://api.moonshot.cn/anthropic/v1/messages')]
  },
  {
    id: 'zhipu', name: '智谱 GLM', mark: 'Z', website: 'https://open.bigmodel.cn',
    docs: 'https://docs.bigmodel.cn/cn/guide/develop/claude/introduction',
    endpoints: [endpoint('chat', 'https://open.bigmodel.cn/api/paas/v4/chat/completions'), endpoint('messages', 'https://open.bigmodel.cn/api/anthropic/v1/messages')]
  },
  {
    id: 'qwen', name: '通义千问 · 百炼', mark: 'Q', website: 'https://bailian.console.aliyun.com',
    docs: 'https://help.aliyun.com/zh/model-studio/base-url',
    endpoints: [endpoint('chat', 'https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions'), endpoint('messages', 'https://dashscope.aliyuncs.com/apps/anthropic/v1/messages')]
  },
  {
    id: 'minimax', name: 'MiniMax', mark: 'M', website: 'https://platform.minimaxi.com',
    docs: 'https://platform.minimaxi.com/docs/guides/text-generation',
    endpoints: [endpoint('messages', 'https://api.minimax.cn/anthropic/v1/messages'), endpoint('chat', 'https://api.minimax.cn/v1/chat/completions')]
  },
  {
    id: 'volcengine', name: '火山方舟', mark: '火', website: 'https://console.volcengine.com/ark',
    docs: 'https://www.volcengine.com/docs/ark/agent-model-invocation-practice',
    endpoints: [endpoint('chat', 'https://ark.cn-beijing.volces.com/api/v3/chat/completions'), endpoint('responses', 'https://ark.cn-beijing.volces.com/api/v3/responses'), endpoint('messages', 'https://ark.cn-beijing.volces.com/api/compatible/v1/messages')]
  },
  {
    id: 'siliconflow', name: '硅基流动', mark: '硅', website: 'https://cloud.siliconflow.cn',
    docs: 'https://docs.siliconflow.cn/api-reference/chat-completions/chat-completions',
    endpoints: [endpoint('chat', 'https://api.siliconflow.cn/v1/chat/completions')]
  },
  {
    id: 'tokenhub', name: '腾讯 TokenHub', mark: 'T', website: 'https://console.cloud.tencent.com/tokenhub',
    docs: 'https://cloud.tencent.com/document/product/1823/130079',
    endpoints: [endpoint('chat', 'https://tokenhub.tencentmaas.com/v1/chat/completions'), endpoint('responses', 'https://tokenhub.tencentmaas.com/v1/responses'), endpoint('messages', 'https://tokenhub.tencentmaas.com/v1/messages')]
  },
  {
    id: 'hunyuan', name: '腾讯混元', mark: '混', website: 'https://console.cloud.tencent.com/hunyuan',
    docs: 'https://cloud.tencent.com/document/product/1729/111007',
    endpoints: [endpoint('chat', 'https://api.hunyuan.cloud.tencent.com/v1/chat/completions')]
  },
  {
    id: 'qianfan', name: '百度千帆', mark: '千', website: 'https://console.bce.baidu.com/qianfan',
    docs: 'https://cloud.baidu.com/doc/qianfan-docs/s/qm8qxemze',
    endpoints: [endpoint('chat', 'https://qianfan.baidubce.com/v2/chat/completions')]
  },
  {
    id: 'openrouter', name: 'OpenRouter', mark: 'R', website: 'https://openrouter.ai',
    docs: 'https://openrouter.ai/docs/api-reference/overview',
    endpoints: [endpoint('chat', 'https://openrouter.ai/api/v1/chat/completions'), endpoint('responses', 'https://openrouter.ai/api/v1/responses'), endpoint('messages', 'https://openrouter.ai/api/v1/messages')]
  },
  {
    id: 'xai', name: 'xAI · Grok', mark: 'X', website: 'https://console.x.ai',
    docs: 'https://docs.x.ai/developers/model-capabilities/text/comparison',
    endpoints: [endpoint('responses', 'https://api.x.ai/v1/responses'), endpoint('chat', 'https://api.x.ai/v1/chat/completions')]
  },
  {
    id: 'mistral', name: 'Mistral AI', mark: 'M', website: 'https://console.mistral.ai',
    docs: 'https://docs.mistral.ai/resources/migration-guides',
    endpoints: [endpoint('chat', 'https://api.mistral.ai/v1/chat/completions')]
  },
  {
    id: 'groq', name: 'Groq', mark: 'G', website: 'https://console.groq.com',
    docs: 'https://console.groq.com/docs/api-reference',
    endpoints: [endpoint('chat', 'https://api.groq.com/openai/v1/chat/completions'), endpoint('responses', 'https://api.groq.com/openai/v1/responses')]
  },
  {
    id: 'cerebras', name: 'Cerebras', mark: 'C', website: 'https://cloud.cerebras.ai',
    docs: 'https://inference-docs.cerebras.ai/resources/openai',
    endpoints: [endpoint('chat', 'https://api.cerebras.ai/v1/chat/completions')]
  },
  {
    id: 'together', name: 'Together AI', mark: 'T', website: 'https://api.together.ai',
    docs: 'https://docs.together.ai/docs/inference/openai-compatibility',
    endpoints: [endpoint('chat', 'https://api.together.ai/v1/chat/completions')]
  },
  {
    id: 'fireworks', name: 'Fireworks AI', mark: 'F', website: 'https://fireworks.ai',
    docs: 'https://docs.fireworks.ai/api-reference/post-chatcompletions',
    endpoints: [endpoint('chat', 'https://api.fireworks.ai/inference/v1/chat/completions')]
  },
  {
    id: 'nvidia', name: 'NVIDIA NIM', mark: 'N', website: 'https://build.nvidia.com',
    docs: 'https://docs.api.nvidia.com/nim/reference/llm-apis',
    endpoints: [endpoint('chat', 'https://integrate.api.nvidia.com/v1/chat/completions')]
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
