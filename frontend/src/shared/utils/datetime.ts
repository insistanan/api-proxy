/** 全站日期时间格式化的唯一来源。 */
export type DateInput = string | number | Date

/** 完整日期时间，例如 2026/11/14 15:30:00。 */
export function formatDateTime(value: DateInput): string {
  return new Date(value).toLocaleString('zh-CN', { hour12: false })
}

/** 短日期时间，例如 11/14 15:30。 */
export function formatShortDateTime(value: DateInput): string {
  return new Date(value).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

/** 仅时分，例如 15:30。 */
export function formatClockTime(value: DateInput): string {
  return new Date(value).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
}

/** datetime-local 控件所需的本地时间，不经过 UTC 换算。 */
export function toDateTimeLocal(value: DateInput): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}
