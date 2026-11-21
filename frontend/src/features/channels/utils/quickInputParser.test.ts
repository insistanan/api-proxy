/**
 * 快速添加渠道输入解析的核心边界：名称、凭据、地址和常见配置文本。
 * 同一规则的样本合并在一个用例中，避免为每个样本重复启动测试环境。
 */
import { describe, expect, it } from 'vitest'
import { isValidApiKey, isValidUrl, parseQuickInput } from '@/features/channels/utils/quickInputParser'

describe('复制配置的快速解析', () => {
  it('识别名称前缀并稳定清理重复标签', () => {
    for (const prefix of ['名称:', '名称：', 'name:', '渠道名称：']) {
      expect(parseQuickInput(`${prefix} 渠道A\nURL: https://api.example.com/v1\n密钥: sk-fixture-key`)).toMatchObject({
        detectedChannelName: '渠道A', detectedBaseUrl: 'https://api.example.com/v1', detectedApiKeys: ['sk-fixture-key']
      })
    }
    let name = '名称: 名称：渠道A'
    for (let count = 0; count < 3; count++) {
      name = parseQuickInput(`名称: ${name}\nURL: https://api.example.com/v1\n密钥: sk-fixture-key`).detectedChannelName
    }
    expect(name).toBe('渠道A')
  })

  it('识别常见 API Key 格式', () => {
    const valid = [
      'sk-proj-Aw9' + 'x'.repeat(100), 'sk-proj-' + 'abcdef1234567890'.repeat(8),
      'sk-ant-api03-' + 'abcdef1234567890'.repeat(6),
      'sk-ant-api03-bK9' + 'x'.repeat(80),
      'AIzaSyDxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx', 'AIzaXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX',
      '269abc123456789012345678.r8abcdef1234', 'abcdefghij1234567890abcd.secretkey123456',
      '550e8400-e29b-41d4-a716-446655440000', '123e4567-e89b-12d3-a456-426614174000',
      'sk-proj-abc123xyz', 'sk-1234567890abcdef', 'sk-abcDEFghiJKL', 'ut_abc123456789', 'api-key12345678901', 'cr_xxxxxxxxx123',
      'sk-111', 'sk-x', 'sk-abc', 'sk-test',
      'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U',
      'abcdefghijklmnopqrstuvwxyz123456', 'ABCDEFGHIJKLMNOPQRSTUVWXYZ123456', '72f988bf7ab9e0f0a1234567890abcde'
    ]
    for (const value of valid) expect(isValidApiKey(value), value).toBe(true)
  })

  it('拒绝高风险误识别的 API Key 候选', () => {
    const invalid = [
      's-1234567890123', 'u_1234567890123', 'xx-111', 'foo-bar', 'test_key',
      'API_TIMEOUT_MS', 'ANTHROPIC_BASE_URL', 'ANTHROPIC_AUTH_TOKEN', 'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC', 'DATABASE_URL', 'SECRET_KEY',
      'eyJhbGciOiJIUzI1NiIs.xxx', 'a'.repeat(31), 'a'.repeat(32),
      'abcdefghijklmnopqrstuvwxyzabcdef'
    ]
    for (const value of invalid) expect(isValidApiKey(value), value).toBe(false)
  })

  it('识别 HTTP(S) 地址并拒绝缺少主机或错误协议的地址', () => {
    for (const value of ['http://localhost', 'http://localhost/', 'http://localhost:3000', 'http://localhost:3000/', 'http://localhost:5688/v1']) {
      expect(isValidUrl(value), value).toBe(true)
    }
    for (const value of ['localhost', 'api.openai.com', '//api.openai.com', 'ftp://example.com', 'ws://example.com']) {
      expect(isValidUrl(value), value).toBe(false)
    }
  })

  it('解析多个地址和密钥时保留首个地址并去重', () => {
    const cases = [
      { input: 'https://api.openai.com/v1\nsk-key1abc123456\nsk-key2def789012\nsk-key3ghi345678', url: 'https://api.openai.com/v1', keys: ['sk-key1abc123456', 'sk-key2def789012', 'sk-key3ghi345678'] },
      { input: 'https://api.example.com, sk-key1234567890; ut_key2abc123456, api-key3def789012', url: 'https://api.example.com', keys: ['sk-key1234567890', 'ut_key2abc123456', 'api-key3def789012'] },
      { input: 'https://first.com https://second.com sk-key1234567890', url: 'https://first.com', keys: ['sk-key1234567890'] },
      { input: 'sk-key1234567890 sk-key1234567890 sk-key2abcdef123', url: '', keys: ['sk-key1234567890', 'sk-key2abcdef123'] },
      { input: 'https://api.example.com/anthropic# sk-key1234567890', url: 'https://api.example.com/anthropic#', keys: ['sk-key1234567890'] },
      { input: 'https://api.example.com/ sk-key1234567890', url: 'https://api.example.com', keys: ['sk-key1234567890'] },
      { input: `https://api.example.com eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U`, url: 'https://api.example.com', keys: ['eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U'] }
    ]
    for (const { input, url, keys } of cases) {
      const result = parseQuickInput(input)
      expect(result.detectedBaseUrl, input).toBe(url)
      expect(result.detectedApiKeys, input).toEqual(keys)
    }
  })

  it('提取引号配置中的 URL 和密钥并排除配置键名', () => {
    const english = `"ANTHROPIC_AUTH_TOKEN": "sk-lACTyHP69FC46DeD8F67T3BLBkFJ4cE3879908bc4c38a336",
"ANTHROPIC_BASE_URL": "https://apic1.example.com/api/v1/ai/openai/cc-omg"`
    const result = parseQuickInput(english)
    expect(result.detectedBaseUrl).toBe('https://apic1.example.com/api/v1/ai/openai/cc-omg')
    expect(result.detectedApiKeys).toContain('sk-lACTyHP69FC46DeD8F67T3BLBkFJ4cE3879908bc4c38a336')
    expect(result.detectedApiKeys).not.toEqual(expect.arrayContaining(['ANTHROPIC_AUTH_TOKEN', 'ANTHROPIC_BASE_URL']))

    const chinese = parseQuickInput('"sk-chinese123456789""https://api.example.com"')
    expect(chinese.detectedBaseUrl).toBe('https://api.example.com')
    expect(chinese.detectedApiKeys).toEqual(['sk-chinese123456789'])

    const config = parseQuickInput(`发一个20$的key，用起来还不错，你们试试，好像是官逆
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "sk-lACTyHP69FC46DeD8F67T3BLBkFJ4cE3879908bc4c38a336",
    "ANTHROPIC_BASE_URL": "https://apic1.example.com/api/v1/ai/openai/cc-omg",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": 1
  }
}`)
    expect(config.detectedBaseUrl).toBe('https://apic1.example.com/api/v1/ai/openai/cc-omg')
    expect(config.detectedApiKeys).toContain('sk-lACTyHP69FC46DeD8F67T3BLBkFJ4cE3879908bc4c38a336')
    expect(config.detectedApiKeys).not.toEqual(expect.arrayContaining(['ANTHROPIC_AUTH_TOKEN', 'ANTHROPIC_BASE_URL', 'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC']))

    const settings = parseQuickInput(`{
  "$schema": "https://json.schemastore.org/claude-code-settings.json",
  "env": {
    "API_TIMEOUT_MS": "200000",
    "ANTHROPIC_BASE_URL": "http://localhost:3688/",
    "ANTHROPIC_AUTH_TOKEN": "key",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"
  },
  "includeCoAuthoredBy": false
}`)
    expect(settings.detectedBaseUrl).toBe('https://json.schemastore.org/claude-code-settings.json')
    expect(settings.detectedApiKeys).not.toEqual(expect.arrayContaining(['API_TIMEOUT_MS', 'ANTHROPIC_BASE_URL', 'ANTHROPIC_AUTH_TOKEN', 'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC']))

    const mixedQuotes = parseQuickInput('"https://api.example.com" "sk-key12345678901')
    expect(mixedQuotes.detectedBaseUrl).toBe('https://api.example.com')
    expect(mixedQuotes.detectedApiKeys).toContain('sk-key12345678901')
  })
})
