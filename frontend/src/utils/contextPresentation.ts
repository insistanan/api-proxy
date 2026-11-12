export const contextKinds: Record<string, { label: string; color: string }> = {
  system: { label: '系统与开发者指令', color: '#a694d6' },
  tools: { label: '工具定义', color: '#cfad78' },
  user: { label: '用户消息', color: '#7d9dc4' },
  assistant: { label: '助手历史', color: '#739f98' },
  reasoning: { label: '可见推理', color: '#ae92b3' },
  tool_call: { label: '工具调用', color: '#c29472' },
  tool_result: { label: '工具结果', color: '#76a58b' }
}

export const contextKind = (kind: string) => contextKinds[kind] || { label: kind, color: '#93969f' }
export const estimatedLabel = (value: number) => `≈${value.toLocaleString()} Token`
