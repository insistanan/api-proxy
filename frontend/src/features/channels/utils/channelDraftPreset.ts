// 渠道池的新建预设：从公用图片理解池或文本摘要池直接打开新建弹窗时，
// 先把渠道身份写进草稿，避免用户建完渠道后仍留在常规分组里。
import type { ChannelFormState } from '@/features/channels/composables/useChannelEditor'

export type ChannelDraftPreset = 'vision' | 'summary'

type DraftPresetTarget = Pick<ChannelFormState,
  'visionCapable' | 'excludeFromConversation' | 'compaction'>

/**
 * 图片理解池要求渠道退出常规调度且支持图片理解；
 * 文本摘要池要求加入摘要池，摘要池渠道同样退出常规调度。
 */
export function applyChannelDraftPreset(form: DraftPresetTarget, preset: ChannelDraftPreset): void {
  if (preset === 'vision') {
    form.visionCapable = true
    form.excludeFromConversation = true
    return
  }
  form.compaction.summaryCapable = true
  form.compaction.summaryPool = false
  form.excludeFromConversation = true
}
