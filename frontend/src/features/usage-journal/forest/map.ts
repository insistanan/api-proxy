import type { ForestTree, TreeKind } from '@/types/forest'

export interface Point {
  x: number
  y: number
}
export interface Mark extends Point {
  id: number
  label: string
  count: number
  radius: number
  trees: number
  lighthouse: boolean
  monument: boolean
  favorite?: boolean
  destroyed: number
  damaged: number
}
export type View = 'island' | 'group' | 'world'
export const capacity = 96
export const islandsPerGroup = 8
export const treeAssetVersion = 1

export const colors: Record<TreeKind, string> = { usage: '#79b067', focus: '#a6cfda', rest: '#f5ac78' }

// 槽位固定在岛上。林间小径占据中轴，开始时优先选择岸边空地。
const slots = Array.from({ length: 168 }, (_, index) => ({
  x: ((index % 14) - 6.5) * 23 + Math.sin(index * 3.71) * 3,
  y: (Math.floor(index / 14) - 5.5) * 14 + Math.cos(index * 7.1) * 3
}))
  .filter(p => Math.hypot(p.x / 164, p.y / 86) < 1 && Math.abs(p.x - Math.sin(p.y / 30) * 20) > 10)
  .sort((a, b) => Math.hypot(a.x + 15, (a.y - 28) * 1.3) - Math.hypot(b.x + 15, (b.y - 28) * 1.3))
  .slice(0, capacity)
// 原有槽位不移动，需要时从相邻网格补足空地。
for (let index = 0; index < 130 && slots.length < capacity; index++) {
  const x = ((index % 13) - 6) * 19
  const y = (Math.floor(index / 13) - 4.5) * 13
  if (
    Math.hypot(x / 164, y / 86) < 1 &&
    Math.abs(x - Math.sin(y / 30) * 20) > 10 &&
    slots.every(point => Math.hypot(point.x - x, point.y - y) > 6)
  )
    slots.push({ x, y })
}

export function treePosition(tree: ForestTree): Point {
  return slots[tree.slot] ?? { x: 0, y: 0 }
}

export const plotPositions = slots

export function landmark(id: number, type: 'group' | 'world'): Point {
  if (id === 0) return { x: 0, y: 0 }
  const distance = Math.sqrt(id) * (type === 'group' ? 158 : 260)
  return { x: Math.cos(id * 2.39996) * distance, y: Math.sin(id * 2.39996) * distance * 0.7 }
}

export function ellipse(ctx: CanvasRenderingContext2D, x: number, y: number, rx: number, ry: number, color: string) {
  ctx.fillStyle = color
  ctx.beginPath()
  ctx.ellipse(x, y, rx, ry, 0, 0, Math.PI * 2)
  ctx.fill()
}

export function drawLandmark(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  size: number,
  type: 'lighthouse' | 'monument'
) {
  ctx.save()
  ctx.translate(x, y)
  ctx.scale(size, size)
  ellipse(ctx, 1, 1, 9, 4, '#385e50')
  ctx.fillStyle = '#f5dfb1'
  ctx.fillRect(-4.5, -21, 9, 21)
  if (type === 'lighthouse') {
    ellipse(ctx, 0, -22, 7, 5, '#c97453')
    ellipse(ctx, 0, -17, 3, 2, '#fff4a8')
  } else {
    ellipse(ctx, 0, -22, 7, 4, '#b9a06e')
    ellipse(ctx, 0, -14, 2, 4, '#b9a06e')
  }
  ctx.restore()
}

