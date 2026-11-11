// MCP 导入与草稿行为回归：只调用页面 setup，不读取本机配置或启动服务。
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { api, ApiError } from '@/services/api'
import type { McpListResponse, McpServer } from '@/types'
import McpView from './McpView.vue'

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))

type State = Record<string, unknown>
interface Row { key: string; name: string; server?: McpServer; sources: string[]; conflict: boolean }
const value = <T>(state: State, key: string) => (state[key] as Ref<T>).value
const run = (state: State, key: string) => (state[key] as () => Promise<void>)()
const setupView = async (): Promise<State> => {
  let state!: State
  const setup = (McpView as unknown as { setup: (props: object, context: SetupContext) => State }).setup
  await renderToString(createSSRApp({
    setup(props, context) {
      state = setup(props, context)
      return () => null
    }
  }))
  return state
}
const snapshot = (overrides: Partial<McpListResponse> = {}): McpListResponse => ({
  revision: 'r1', servers: [], agents: [
    { key: 'claude-code', name: 'Claude Code', path: '/test/claude.json', exists: true, count: 0, active: 0, revision: 'cc1' },
    { key: 'codex', name: 'Codex', path: '/test/config.toml', exists: true, count: 0, active: 0, revision: 'cx1' },
    { key: 'opencode', name: 'OpenCode', path: '/test/opencode.json', exists: true, count: 0, active: 0, revision: 'oc1' }
  ], detected: [], ...overrides
})
const server = (name: string, command = 'npx'): McpServer => ({ name, transport: 'stdio', command, agents: ['codex'] })
afterEach(() => { vi.restoreAllMocks() })

