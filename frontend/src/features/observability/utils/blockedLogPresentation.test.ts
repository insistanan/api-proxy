import { describe, expect, it } from 'vitest'
import type { BlockedLogEntry } from '@/types/logs'
import { blockedLogSnippetPreview, groupRepeatedBlockedLogs, parseBlockedLogSnippet } from '@/features/observability/utils/blockedLogPresentation'

const record = (id: number, overrides: Partial<BlockedLogEntry> = {}): BlockedLogEntry => ({
  id, timestamp: '2026-09-14T08:20:28Z', createdAt: '2026-09-14T08:20:28Z', apiType: 'messages', blockType: 'credential', ruleName: 'api_key',
  conversationId: 'conversation-a', requestId: `request-${id}`,
  promptSnippet: 'mode=mask source=tool_result path=messages[0].content\n凭据：[凭据已隐藏:api_key]\n15 "type": "local"', ...overrides
})

describe('拦截记录摘要与重复折叠', () => {
  it('保留命中上下文、兼容旧格式并正确解析元数据', () => {
    expect(blockedLogSnippetPreview(record(1).promptSnippet)).toBe('凭据：[凭据已隐藏:api_key] 15 "type": "local"')
    expect(blockedLogSnippetPreview('mode=mask source=user path=input\r\n命中片段\r\n')).toBe('命中片段')
    expect(blockedLogSnippetPreview('旧记录第一行\n第二行')).toBe('旧记录第一行 第二行')
    expect(blockedLogSnippetPreview()).toBe('')
    expect(parseBlockedLogSnippet('mode=mask source=user\n正文').mode).toBe('mask')
    expect(parseBlockedLogSnippet('mode=unknown\n正文').mode).toBe('')
  })

  it('只折叠相同会话和处置的记录，保留请求顺序及完整上下文差异', () => {
    const groups = groupRepeatedBlockedLogs([record(3), record(2), record(1)])
    expect(groups).toHaveLength(1)
    expect(groups[0]?.entries.map(entry => entry.requestId)).toEqual(['request-3', 'request-2', 'request-1'])

    const different = [
      { conversationId: 'conversation-b' }, { apiType: 'responses' as const }, { ruleName: 'named_secret' },
      { model: 'other-model' }, { channelName: 'other-channel' },
      { promptSnippet: 'mode=mask source=user path=messages[0].content\n相同尾部' },
      { promptSnippet: 'mode=audit source=tool_result path=messages[0].content\n相同尾部' }
    ]
    for (const overrides of different) expect(groupRepeatedBlockedLogs([record(2), record(1, overrides)])).toHaveLength(2)

    const changedContext = record(1, { promptSnippet: record(2).promptSnippet?.replace('凭据：', '另一处凭据：') })
    expect(groupRepeatedBlockedLogs([record(2), changedContext])).toHaveLength(2)
    for (const overrides of [
      { conversationId: undefined }, { blockType: 'dangerous_cmd' as const }, { promptSnippet: 'mode=block source=user path=input\n同一个命中' }
    ]) expect(groupRepeatedBlockedLogs([record(2, overrides), record(1, overrides)])).toHaveLength(2)
  })
})
