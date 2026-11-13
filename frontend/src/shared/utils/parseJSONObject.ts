// 客户端可选的 JSON 配置字段：留空表示没有附加配置，非空内容必须是对象。
export function parseJSONObject(text: string, field: string): Record<string, unknown> {
  if (!text.trim()) return {}
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    throw new Error(`${field} 必须是有效的 JSON 对象`)
  }
  if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
    throw new Error(`${field} 必须是 JSON 对象`)
  }
  return parsed as Record<string, unknown>
}
