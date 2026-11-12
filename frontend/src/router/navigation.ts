// 顶部导航与页面说明共用一份配置，路由组件仍由 router/index.ts 懒加载。
// 所有入口平铺在顶栏，一次点击即可到达；分组只用于视觉归类，不产生二级菜单。
export interface NavigationItem {
  label: string
  to: string
  description: string
}

export const navigationGroups: Array<{ key: string; label: string; items: NavigationItem[] }> = [
  {
    key: 'channels', label: '渠道协议',
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
    key: 'activity', label: '运行观测',
    items: [
      { label: '对话', to: '/conversations', description: '查看对话、请求链路与模型输入。' },
      { label: '日志', to: '/logs', description: '按时间和入口筛选请求，查看正文及详细运行日志。' },
      { label: '拦截', to: '/blocked-logs', description: '查看内容安全检测与危险命令审批记录。' }
    ]
  },
  {
    key: 'clients', label: '客户端配置',
    items: [
      { label: 'Claude Code', to: '/claude-code', description: '连接代理，配置模型与 Claude Code 客户端选项。' },
      { label: 'GPT', to: '/gpt', description: '连接代理，管理 Codex 的模型与客户端配置。' },
      { label: 'OpenCode', to: '/opencode', description: '管理 OpenCode 服务商、模型与代理连接。' },
      { label: 'pi', to: '/pi-agent', description: '管理 pi-agent 服务商、模型与代理连接。' },
      { label: 'DSH', to: '/dsh', description: '管理 DSH 模型与代理连接。' }
    ]
  },
  {
    key: 'extensions', label: '工具与扩展',
    items: [
      { label: '数据集', to: '/datasets', description: '审核意图和工具风险样本，构建、发布并评测向量索引。' },
      { label: 'Skills', to: '/skills', description: '发现、安装并管理各客户端使用的技能。' },
      { label: 'MCP', to: '/mcp', description: '集中管理 MCP 服务器，按客户端启用与分发。' }
    ]
  }
]

const navigationItems = navigationGroups.flatMap(group => group.items)

export const findNavigation = (path: string): NavigationItem | undefined =>
  navigationItems.find(item => item.to === path)