export function island(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  size: number,
  counts: number,
  detailed = false,
  ornaments: string[] = []
) {
  ctx.save()
  ctx.translate(x, y)
  ctx.scale(size, size)
  ellipse(ctx, 5, 10, 190, 111, '#7caeaa')
  ellipse(ctx, 0, 7, 177, 102, '#536e59')
  ellipse(ctx, 0, 0, 177, 100, '#cac69c')
  const land = ctx.createLinearGradient(0, -93, 0, 91)
  land.addColorStop(0, '#b0c77c')
  land.addColorStop(1, '#7da77a')
  ctx.fillStyle = land
  ctx.beginPath()
  ctx.ellipse(0, -4, 171, 92, 0, 0, Math.PI * 2)
  ctx.fill()
  if (detailed) {
    ctx.strokeStyle = '#e7d6a4'
    ctx.lineWidth = 11
    ctx.lineCap = 'round'
    ctx.beginPath()
    ctx.moveTo(-10, 84)
    ctx.bezierCurveTo(54, 42, -54, 0, 5, -85)
    ctx.stroke()
    for (let i = 0; i < 35; i++) {
      const angle = i * 2.39996
      const radius = Math.sqrt((i + 4) / 42)
      const px = Math.cos(angle) * radius * 160
      const py = Math.sin(angle) * radius * 82
      if (Math.abs(px) < 30) continue
      ellipse(ctx, px, py, 2 + (i % 4), 1.6, i % 5 === 0 ? '#ffe2a4' : '#66956c')
    }
    if (ornaments.includes('storm') && !ornaments.includes('stones')) ellipse(ctx, 13, 32, 16, 7, '#82aeb0')
    else if (ornaments.includes('trail') || ornaments.includes('stones'))
      for (let i = 0; i < 6; i++) ellipse(ctx, Math.sin(i * 0.9) * 17, 56 - i * 13, 4, 2, '#f6e5b8')
    if (ornaments.includes('aphids') && !ornaments.includes('ladybug')) {
      ellipse(ctx, -34, 48, 6, 4, '#a3ab55')
      ellipse(ctx, -30, 47, 2.3, 1.9, '#d5c771')
    }
    if (ornaments.includes('ladybug')) {
      ellipse(ctx, -34, 48, 5, 4, '#b65041')
      ellipse(ctx, -35, 47, 1.2, 1.1, '#422f26')
      ellipse(ctx, -32, 49, 1.2, 1.1, '#422f26')
    }
    if (ornaments.includes('squirrel')) {
      ellipse(ctx, 38, 38, 8, 5, '#a48762')
      for (let i = 0; i < 3; i++) ellipse(ctx, 34 + 4 * i, 37 + (i % 2), 1.8, 2.7, '#765031')
    }
    if (ornaments.includes('flower'))
      for (let i = 0; i < 5; i++) ellipse(ctx, -65 + i * 4, -32 + (i % 2) * 3, 2.6, 2.2, '#f9aab0')
    if (ornaments.includes('lantern')) drawLandmark(ctx, 110, 10, 1, 'lighthouse')
    if (ornaments.includes('monument')) drawLandmark(ctx, -114, 1, 1, 'monument')
  } else {
    for (let i = 0; i < Math.min(26, Math.ceil(counts / 4)); i++) {
      const angle = i * 2.39996
      const radius = Math.sqrt((i + 3) / 34)
      const px = Math.cos(angle) * radius * 152
      const py = Math.sin(angle) * radius * 75
      ellipse(ctx, px, py, 13 + (i % 8), 11 + (i % 4), i % 3 === 0 ? '#65956d' : '#487e61')
    }
  }
  ctx.restore()
}

