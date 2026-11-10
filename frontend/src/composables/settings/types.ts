// 设置表单共用的页面类型与操作提示。
export type SettingsSection = 'network' | 'gateway' | 'content-safety' | 'integration' | 'disguise' | 'assistant' | 'prompts'
export type SettingsNotice = { type: 'success' | 'error'; message: string } | null
