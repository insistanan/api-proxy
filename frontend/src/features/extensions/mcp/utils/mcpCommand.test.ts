import { describe, expect, it } from 'vitest'
import { formatMcpCommand, parseMcpCommand } from '@/features/extensions/mcp/utils/mcpCommand'

describe('MCP 启动命令解析', () => {
  it('解析常见启动方式并在格式转换中保留参数', () => {
    expect(parseMcpCommand('npx -y @playwright/mcp@latest --extension')).toEqual({
      transport: 'stdio', command: 'npx', args: ['-y', '@playwright/mcp@latest', '--extension'], suggestedName: 'playwright'
    })
    const windows = parseMcpCommand(String.raw`"C:\Program Files\nodejs\node.exe" "D:\MCP Tools\server.js" "" --flag=a=b`)
    expect(windows.command).toBe(String.raw`C:\Program Files\nodejs\node.exe`)
    expect(windows.args).toEqual([String.raw`D:\MCP Tools\server.js`, '', '--flag=a=b'])

    const wrapped = parseMcpCommand('TOKEN="a b" cmd /c npx @playwright/mcp@latest')
    expect(wrapped.env).toEqual({ TOKEN: 'a b' })
    expect(wrapped.command).toBe('cmd')
    expect(wrapped.args).toEqual(['/c', 'npx', '@playwright/mcp@latest'])
    expect(wrapped.suggestedName).toBe('playwright')

    const command = String.raw`C:\Program Files\node.exe`
    const args = ['', 'a "quoted" value', 'a\nb', String.raw`D:\folder\server.js`, '$literal']
    expect(parseMcpCommand(formatMcpCommand(command, args))).toMatchObject({ command, args })
  })

  it('明确拒绝不完整或包含 shell 逻辑的输入', () => {
    for (const text of ['', 'TOKEN=x', 'npx "unclosed', 'npx foo && other', 'npx foo | other', 'npx $PACKAGE']) {
      expect(() => parseMcpCommand(text), text).toThrow()
    }
  })
})
