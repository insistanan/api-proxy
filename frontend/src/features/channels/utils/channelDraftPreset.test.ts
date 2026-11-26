import { describe, expect, it } from 'vitest'
import { defaultChannelForm } from '@/features/channels/composables/useChannelEditor'
import { applyChannelDraftPreset } from '@/features/channels/utils/channelDraftPreset'

describe('applyChannelDraftPreset', () => {
  it('图片理解池预设开启图片理解并退出常规调度', () => {
    const form = defaultChannelForm('messages')

    applyChannelDraftPreset(form, 'vision')

    expect(form.visionCapable).toBe(true)
    expect(form.excludeFromConversation).toBe(true)
    expect(form.compaction.summaryCapable).toBe(false)
  })

  it('文本摘要池预设加入摘要池并退出常规调度', () => {
    const form = defaultChannelForm('messages')

    applyChannelDraftPreset(form, 'summary')

    expect(form.compaction.summaryCapable).toBe(true)
    expect(form.compaction.summaryPool).toBe(false)
    expect(form.excludeFromConversation).toBe(true)
    expect(form.visionCapable).toBe(false)
  })

  it('摘要预设会清掉已开启的摘要委托，避免池内渠道再委托给自己', () => {
    const form = defaultChannelForm('messages')
    form.compaction.summaryPool = true

    applyChannelDraftPreset(form, 'summary')

    expect(form.compaction.summaryPool).toBe(false)
  })
})
