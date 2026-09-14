/**
 * 拦截记录的摘要与重复片段展示。
 * 折叠仅作用于已加载的脱敏审计记录，保留每次请求，不能据此判断敏感原文相同。
 */
import type { BlockedLogEntry } from '@/types/logs'

export interface BlockedLogSnippet {
  mode: string
  content: string
}

export const parseBlockedLogSnippet = (snippet = ''): BlockedLogSnippet => {
  const lines = snippet.split(/\r?\n/)
  const mode = /^mode=(mask|audit|block|allow|ai_gate)(?:\s|$)/.exec(lines[0] || '')?.[1]
  return { mode: mode || '', content: mode ? lines.slice(1).join('\n') : snippet }
}

export const blockedLogSnippetPreview = (snippet?: string) =>
  parseBlockedLogSnippet(snippet).content.replace(/\s+/g, ' ').trim()

export interface RepeatedBlockedLogGroup {
  key: string
  entry: BlockedLogEntry
  entries: BlockedLogEntry[]
}

// 相同脱敏摘要不代表同一份凭据：这里只折叠显示，展开后仍可逐条查看和删除。
// 保留来源路径、处置模式、规则、模型与渠道的差异，危险命令裁决始终独立展示。
export const groupRepeatedBlockedLogs = (entries: readonly BlockedLogEntry[]): RepeatedBlockedLogGroup[] => {
  const groups = new Map<string, RepeatedBlockedLogGroup>()
  for (const entry of entries) {
    const { mode, content } = parseBlockedLogSnippet(entry.promptSnippet)
    const canFold = Boolean(entry.conversationId && content.trim()) &&
      (mode === 'mask' || mode === 'audit') && !entry.command && !entry.aiReview &&
      ['credential', 'sensitive_info', 'custom_rule'].includes(entry.blockType)
    const key = canFold
      ? JSON.stringify([
        entry.conversationId, entry.apiType, entry.blockType, entry.ruleName || '',
        entry.model || '', entry.channelName || '', entry.promptSnippet
      ])
      : `record:${entry.id}`
    const group = groups.get(key)
    if (group) group.entries.push(entry)
    else groups.set(key, { key, entry, entries: [entry] })
  }
  return [...groups.values()]
}
