import { describe, expect, it } from 'vitest'
import type { BlockedLogEntry } from '@/types/logs'
import { blockedLogSnippetPreview, groupRepeatedBlockedLogs, parseBlockedLogSnippet } from './blockedLogPresentation'

const record = (id: number, overrides: Partial<BlockedLogEntry> = {}): BlockedLogEntry => ({
  id,
  timestamp: '2026-09-14T08:20:28Z',
  createdAt: '2026-09-14T08:20:28Z',
  apiType: 'messages',
  blockType: 'credential',
  ruleName: 'api_key',
  conversationId: 'conversation-a',
  requestId: `request-${id}`,
  promptSnippet: 'mode=mask source=tool_result path=messages[0].content\n凭据：[凭据已隐藏:api_key]\n15 "type": "local"',
  ...overrides
})

describe('拦截记录摘要', () => {
  it('保留多行命中上下文，不再只显示 JSON 尾部', () => {
    expect(blockedLogSnippetPreview(record(1).promptSnippet))
      .toBe('凭据：[凭据已隐藏:api_key] 15 "type": "local"')
  })

  it('兼容 CRLF、末尾换行以及不含元数据的旧记录', () => {
    expect(blockedLogSnippetPreview('mode=mask source=user path=input\r\n命中片段\r\n'))
      .toBe('命中片段')
    expect(blockedLogSnippetPreview('旧记录第一行\n第二行')).toBe('旧记录第一行 第二行')
    expect(blockedLogSnippetPreview()).toBe('')
    expect(parseBlockedLogSnippet('mode=mask source=user\n正文').mode).toBe('mask')
    expect(parseBlockedLogSnippet('mode=unknown\n正文').mode).toBe('')
  })
})

describe('重复脱敏片段折叠', () => {
  it('合并同会话相同片段，保留最新优先顺序和全部请求', () => {
    const entries = [record(3), record(2), record(1)]
    const groups = groupRepeatedBlockedLogs(entries)
    expect(groups).toHaveLength(1)
    expect(groups[0]?.entries.map(entry => entry.requestId)).toEqual(['request-3', 'request-2', 'request-1'])
    expect(entries).toHaveLength(3)
  })

  it.each([
    { conversationId: 'conversation-b' },
    { apiType: 'responses' as const },
    { ruleName: 'named_secret' },
    { model: 'other-model' },
    { channelName: 'other-channel' },
    { promptSnippet: 'mode=mask source=user path=messages[0].content\n相同尾部' },
    { promptSnippet: 'mode=audit source=tool_result path=messages[0].content\n相同尾部' }
  ])('不同会话、规则、来源或处置不合并：%j', overrides => {
    expect(groupRepeatedBlockedLogs([record(2), record(1, overrides)])).toHaveLength(2)
  })

  it('完整上下文不同但最后一行相同的记录仍然独立', () => {
    const first = record(2)
    const second = record(1, { promptSnippet: first.promptSnippet?.replace('凭据：', '另一处凭据：') })
    expect(groupRepeatedBlockedLogs([first, second])).toHaveLength(2)
  })

  it('阻断、危险命令和未关联会话的记录不折叠', () => {
    for (const overrides of [
      { conversationId: undefined },
      { blockType: 'dangerous_cmd' as const },
      { promptSnippet: 'mode=block source=user path=input\n同一个命中' }
    ]) {
      expect(groupRepeatedBlockedLogs([record(2, overrides), record(1, overrides)])).toHaveLength(2)
    }
  })
})
