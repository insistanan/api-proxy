// 渠道选择只使用公开元信息，不包含密钥或运行配置。
export interface ChannelSelectionOption {
  id: string
  name: string
  kind: string
  poolId?: string
  poolName?: string
  defaultModel?: string
  models?: string[]
}
