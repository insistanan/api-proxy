export type CsvCell = string | number | boolean | null | undefined

function csvCell(value: CsvCell): string {
  let text = value == null ? '' : String(value)
  // 防止渠道名或错误文本在电子表格中被解释为公式；数值仍保持数值语义。
  if (typeof value === 'string' && /^\s*[=+\-@]/.test(text)) text = `'${text}`
  return /[",\r\n]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text
}

export function createCsv(headers: string[], rows: CsvCell[][]): string {
  return [headers, ...rows].map(row => row.map(csvCell).join(',')).join('\r\n')
}

/** 浏览器端导出已取得的脱敏数据，不上传正文，也不额外发起模型请求。 */
export function downloadCsv(filename: string, headers: string[], rows: CsvCell[][]): void {
  const blob = new Blob([`\uFEFF${createCsv(headers, rows)}`], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  link.style.display = 'none'
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}
