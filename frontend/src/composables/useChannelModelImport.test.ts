// 本代理模型导入回归：保留同名分组，并按实际入口协议过滤。
import { describe, expect, it } from 'vitest'
import type { ApiTab, ModelEntry } from '@/services/api'
import {
  buildDSHModel,
  buildOpenCodeModel,
  buildPiAgentModel,
  filterProxyModelsByKind,
  importProxyModels
} from './useChannelModelImport'

const entry = (id: string, ownedBy: string): ModelEntry => ({
  id,
  object: 'model',
  created: 0,
  owned_by: ownedBy
})

const messagesModels = ['opus', 'sonnet', 'gpt', 'gemini', 'chat', 'claude', 'deepseek', 'glm', 'grok', 'kimi']
const entries = [
  ...messagesModels.map(id => entry(id, id === 'gpt' ? 'pool:messages,responses' : 'pool:messages')),
  entry('responses-only', 'pool:responses'),
  entry('gemini-only', 'pool:gemini'),
  entry('chat-only', 'pool:chat'),
  entry('images-only', 'pool:images'),
  entry('unconfigured-family', 'api-proxy')
]

describe('本代理模型按协议导入', () => {
  it('完整导入超过五个分组，保留与固定别名同名的分组', () => {
    expect(filterProxyModelsByKind(entries, 'messages')).toEqual(messagesModels)
  })

  it.each<[ApiTab, string[]]>([
    ['responses', ['gpt', 'responses-only']],
    ['gemini', ['gemini-only']],
    ['chat', ['chat-only']],
    ['images', ['images-only']]
  ])('%s 只导入该入口中实际配置的分组', (kind, expected) => {
    expect(filterProxyModelsByKind(entries, kind)).toEqual(expected)
  })

  it('只配置其他协议分组时返回空列表', () => {
    expect(filterProxyModelsByKind([entry('gpt', 'pool:responses'), entry('sonnet', 'api-proxy')], 'messages')).toEqual([])
  })

  it('忽略空 ID，并对多协议条目去重', () => {
    expect(filterProxyModelsByKind([
      entry(' gpt ', 'pool:messages,responses'),
      entry('gpt', 'pool:messages'),
      entry(' ', 'pool:messages')
    ], 'messages')).toEqual(['gpt'])
  })

  it('三个客户端都完整替换为所选协议的分组模型', () => {
    const modelIds = filterProxyModelsByKind(entries, 'messages')
    let dshModels: ReturnType<typeof buildDSHModel>[] = [buildDSHModel('old', { contextLimit: 1, outputLimit: 1, inputLimit: 1 })]
    let piModels: ReturnType<typeof buildPiAgentModel>[] = []
    let openCodeModels: ReturnType<typeof buildOpenCodeModel>[] = []

    expect(importProxyModels('messages', modelIds, buildDSHModel, models => { dshModels = models })).toBe(10)
    expect(importProxyModels('messages', modelIds, buildPiAgentModel, models => { piModels = models })).toBe(10)
    expect(importProxyModels('messages', modelIds, buildOpenCodeModel, models => { openCodeModels = models })).toBe(10)
    expect(dshModels.map(model => model.id)).toEqual(messagesModels)
    expect(piModels.map(model => model.id)).toEqual(messagesModels)
    expect(openCodeModels.map(model => model.key)).toEqual(messagesModels)
  })
})
