// Images 渠道自定义请求参数。界面按行编辑，提交时编译成对象；类型规则与后端保持一致。
export type ImagesExtraParamRow = {
  key: string
  value: string
}

const jsonNumberPattern = /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/

export function rowsFromImagesExtraParams(params?: Record<string, string> | null): ImagesExtraParamRow[] {
  return Object.entries(params || {}).map(([key, value]) => ({
    key,
    value: value ?? ''
  }))
}

export function compileImagesExtraParams(rows: ImagesExtraParamRow[]): { params: Record<string, string>; error: string } {
  const params: Record<string, string> = {}
  for (const row of rows) {
    const key = row.key.trim()
    if (!key) return { params: {}, error: '自定义参数名不能为空' }
    if (key.includes('\n') || key.includes('\r')) return { params: {}, error: '自定义参数名不能包含换行' }
    if (key === 'model' || key === 'stream') return { params: {}, error: `自定义参数不能使用保留字段 ${key}` }
    if (Object.prototype.hasOwnProperty.call(params, key)) {
      return { params: {}, error: `自定义参数名重复: ${key}` }
    }
    const value = row.value.trim()
    const valueError = validateImagesExtraParamValue(key, value)
    if (valueError) return { params: {}, error: valueError }
    params[key] = value
  }
  return { params, error: '' }
}

function validateImagesExtraParamValue(key: string, value: string): string {
  if (!value || !looksLikeStructuredValue(value)) return ''
  try {
    JSON.parse(value)
  } catch {
    return `自定义参数 ${key} 的值不是合法 JSON`
  }
  return ''
}

function looksLikeStructuredValue(value: string): boolean {
  if (value === 'true' || value === 'false' || value === 'null' || jsonNumberPattern.test(value)) return true
  const first = value[0]
  return first === '{' || first === '[' || first === '"'
}
