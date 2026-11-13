import type { McpConnection } from '@/types'

/** 仅解析启动命令，不执行 shell；保留参数边界、引号内空格与 Windows 路径。 */
export function parseMcpCommand(text: string): McpConnection & { suggestedName: string } {
  const tokens: string[] = []
  let token = ''
  let quote = ''
  let started = false
  for (let index = 0; index < text.length; index++) {
    const char = text[index]
    if (quote) {
      if (char === quote) quote = ''
      else if (quote === '"' && char === '\\' && ['"', '\\'].includes(text[index + 1] ?? '')) token += text[++index]
      else token += char
    } else if (char === '"' || char === "'") {
      quote = char
      started = true
    } else if (/\s/.test(char)) {
      if (started) { tokens.push(token); token = ''; started = false }
    } else if (/[&|<>;`$()]/.test(char)) {
      throw new Error('请粘贴单条启动命令，不支持管道、重定向、变量展开或 shell 运算符')
    } else if (char === '\\' && /[\s"']/.test(text[index + 1] ?? '\u0000')) {
      token += text[++index]
      started = true
    } else {
      token += char
      started = true
    }
  }
  if (quote) throw new Error('命令中的引号尚未闭合')
  if (started) tokens.push(token)
  const env: Record<string, string> = Object.create(null)
  while (/^[A-Za-z_][A-Za-z0-9_]*=/.test(tokens[0] ?? '')) {
    const assignment = tokens.shift()!
    const separator = assignment.indexOf('=')
    const key = assignment.slice(0, separator)
    if (Object.prototype.hasOwnProperty.call(env, key)) throw new Error(`环境变量 ${key} 重复`)
    env[key] = assignment.slice(separator + 1)
  }
  const command = tokens.shift()
  if (!command) throw new Error('请填写 MCP 启动命令，例如 npx -y @playwright/mcp@latest')
  let launcher = command.split(/[/\\]/).pop() ?? command
  let launchArgs = [...tokens]
  if (/^cmd(?:\.exe)?$/i.test(launcher) && launchArgs[0]?.toLowerCase() === '/c') {
    launcher = launchArgs[1] ?? launcher
    launchArgs = launchArgs.slice(2)
  }
  let packageName = launcher.replace(/\.(?:exe|cmd)$/i, '')
  if (/^(?:npx|uvx|bunx)(?:\.cmd)?$/i.test(launcher)) {
    packageName = launchArgs.find(arg => arg && !arg.startsWith('-')) ?? packageName
  }
  packageName = packageName.replace(/@[^/@]+$/, '')
  const parts = packageName.replace(/^@/, '').split('/')
  const base = parts[parts.length - 1] === 'mcp' && parts.length > 1 ? parts[0] : parts[parts.length - 1]
  const suggestedName = (base ?? '').replace(/[^A-Za-z0-9_-]/g, '-').slice(0, 64)
  return { transport: 'stdio', command, args: tokens, ...(Object.keys(env).length ? { env } : {}), suggestedName }
}

export function formatMcpCommand(command: string, args: string[] = []): string {
  return [command, ...args].map(token => !token || /[\s"'\\&|<>;`$()]/.test(token)
    ? `"${token.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"` : token).join(' ')
}
