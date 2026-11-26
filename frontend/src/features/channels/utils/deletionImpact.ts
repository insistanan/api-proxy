import type { ChannelDeletionImpact } from '@/types'
import { API_TAB_LABELS } from '@/features/channels/services/channel-api'

/** 删除前明确说明图片理解引用也会被解除，避免误以为只是删除上游。 */
export function deletionImpactWarning(impact: ChannelDeletionImpact): string {
  if (!impact.dependents.length) return ''
  const blocking = impact.dependents.filter(item => item.blocking)
  if (blocking.length) {
    const details = blocking.map(item => `• ${API_TAB_LABELS[item.kind]}：${item.name}（${item.feature}）`).join('\n')
    return `\n\n以下依赖会阻止删除，请先在对应设置中解除引用：\n${details}`
  }
  const channels = impact.dependents.map(channel => `• ${API_TAB_LABELS[channel.kind]}：${channel.name}`).join('\n')
  return `\n\n以下渠道引用了待删除的图片理解渠道：\n${channels}\n\n确认后将同时关闭这些渠道的图片理解支持和图片理解层，并清除所选渠道及模型配置；这些渠道本身会保留。`
}
