// 导航文案与页面定位共用一份配置，路由组件仍由 router/index.ts 懒加载。
export const navigationGroups = [
  {
    key: 'channels', label: '渠道管理', icon: 'mdi-server-network',
    items: [
      { label: 'Messages', to: '/channels/messages', description: '管理 Claude Messages 入口的渠道与模型分组。' },
      { label: 'Responses', to: '/channels/responses', description: '管理 OpenAI Responses 入口的渠道与模型分组。' },
      { label: 'Gemini', to: '/channels/gemini', description: '管理 Gemini 入口的渠道与模型分组。' },
      { label: 'Chat', to: '/channels/chat', description: '管理 OpenAI Chat 入口的渠道与模型分组。' },
      { label: 'Images', to: '/channels/images', description: '管理图片生成入口的渠道与输出设置。' },
      { label: 'Embeddings', to: '/channels/embeddings', description: '管理向量嵌入入口的渠道与输出维度。' },
      { label: 'Decisions', to: '/channels/decisions', description: '管理决策入口的渠道与模型分组。' }
    ]
  },
  {
    key: 'activity', label: '运行观测', icon: 'mdi-chart-areaspline',
    items: [
      { label: '对话', to: '/conversations', description: '查看对话、请求链路与模型输入。' },
      { label: '请求与系统日志', to: '/logs', description: '按时间和入口筛选请求，查看正文及详细运行日志。' },
      { label: '拦截记录', to: '/blocked-logs', description: '查看内容安全检测与危险命令审批记录。' }
    ]
  },
  {
    key: 'clients', label: '客户端配置', icon: 'mdi-robot-outline',
    items: [
      { label: 'Claude Code', to: '/claude-code', description: '连接代理，配置模型与 Claude Code 客户端选项。' },
      { label: 'GPT / Codex', to: '/gpt', description: '连接代理，管理 Codex 的模型与客户端配置。' },
      { label: 'OpenCode', to: '/opencode', description: '管理 OpenCode 服务商、模型与代理连接。' },
      { label: 'pi-agent', to: '/pi-agent', description: '管理 pi-agent 服务商、模型与代理连接。' },
      { label: 'DSH', to: '/dsh', description: '管理 DSH 模型与代理连接。' }
    ]
  },
  {
    key: 'extensions', label: '工具与扩展', icon: 'mdi-tune',
    items: [
      { label: 'Skills', to: '/skills', description: '发现、安装并管理各客户端使用的技能。' },
      { label: 'MCP', to: '/mcp', description: '集中管理 MCP 服务器，按客户端启用与分发。' }
    ]
  }
]

export function findNavigation(path: string) {
  for (const group of navigationGroups) {
    const item = group.items.find(item => item.to === path)
    if (item) return { ...item, group: group.label, groupKey: group.key }
  }
  if (path === '/settings') {
    return { label: '设置', to: path, description: '管理网络、请求策略、内容安全与客户端集成。', group: '系统', groupKey: 'settings' }
  }
  return undefined
}
