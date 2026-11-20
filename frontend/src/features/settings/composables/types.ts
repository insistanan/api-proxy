// 设置表单共用的页面类型与操作提示。
export type SettingsSection = 'appearance' | 'network' | 'gateway' | 'content-safety' | 'disguise' | 'prompts' | 'extensions'
export type SettingsNotice = { type: 'success' | 'error'; message: string } | null