describe('MCP 应用配置管理', () => {
  it('Playwright 的简单 cmd 包装合并为一行，直接编辑保存和开关保留各应用命令', async () => {
    const direct: McpServer = { ...server('playwright'), args: ['@playwright/mcp@latest', '--extension'] }
    const wrapped: McpServer = { ...direct, command: 'cmd', args: ['/c', 'npx', ...direct.args!], agents: ['claude-code'] }
    vi.spyOn(api, 'getMcp').mockResolvedValue(snapshot({ detected: [
      { name: 'playwright', agent: 'claude-code', enabled: true, server: wrapped },
      { name: 'playwright', agent: 'codex', enabled: true, server: direct }
    ] }))
    const save = vi.spyOn(api, 'saveMcp').mockResolvedValue({ success: true, revision: 'r2', agents: [] })
    const toggle = vi.spyOn(api, 'toggleMcp').mockResolvedValue({ success: true, revision: 'r3', agents: [{ key: 'codex', name: 'Codex', path: '/test/config.toml', ok: true }] })
    const state = await setupView()
    await run(state, 'loadList')
    const rows = value<Row[]>(state, 'discoveredRows')
    expect(rows).toHaveLength(1)
    expect(rows[0].conflict).toBe(false)
    ;(state.openDetectedEdit as (row: Row) => void)(rows[0])
    await run(state, 'submitForm')
    await run(state, 'saveList')
    expect(save.mock.calls[0][1][0].agentConfigs).toEqual(expect.objectContaining({
      codex: expect.objectContaining({ command: 'npx', args: direct.args }),
      'claude-code': expect.objectContaining({ command: 'cmd', args: wrapped.args })
    }))
    await (state.toggleServer as (server: McpServer, agent: string, enabled: boolean) => Promise<void>)(rows[0].server!, 'codex', false)
    expect(toggle).toHaveBeenCalledWith(expect.objectContaining({ server: expect.objectContaining({ command: 'npx', args: direct.args }) }))
  })

  it('已管理的 Playwright 合并其它应用来源，普通编辑保留启动方式', async () => {
    const direct: McpServer = { ...server('playwright'), args: ['@playwright/mcp@latest'] }
    vi.spyOn(api, 'getMcp').mockResolvedValue(snapshot({ servers: [direct], detected: [
      { name: direct.name, agent: 'codex', enabled: true, server: direct },
      { name: direct.name, agent: 'claude-code', enabled: true, server: { ...direct, command: 'cmd', args: ['/c', 'npx', ...direct.args!], agents: ['claude-code'] } }
    ] }))
    const state = await setupView()
    await run(state, 'loadList')
    expect(value<Row[]>(state, 'discoveredRows')).toHaveLength(0)
    expect(value<boolean>(state, 'dirty')).toBe(false)
    ;(state.openEdit as (index: number) => void)(0)
    await run(state, 'submitForm')
    expect(value<McpServer[]>(state, 'servers')[0].agentConfigs?.['claude-code']?.command).toBe('cmd')
    expect(value<boolean>(state, 'dirty')).toBe(false)
    ;(state.openEdit as (index: number) => void)(0)
    ;(state.form as { command: string }).command = 'custom-launcher'
    await run(state, 'submitForm')
    expect(value<McpServer[]>(state, 'servers')[0].agentConfigs).toBeUndefined()
  })

  it('带 shell 运算符或不同参数的配置保持冲突', async () => {
    const direct: McpServer = { ...server('playwright'), args: ['@playwright/mcp@latest'] }
    vi.spyOn(api, 'getMcp').mockResolvedValue(snapshot({ detected: [
      { name: 'playwright', agent: 'codex', enabled: true, server: direct },
      { name: 'playwright', agent: 'claude-code', enabled: true, server: { ...direct, command: 'cmd', args: ['/c', 'npx', '@playwright/mcp@latest', '&&', 'other'], agents: ['claude-code'] } }
    ] }))
    const state = await setupView()
    await run(state, 'loadList')
    const rows = value<Row[]>(state, 'discoveredRows')
    expect(rows).toHaveLength(2)
    expect(rows.every(row => row.conflict)).toBe(true)
  })

  it('node_repl 与其它 MCP 使用相同的开关规则', async () => {
    const runtime = server('node_repl', 'C:/Codex/runtimes/cua_node/node.exe')
    vi.spyOn(api, 'getMcp').mockResolvedValue(snapshot({ detected: [{ name: runtime.name, agent: 'codex', enabled: true, server: runtime }] }))
    const toggle = vi.spyOn(api, 'toggleMcp').mockResolvedValue({ success: true, revision: 'r2', agents: [{ key: 'claude-code', name: 'Claude Code', path: '/test/claude.json', ok: true }] })
    const state = await setupView()
    await run(state, 'loadList')
    await (state.toggleServer as (server: McpServer, agent: string, enabled: boolean) => Promise<void>)(runtime, 'claude-code', true)
    expect(toggle).toHaveBeenCalledOnce()
  })

  it('读取并合并来源，直接编辑在保存前不写入配置', async () => {
    vi.spyOn(api, 'getMcp').mockResolvedValue(snapshot({ detected: [
      { name: 'demo', agent: 'codex', enabled: true, server: server('demo') },
      { name: 'demo', agent: 'claude-code', enabled: true, server: { ...server('demo'), agents: ['claude-code'] } }
    ] }))
    const save = vi.spyOn(api, 'saveMcp')
    const state = await setupView()
    await run(state, 'loadList')
    const rows = value<Row[]>(state, 'discoveredRows')
    expect(rows).toHaveLength(1)
    expect(rows[0].sources).toEqual(['codex', 'claude-code'])
    expect(value<McpServer[]>(state, 'servers')).toEqual([])
    ;(state.openDetectedEdit as (row: Row) => void)(rows[0])
    await run(state, 'submitForm')
    expect(value<McpServer[]>(state, 'servers')[0].agents).toEqual(['claude-code', 'codex'])
    expect(value<boolean>(state, 'dirty')).toBe(true)
    expect(save).not.toHaveBeenCalled()
  })

  it('编辑同名冲突条目建议新名称，并保留已有草稿', async () => {
    vi.spyOn(api, 'getMcp').mockResolvedValue(snapshot({
      servers: [server('demo', 'original')],
      detected: [{ name: 'demo', agent: 'claude-code', enabled: true, server: { ...server('demo', 'different'), agents: ['claude-code'] } }]
    }))
    const state = await setupView()
    await run(state, 'loadList')
    const rows = value<Row[]>(state, 'discoveredRows')
    expect(rows[0].conflict).toBe(true)
    ;(state.openDetectedEdit as (row: Row) => void)(rows[0])
    expect((state.form as { name: string }).name).toBe('demo-claude-code')
    expect(value<McpServer[]>(state, 'servers')[0].command).toBe('original')
    expect(value<boolean>(state, 'dialogOpen')).toBe(true)
  })

  it('搜索后的勾选使用原列表下标', async () => {
    vi.spyOn(api, 'getMcp').mockResolvedValue(snapshot({ servers: [server('first'), server('second')] }))
    const toggle = vi.spyOn(api, 'toggleMcp').mockResolvedValue({ success: true, revision: 'r2', agents: [{ key: 'claude-code', name: 'Claude Code', path: '/test/claude.json', ok: true }] })
    const state = await setupView()
    await run(state, 'loadList')
    ;(state.search as Ref<string>).value = 'second'
    const rows = value<{ index: number }[]>(state, 'filteredManaged')
    expect(rows).toHaveLength(1)
    expect(rows[0].index).toBe(1)
    await (state.setAgentEnabled as (index: number, agent: string, enabled: boolean) => Promise<void>)(rows[0].index, 'claude-code', true)
    expect(toggle).toHaveBeenCalledWith(expect.objectContaining({ agent: 'claude-code', server: expect.objectContaining({ name: 'second' }), enabled: true }))
    expect(value<McpServer[]>(state, 'servers')[0].agents).toEqual(['codex'])
    expect(value<McpServer[]>(state, 'servers')[1].agents).toEqual(['claude-code', 'codex'])
  })

  it('发现的条目直接删除，保存携带来源原配置且保留其它草稿', async () => {
    vi.spyOn(api, 'getMcp').mockResolvedValue(snapshot({ servers: [server('draft')], detected: [
      { name: 'demo', agent: 'codex', enabled: false, server: { ...server('demo'), agents: [] } }
    ] }))
    const save = vi.spyOn(api, 'saveMcp').mockResolvedValue({ success: true, revision: 'r2', agents: [{ key: 'codex', name: 'Codex', path: '/test/config.toml', ok: true }] })
    const state = await setupView()
    await run(state, 'loadList')
    const rows = value<Row[]>(state, 'discoveredRows')
    ;(state.confirmDetectedDelete as (row: Row) => void)(rows[0])
    await run(state, 'deleteServer')
    expect(value<Row[]>(state, 'discoveredRows')).toHaveLength(0)
    expect(value<boolean>(state, 'dirty')).toBe(true)
    await run(state, 'saveList')
    expect(save).toHaveBeenCalledWith('r1', [expect.objectContaining({ name: 'draft' })], [expect.objectContaining({ agent: 'codex', agentRevision: 'cx1', replacement: '', server: expect.objectContaining({ name: 'demo' }) })])
  })

  it('粘贴完整命令自动填充名称和参数，错误输入不会提交旧解析结果', async () => {
    vi.spyOn(api, 'getMcp').mockResolvedValue(snapshot())
    const state = await setupView()
    await run(state, 'loadList')
    await run(state, 'openCreate')
    ;(state.applyCommandText as (text: string) => void)('npx -y @playwright/mcp@latest --extension')
    expect((state.form as { name: string }).name).toBe('playwright')
    await run(state, 'submitForm')
    expect(value<McpServer[]>(state, 'servers')[0]).toEqual(expect.objectContaining({ name: 'playwright', command: 'npx', args: ['-y', '@playwright/mcp@latest', '--extension'] }))
    await run(state, 'openCreate')
    ;(state.applyCommandText as (text: string) => void)('uvx valid-server')
    ;(state.applyCommandText as (text: string) => void)('uvx "unclosed')
    await run(state, 'submitForm')
    expect(value<McpServer[]>(state, 'servers')).toHaveLength(1)
    expect(value<string>(state, 'formError')).toContain('引号')
  })

  it('发现的条目直接禁用，不要求导入或提交其它草稿', async () => {
    vi.spyOn(api, 'getMcp').mockResolvedValue(snapshot({
      servers: [server('draft')],
      detected: [{ name: 'demo', agent: 'codex', enabled: true, server: server('demo') }]
    }))
    const toggle = vi.spyOn(api, 'toggleMcp').mockResolvedValue({ success: true, revision: 'r2', agents: [{ key: 'codex', name: 'Codex', path: '/test/config.toml', ok: true }] })
    const save = vi.spyOn(api, 'saveMcp')
    const state = await setupView()
    await run(state, 'loadList')
    value<McpServer[]>(state, 'servers')[0].command = 'unsaved-command'
    await (state.toggleServer as (server: McpServer, agent: string, enabled: boolean) => Promise<void>)(server('demo'), 'codex', false)
    expect(toggle).toHaveBeenCalledWith(expect.objectContaining({ revision: 'r1', agentRevision: 'cx1', agent: 'codex', enabled: false }))
    expect(save).not.toHaveBeenCalled()
    expect(value<McpServer[]>(state, 'servers')).toHaveLength(1)
    expect(value<McpServer[]>(state, 'servers')[0].command).toBe('unsaved-command')
    expect(value<string>(state, 'toggleNotice')).toContain('禁用')
  })

  it('不同的同名配置不能跨应用覆盖，但仍可切换来源应用', async () => {
    vi.spyOn(api, 'getMcp').mockResolvedValue(snapshot({ detected: [
      { name: 'demo', agent: 'codex', enabled: true, server: server('demo', 'codex-command') },
      { name: 'demo', agent: 'claude-code', enabled: true, server: { ...server('demo', 'claude-command'), agents: ['claude-code'] } }
    ] }))
    const toggle = vi.spyOn(api, 'toggleMcp').mockResolvedValue({ success: true, revision: 'r2', agents: [{ key: 'codex', name: 'Codex', path: '/test/config.toml', ok: true }] })
    const state = await setupView()
    await run(state, 'loadList')
    const operation = state.toggleServer as (server: McpServer, agent: string, enabled: boolean) => Promise<void>
    await operation(server('demo', 'codex-command'), 'claude-code', true)
    expect(toggle).not.toHaveBeenCalled()
    expect(value<string>(state, 'saveError')).toContain('同名配置')
    await operation(server('demo', 'codex-command'), 'codex', false)
    expect(toggle).toHaveBeenCalledTimes(1)
  })

  it('保存冲突保留草稿，不自动刷新丢弃修改', async () => {
    const list = vi.spyOn(api, 'getMcp').mockResolvedValue(snapshot({ servers: [server('demo')] }))
    vi.spyOn(api, 'saveMcp').mockRejectedValue(new ApiError('冲突', 409, undefined, 'revision_conflict'))
    const state = await setupView()
    await run(state, 'loadList')
    value<McpServer[]>(state, 'servers')[0].command = 'edited'
    await run(state, 'saveList')
    expect(list).toHaveBeenCalledTimes(1)
    expect(value<McpServer[]>(state, 'servers')[0].command).toBe('edited')
    expect(value<string>(state, 'saveError')).toContain('草稿已保留')
  })
})
