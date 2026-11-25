import type { JournalDay, JournalDetail, JournalUsage } from '@/types/usage-journal'
import type { ForestDay } from '@/types/forest'

export const number = new Intl.NumberFormat('zh-CN')
const percent = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1 })

export const inputTotal = (usage: JournalUsage) => usage.input + usage.cacheCreation + usage.cacheRead
export const allTokens = (usage: JournalUsage) => inputTotal(usage) + usage.output
export const tokenShare = (part: number, total: number) => total ? percent.format(part / total) : '—'

export function coverageText(day: JournalDay) {
  switch (day.coverage) {
    case 'unrecorded': return '未记录'
    case 'partial': return '记录不完整'
    case 'open': return '更新中'
    case 'complete': return day.tokens ? '记录完整' : '无用量'
  }
}

export function ticketVersion(detail: JournalDetail) {
  return [detail.timeZone, `第 ${number.format(detail.revision)} 版`, detail.lateUpdate ? '已补记迟到用量' : ''].filter(Boolean).join(' · ')
}

export function reportRows(detail: JournalDetail, timeZone: string): Array<[string, string]> {
  const usage = detail.upstream
  const peak = detail.peakHour && new Date(detail.peakHour).toLocaleTimeString('zh-CN', {
    timeZone, hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZoneName: 'shortOffset'
  })
  return [
    ['输入合计', number.format(inputTotal(usage))],
    ['未缓存输入', number.format(usage.input)],
    ['缓存创建', number.format(usage.cacheCreation)],
    ['缓存读取', number.format(usage.cacheRead)],
    ['输出', number.format(usage.output)],
    ['缓存命中', tokenShare(usage.cacheRead, inputTotal(usage))],
    ['最活跃时段', peak ? `${peak} 起的一小时` : '—'],
    ['估算用量（不计入合计）', number.format(allTokens(detail.estimated))],
    ['未报告用量的尝试', number.format(detail.missingAttempts)]
  ]
}

export async function exportTicket(detail: JournalDetail, timeZone: string, growth?: ForestDay) {
  const canvas = document.createElement('canvas')
  canvas.width = 900
  const models = detail.models.slice(0, 5)
  canvas.height = 990 + models.length * 44
  const context = canvas.getContext('2d')
  if (!context) throw new Error('无法创建票据图像')
  const font = '"Microsoft YaHei", "PingFang SC", system-ui, sans-serif'
  context.fillStyle = '#173a2e'
  context.fillRect(0, 0, canvas.width, canvas.height)
  context.fillStyle = '#fcf9ed'
  context.beginPath()
  context.roundRect(72, 42, 756, canvas.height - 84, 26)
  context.fill()
  context.fillStyle = '#264337'
  context.font = `600 27px ${font}`
  context.fillText('API PROXY · 每日用量票据', 120, 110)
  context.font = `24px ${font}`
  context.fillText(`${detail.date} · ${coverageText(detail)}`, 120, 163)
  context.font = `700 64px ${font}`
  context.fillText(number.format(detail.tokens), 120, 250, 645)
  context.font = `22px ${font}`
  context.fillText('Token', 120, 285)
  context.setLineDash([7, 7])
  context.strokeStyle = '#8c998c'
  context.beginPath()
  context.moveTo(120, 315)
  context.lineTo(780, 315)
  context.stroke()
  const row = (label: string, value: string, y: number) => {
    context.textAlign = 'left'
    context.fillText(label, 120, y, 420)
    context.textAlign = 'right'
    context.fillText(value, 780, y, 235)
  }
  context.font = `24px ${font}`
  reportRows(detail, timeZone).forEach(([label, value], index) => row(label, value, 360 + index * 43))
  models.forEach((model, index) => row(model.name || '未报告模型', `${number.format(model.tokens)} · ${tokenShare(model.tokens, detail.tokens)}`, 765 + index * 44))
  const growthY = 805 + models.length * 44
  context.textAlign = 'left'
  context.font = `600 26px ${font}`
  context.fillText('森林足迹', 120, growthY)
  context.font = `22px ${font}`
  context.fillText(growth ? `这一天，海上多了 ${number.format(Object.values(growth.counts).reduce((sum, count) => sum + count, 0))} 棵树。` : '森林记录暂不可用', 120, growthY + 40, 650)
  context.font = `20px ${font}`
  context.textAlign = 'left'
  context.fillText(ticketVersion(detail), 120, canvas.height - 76, 650)
  const blob = await new Promise<Blob>((resolve, reject) => canvas.toBlob((value) => {
    if (value) resolve(value)
    else reject(new Error('无法导出 PNG 票据'))
  }))
  const url = window.URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `用量票据-${detail.date}.png`
  link.click()
  window.setTimeout(() => window.URL.revokeObjectURL(url), 1000)
}