export function drawTree(
  ctx: CanvasRenderingContext2D,
  tree: ForestTree,
  sprite: HTMLImageElement,
  selected: boolean,
  dimmed: boolean,
  motion: number,
  entering: boolean
) {
  const { x, y } = treePosition(tree)
  ctx.save()
  ctx.translate(x, y)
  ctx.globalAlpha = (dimmed ? 0.23 : 1) * (entering ? 1 - motion * 0.65 : 1)
  const scale = (0.85 + (tree.variant % 19) / 60) * (entering ? 1 - motion * 0.32 : 1)
  ctx.scale(scale, scale)
  if (selected) ellipse(ctx, 0, 0, 17, 8, '#fff4b7')
  ellipse(ctx, 2, 1, 12, 4, '#43644768')
  ctx.rotate(Math.sin(motion * 13) * motion * 0.18)
  if (tree.condition === 'destroyed') {
    // 残桩保留原来的地块与身份，重种后恢复既有外观。
    ctx.fillStyle = '#85664e'
    ctx.fillRect(-6, -9, 12, 10)
    ellipse(ctx, 0, -9, 6, 3, '#d3b68b')
    ellipse(ctx, 0, -9, 3.5, 1.7, '#ae8d65')
    ctx.strokeStyle = '#7a5d47'
    ctx.lineWidth = 3
    ctx.beginPath()
    ctx.moveTo(8, -2)
    ctx.lineTo(18, -7)
    ctx.lineTo(23, -4)
    ctx.stroke()
    ctx.restore()
    return
  }
  if (tree.condition === 'damaged') {
    ctx.strokeStyle = '#80664e'
    ctx.lineWidth = 3
    ctx.lineCap = 'round'
    ctx.beginPath()
    ctx.moveTo(0, 0)
    ctx.lineTo(1, -29)
    ctx.moveTo(1, -15)
    ctx.lineTo(-9, -23)
    ctx.moveTo(1, -20)
    ctx.lineTo(10, -25)
    ctx.stroke()
    ellipse(ctx, -7, -25, 6, 4, '#a7a269')
    ellipse(ctx, 4, -30, 5, 3, '#b6ad79')
    ellipse(ctx, 11, -9, 3, 1.5, '#bf9965')
    ellipse(ctx, -9, 1, 3, 1, '#a69563')
    ctx.restore()
    return
  }
  if (tree.assetVersion !== treeAssetVersion) {
    ellipse(ctx, 0, -10, 6, 9, '#a4afa6')
  } else if (tree.kind === 'usage' && tree.style < 4 && sprite.complete && sprite.naturalWidth > 0) {
    ctx.drawImage(sprite, tree.style * 144, 0, 144, 168, -19, -43, 38, 45)
    const leaves = ['#d9cf93', '#f5cc9e', '#aed0a0', '#c7dfa4']
    for (let i = 0; i < 1 + (tree.variant % 4); i++) {
      const offset = (tree.variant >> (i * 3)) % 12
      ellipse(
        ctx,
        -8 + offset * 1.25,
        -30 + ((offset + i * 5) % 12),
        1.1,
        1,
        leaves[(tree.variant + i) % leaves.length]
      )
    }
  } else {
    drawCrown(ctx, tree)
  }
  if (selected && tree.kind === 'rest' && tree.style === 0) {
    ellipse(ctx, 12, -32, 4, 3, '#f9eede')
    ellipse(ctx, 18, -40, 2, 2, '#f9eede')
  }
  if (tree.accessory === 'ribbon') {
    ctx.fillStyle = '#e98989'
    ctx.beginPath()
    ctx.moveTo(0, -16)
    ctx.lineTo(-8, -22)
    ctx.lineTo(-8, -12)
    ctx.closePath()
    ctx.fill()
    ctx.beginPath()
    ctx.moveTo(0, -16)
    ctx.lineTo(8, -22)
    ctx.lineTo(8, -12)
    ctx.closePath()
    ctx.fill()
    ellipse(ctx, 0, -16, 2.3, 2.3, '#f7ceb5')
  }
  if (tree.accessory === 'fireflies')
    for (let i = 0; i < 7; i++) {
      const angle = (i * Math.PI * 2) / 7
      ellipse(ctx, Math.cos(angle) * 19, -21 + Math.sin(angle) * 14, 2.1, 2.1, '#ffef99')
      ellipse(ctx, Math.cos(angle) * 19, -21 + Math.sin(angle) * 14, 0.8, 0.8, '#fffde1')
    }
  ctx.restore()
}

function drawCrown(ctx: CanvasRenderingContext2D, tree: ForestTree) {
  if (tree.style >= 4) {
    drawRareCrown(ctx, tree)
    return
  }
  const resting = tree.kind === 'rest'
  const inverted = resting && tree.style === 1
  const lying = resting && tree.style === 3
  const dx = lying ? 15 : 0
  const top = inverted ? -8 : lying ? -8 : -29
  ctx.strokeStyle = tree.kind === 'focus' ? '#d2d5bc' : '#76483d'
  ctx.lineWidth = 3.5
  ctx.lineCap = 'round'
  ctx.beginPath()
  ctx.moveTo(0, -1)
  ctx.quadraticCurveTo(dx * 0.35 + 4, -15, dx, inverted ? -26 : top)
  ctx.stroke()
  const tints = {
    usage: ['#62965c', '#517b5a', '#79a868', '#ad896c'],
    focus: ['#88baad', '#73a3b7', '#7eac9e', '#9eacbd'],
    rest: ['#df9476', '#d5aa67', '#edac89', '#de836e']
  }
  const tint = tints[tree.kind][tree.style]
  const tiers = !resting && tree.style === 1 ? 3 : 1
  for (let i = 0; i < tiers; i++) ellipse(ctx, dx, top - i * 5, (lying ? 17 : 12) - 2 * i, lying ? 6 : 8, tint)
  for (let i = 0; i < 4; i++) {
    const shift = (tree.variant % 11) / 6
    const highlight = tree.kind === 'usage' ? '#a3c88b' : resting ? '#f7c99e' : '#bddad1'
    ellipse(ctx, dx - 7 + i * 4 + shift, top - 4 - (i % 2) * 3, 5, 5, highlight)
  }
  if (tree.kind === 'focus') {
    if (tree.style === 2) {
      ellipse(ctx, -7, -19, 8, 6, '#a0c5b3')
      ellipse(ctx, 7, -25, 8, 6, '#c3ddc1')
    }
    if (tree.style === 3) {
      ellipse(ctx, 0, -18, 6, 6, '#f1d298')
      ellipse(ctx, 0, -18, 3, 4, '#fff3ba')
    }
  }
  if (inverted) {
    ctx.strokeStyle = '#76483d'
    ctx.lineWidth = 2
    for (const root of [-8, 0, 8]) {
      ctx.beginPath()
      ctx.moveTo(0, -21)
      ctx.lineTo(root, -34)
      ctx.stroke()
    }
  }
  if (resting && tree.style === 0) {
    ctx.save()
    ctx.rotate(0.2)
    ctx.fillStyle = '#fae8bd'
    ctx.fillRect(-8, -25, 10, 5)
    ctx.restore()
  }
  if (resting && tree.style === 2) {
    ellipse(ctx, -7, -16, 3, 6, '#f6ddb1')
    ellipse(ctx, 6, -16, 3, 5, '#90b4ae')
  }
}

