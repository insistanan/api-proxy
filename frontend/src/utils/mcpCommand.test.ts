import { describe, expect, it } from 'vitest'
import { formatMcpCommand, parseMcpCommand } from './mcpCommand'

describe('MCP 启动命令解析', () => {
  it('识别 npx 包名、版本与参数，建议服务名称', () => {
    expect(parseMcpCommand('npx -y @playwright/mcp@latest --extension')).toEqual({
      transport: 'stdio', command: 'npx', args: ['-y', '@playwright/mcp@latest', '--extension'], suggestedName: 'playwright'
    })
  })
  it('保留带空格的 Windows 路径、空参数和参数中的等号', () => {
    const parsed = parseMcpCommand(String.raw`"C:\Program Files\nodejs\node.exe" "D:\MCP Tools\server.js" "" --flag=a=b`)
    expect(parsed.command).toBe(String.raw`C:\Program Files\nodejs\node.exe`)
    expect(parsed.args).toEqual([String.raw`D:\MCP Tools\server.js`, '', '--flag=a=b'])
  })
  it('解析前置环境变量并保留 cmd 启动包装', () => {
    const parsed = parseMcpCommand('TOKEN="a b" cmd /c npx @playwright/mcp@latest')
    expect(parsed.env).toEqual({ TOKEN: 'a b' })
    expect(parsed.command).toBe('cmd')
    expect(parsed.args).toEqual(['/c', 'npx', '@playwright/mcp@latest'])
    expect(parsed.suggestedName).toBe('playwright')
  })
  it('在两种输入模式间切换时保留路径、引号和换行参数', () => {
    const command = String.raw`C:\Program Files\node.exe`
    const args = ['', 'a "quoted" value', 'a\nb', String.raw`D:\folder\server.js`, '$literal']
    const parsed = parseMcpCommand(formatMcpCommand(command, args))
    expect(parsed.command).toBe(command)
    expect(parsed.args).toEqual(args)
  })
  it.each(['', 'TOKEN=x', 'npx "unclosed', 'npx foo && other', 'npx foo | other', 'npx $PACKAGE'])('明确拒绝不完整或含 shell 逻辑的输入：%s', text => {
    expect(() => parseMcpCommand(text)).toThrow()
  })
})
