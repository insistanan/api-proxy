export const JSON_AUTO_PARSE_BYTES = 64 * 1024
export const JSON_MAX_PARSE_BYTES = 2 * 1024 * 1024
export const JSON_MAX_ROWS = 1500
export const JSON_MAX_DEPTH = 128
const MAX_SEARCH_NODES = 20000

export function jsonText(data: unknown): string {
  if (typeof data === 'string') return data
  const text = JSON.stringify(data, null, 2)
  if (text === undefined) throw new Error('该值不能序列化为 JSON')
  return text
}

export function jsonType(value: unknown): string {
  return value === null ? 'null' : Array.isArray(value) ? 'array' : typeof value
}

export function jsonLength(value: unknown): number | undefined {
  if (typeof value === 'string' || Array.isArray(value)) return value.length
  if (value !== null && typeof value === 'object') return Object.keys(value).length
  return undefined
}

export interface JsonTreeRow {
  path: string
  key: string
  value: unknown
  depth: number
  type: string
  length?: number
  container: boolean
  expanded: boolean
}

const childPath = (parent: string, key: string) => `${parent}/${key.replace(/~/g, '~0').replace(/\//g, '~1')}`
const isContainer = (value: unknown): value is Record<string, unknown> | unknown[] => value !== null && typeof value === 'object'
const preview = (value: unknown) => typeof value === 'string' ? value : String(value)

// 迭代遍历避免深层 JSON 递归爆栈；树与搜索均设预算，超限明确提示。
export function jsonTreeRows(value: unknown, opened: ReadonlySet<string>, expandAll: boolean, query: string) {
  const needle = query.trim().toLocaleLowerCase()
  const matches = new Set<string>()
  let searchLimited = false
  if (needle) {
    const stack = [{ value, path: '$', key: '$', depth: 0 }]
    let visited = 0
    while (stack.length) {
      if (++visited > MAX_SEARCH_NODES) { searchLimited = true; break }
      const node = stack.pop()!
      if (node.key.toLocaleLowerCase().includes(needle) || (!isContainer(node.value) && preview(node.value).toLocaleLowerCase().includes(needle))) {
        let path = node.path
        matches.add(path)
        while (path.includes('/')) { path = path.slice(0, path.lastIndexOf('/')); matches.add(path) }
      }
      if (isContainer(node.value)) {
        if (node.depth >= JSON_MAX_DEPTH) { searchLimited = true; continue }
        const keys = Object.keys(node.value)
        const count = Math.min(keys.length, MAX_SEARCH_NODES - visited - stack.length)
        if (keys.length > count) searchLimited = true
        for (let i = count - 1; i >= 0; i--) {
          const key = keys[i]!
          stack.push({ value: (node.value as Record<string, unknown>)[key], path: childPath(node.path, key), key, depth: node.depth + 1 })
        }
      }
    }
  }
  const rows: JsonTreeRow[] = []
  const stack = [{ value, path: '$', key: '$', depth: 0 }]
  let limited = false
  while (stack.length) {
    const node = stack.pop()!
    if (needle && !matches.has(node.path)) continue
    if (rows.length >= JSON_MAX_ROWS) { limited = true; break }
    const container = isContainer(node.value)
    const depthLimited = container && node.depth >= JSON_MAX_DEPTH
    if (depthLimited) limited = true
    const expanded = container && !depthLimited && (expandAll ? !opened.has(node.path) : opened.has(node.path))
    rows.push({ ...node, type: jsonType(node.value), length: jsonLength(node.value), container, expanded })
    if (container && expanded) {
      const keys = Object.keys(node.value as object)
      // 搜索可以定位后部字段；普通预览仅排入显示预算内的子节点。
      const matchedKeys = needle ? keys.filter(key => matches.has(childPath(node.path, key))) : keys
      const remaining = JSON_MAX_ROWS - rows.length - stack.length
      if (matchedKeys.length > remaining) limited = true
      const visibleKeys = matchedKeys.slice(0, Math.max(0, remaining))
      for (let i = visibleKeys.length - 1; i >= 0; i--) {
        const key = visibleKeys[i]!
        stack.push({ value: (node.value as Record<string, unknown>)[key], path: childPath(node.path, key), key, depth: node.depth + 1 })
      }
    }
  }
  return { rows, limited, searchLimited }
}