function drawRareCrown(ctx: CanvasRenderingContext2D, tree: ForestTree) {
  ctx.strokeStyle = '#846455'
  ctx.lineWidth = 3
  ctx.lineCap = 'round'
  ctx.beginPath()
  ctx.moveTo(0, 0)
  ctx.quadraticCurveTo(4, -16, 0, -32)
  ctx.stroke()
  if (tree.kind === 'rest') {
    ctx.strokeStyle = '#cb8a61'
    ctx.lineWidth = 10
    ctx.beginPath()
    ctx.ellipse(0, -29, 11, 10, -0.2, 0, Math.PI * 2)
    ctx.stroke()
    ctx.strokeStyle = '#f4a9b9'
    ctx.lineWidth = 7
    ctx.beginPath()
    ctx.ellipse(0, -30, 11, 10, -0.2, 0.1, Math.PI * 1.75)
    ctx.stroke()
    for (let i = 0; i < 8; i++) {
      const a = i * 0.8
      ctx.strokeStyle = i % 2 ? '#fff2b5' : '#8cc9ba'
      ctx.lineWidth = 1.5
      const x = Math.cos(a) * 11,
        y = -30 + Math.sin(a) * 10
      ctx.beginPath()
      ctx.moveTo(x, y)
      ctx.lineTo(x + 2, y - 2)
      ctx.stroke()
    }
  } else if (tree.kind === 'focus') {
    ellipse(ctx, -8, -23, 9, 13, '#829aab')
    ellipse(ctx, 8, -28, 9, 14, '#aec7c6')
    ctx.fillStyle = '#ffe6a7'
    ctx.beginPath()
    ctx.arc(0, -37, 8, 0.5, 5.8)
    ctx.quadraticCurveTo(-2, -36, 7, -33)
    ctx.fill()
  } else if (tree.style === 5) {
    for (let i = 0; i < 7; i++) {
      const x = (i - 3) * 5
      ctx.strokeStyle = i % 2 ? '#86c8b9' : '#b5d7ab'
      ctx.lineWidth = 4
      ctx.beginPath()
      ctx.moveTo(0, -36)
      ctx.quadraticCurveTo(x * 1.5, -43, x, -12 + Math.abs(i - 3) * 2)
      ctx.stroke()
      ellipse(ctx, x, -14 + Math.abs(i - 3) * 2, 1.7, 2, '#fff1b3')
    }
  } else {
    for (let i = 0; i < 7; i++) {
      const a = i * 2.4
      ctx.fillStyle = i % 2 ? '#db804f' : '#efa765'
      const x = Math.cos(a) * 9,
        y = -29 + Math.sin(a) * 8
      ctx.beginPath()
      for (let p = 0; p < 10; p++) {
        const r = p % 2 ? 5 : 10,
          angle = (p * Math.PI) / 5
        const px = x + Math.cos(angle) * r,
          py = y + Math.sin(angle) * r
        if (p === 0) ctx.moveTo(px, py)
        else ctx.lineTo(px, py)
      }
      ctx.closePath()
      ctx.fill()
    }
  }
}
