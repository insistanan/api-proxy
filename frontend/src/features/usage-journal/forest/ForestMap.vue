<template>
  <div ref="frame" class="forest-map relative min-w-0 overflow-hidden rounded-card">
    <canvas
      ref="canvas"
      class="block size-full touch-none"
      role="img"
      :aria-label="caption"
      @pointerdown="down"
      @pointermove="move"
      @pointerup="up"
      @pointercancel="cancel"
      @wheel="wheel"
    ></canvas>
    <p
      v-if="spriteError && view === 'island'"
      class="map-tools absolute bottom-3 left-3 max-w-32 rounded-control px-2 py-1 text-xs"
      role="status"
    >
      素材读取失败，已显示简化树形。
    </p>
    <div class="map-tools absolute bottom-3 right-3 flex gap-1 rounded-control p-1">
      <UiButton size="small" variant="text" aria-label="缩小地图" @click="zoom(0.8)">−</UiButton>
      <UiButton size="small" variant="text" aria-label="放大地图" @click="zoom(1.25)">＋</UiButton>
      <UiButton size="small" variant="text" @click="reset(Boolean(selected))">取景</UiButton>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import UiButton from '@/shared/ui/button/Button.vue'
import type { ForestTree, TreeKind } from '@/types/forest'
import {
  capacity,
  drawLandmark,
  drawTree,
  ellipse,
  island,
  landmark,
  plotPositions,
  treePosition,
  type Mark,
  type Point,
  type View
} from '@/features/usage-journal/forest/map'
import sprites from '@/features/usage-journal/forest/trees.webp'

const props = defineProps<{
  view: View
  scene: string
  marks: Mark[]
  trees: ForestTree[]
  selected?: ForestTree
  filter?: TreeKind
  ornaments: string[]
  moving: boolean
  caption: string
}>()
const emit = defineEmits<{ mark: [id: number]; tree: [tree: ForestTree]; plot: [slot: number] }>()
const frame = ref<HTMLDivElement>()
const canvas = ref<HTMLCanvasElement>()
const spriteError = ref(false)
const sprite = document.createElement('img')
const terrainCache = new Map<string, HTMLCanvasElement>()
const pointers = new Map<number, Point>()
type Camera = Point & { scale: number }
const cameras = new Map<string, Camera>()
let islandCamera: Camera | undefined
let center = { x: 0, y: 0 }
let scale = 1
let width = 1
let height = 1
let observer: ResizeObserver | undefined
let renderID = 0
let origin = { x: 0, y: 0 }
let moved = false
let hasCamera = false
let adjusted = false
let animation = { id: 0, until: 0, enter: false }
const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')

function draw() {
  const target = canvas.value
  const ctx = target?.getContext('2d')
  if (!target || !ctx) return
  ctx.setTransform(target.width / width, 0, 0, target.height / height, 0, 0)
  ctx.clearRect(0, 0, width, height)
  ctx.save()
  ctx.translate(width / 2, height / 2)
  ctx.scale(scale, scale)
  ctx.translate(-center.x, -center.y)
  if (props.view === 'island') {
    paintIsland(ctx, 0, 0, 1, 0, true)
    if (props.moving) {
      const blocked = new Set(props.trees.map(tree => tree.slot))
      plotPositions.forEach((point, slot) => {
        if (!blocked.has(slot)) ellipse(ctx, point.x, point.y, 4, 2.5, '#fff3af')
      })
    }
    const sorted = [...props.trees].sort((a, b) => treePosition(a).y - treePosition(b).y)
    const now = performance.now()
    for (const tree of sorted) {
      if (!inFrame(treePosition(tree), 48)) continue
      const progress = animation.id === tree.id ? Math.max(0, animation.until - now) / 420 : 0
      drawTree(
        ctx,
        tree,
        sprite,
        props.selected?.id === tree.id,
        Boolean(props.filter && props.filter !== tree.kind),
        progress,
        animation.enter
      )
    }
    if (now < animation.until && !reducedMotion.matches) schedule()
  } else {
    drawMarks(ctx)
  }
  ctx.restore()
}

function paintIsland(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  size: number,
  count: number,
  detailed = false
) {
  const density = Math.max(0, Math.min(26, Math.ceil(count / 4)))
  const features = detailed ? props.ornaments : []
  const resolution = detailed ? (scale > 2 ? 3 : 2) : 1
  const key = detailed ? `detail:${resolution}:${features.join(',')}` : `canopy:${density}`
  let image = terrainCache.get(key)
  if (!image) {
    image = document.createElement('canvas')
    image.width = 400 * resolution
    image.height = 254 * resolution
    const layer = image.getContext('2d')
    if (!layer) {
      island(ctx, x, y, size, count, detailed, features)
      return
    }
    layer.scale(resolution, resolution)
    island(layer, 200, 127, 1, density * 4, detailed, features)
    if (terrainCache.size === 32) terrainCache.clear()
    terrainCache.set(key, image)
  }
  ctx.drawImage(image, x - 200 * size, y - 127 * size, 400 * size, 254 * size)
}

function inFrame(point: Point, radius: number) {
  return (
    Math.abs(point.x - center.x) < width / scale / 2 + radius &&
    Math.abs(point.y - center.y) < height / scale / 2 + radius
  )
}

function drawMarks(ctx: CanvasRenderingContext2D) {
  const marks = props.marks.filter(mark => inFrame(mark, mark.radius))
  if (props.view === 'group') {
    ctx.beginPath()
    props.marks.forEach((mark, index) => (index ? ctx.lineTo(mark.x, mark.y) : ctx.moveTo(mark.x, mark.y)))
    ctx.strokeStyle = '#edf6ce'
    ctx.lineWidth = 2 / scale
    ctx.setLineDash([5 / scale, 6 / scale])
    ctx.stroke()
    ctx.setLineDash([])
  }
  for (const mark of marks) {
    if (props.view === 'group') {
      const size = mark.radius / 190
      paintIsland(ctx, mark.x, mark.y, size, mark.trees)
      if (mark.lighthouse)
        drawLandmark(ctx, mark.x + 110 * size, mark.y + 10 * size, Math.max(size, 0.58 / scale), 'lighthouse')
      if (mark.monument) drawLandmark(ctx, mark.x - 114 * size, mark.y + size, Math.max(size, 0.58 / scale), 'monument')
    } else {
      const positions = Array.from({ length: mark.count }, (_, i) => landmark(i, 'group'))
      const islandSize = mark.count === 1 ? 1 : 0.42
      const left = Math.min(...positions.map(p => p.x)) - 190 * islandSize
      const right = Math.max(...positions.map(p => p.x)) + 190 * islandSize
      const top = Math.min(...positions.map(p => p.y)) - 125 * islandSize
      const bottom = Math.max(...positions.map(p => p.y)) + 125 * islandSize
      const projection = (mark.radius * 1.8) / Math.max(right - left, bottom - top)
      const size = projection * islandSize
      for (let i = 0; i < mark.count; i++) {
        const position = positions[i]
        const x = mark.x + (position.x - (left + right) / 2) * projection
        const y = mark.y + (position.y - (top + bottom) / 2) * projection
        const planted = mark.trees + mark.destroyed
        const livingShare = planted ? mark.trees / planted : 0
        paintIsland(ctx, x, y, size, Math.min(capacity, planted - i * capacity) * livingShare)
        if (i === 0 && mark.lighthouse) drawLandmark(ctx, x + 110 * size, y, Math.max(size, 0.48 / scale), 'lighthouse')
        if (i === 0 && mark.monument) drawLandmark(ctx, x - 114 * size, y, Math.max(size, 0.48 / scale), 'monument')
      }
    }
    if (mark.favorite)
      ellipse(ctx, mark.x - mark.radius * 0.46, mark.y - mark.radius * 0.28, 3 / scale, 3 / scale, '#ffe190')
    const textSize = 12 / scale
    if (mark.radius * scale < 16) continue
    ctx.font = `600 ${textSize}px system-ui, sans-serif`
    ctx.textAlign = 'center'
    ctx.fillStyle = '#18382c'
    const labelWidth = Math.min(ctx.measureText(mark.label).width, mark.radius * 2) + 16 / scale
    const y = mark.y + mark.radius * 0.83 + textSize
    ctx.beginPath()
    ctx.roundRect(mark.x - labelWidth / 2, y - textSize, labelWidth, textSize * 1.45, 5 / scale)
    ctx.fillStyle = '#f1f5de'
    ctx.fill()
    ctx.fillStyle = '#18382c'
    ctx.fillText(mark.label, mark.x, y + 1 / scale, mark.radius * 2)
    if (mark.damaged || mark.destroyed) {
      ctx.font = `${10 / scale}px system-ui, sans-serif`
      const note = `${mark.damaged + mark.destroyed} 处待照料`
      const noteWidth = ctx.measureText(note).width + 12 / scale
      ctx.fillStyle = '#fff1d7'
      ctx.fillRect(mark.x - noteWidth / 2, y + 8 / scale, noteWidth, 15 / scale)
      ctx.fillStyle = '#91472f'
      ctx.fillText(note, mark.x, y + 19 / scale)
    }
  }
}

function schedule() {
  if (renderID) return
  renderID = window.requestAnimationFrame(() => {
    renderID = 0
    draw()
  })
}

function reset(useSelection = false) {
  if (width <= 1 || height <= 32) return
  if (props.view === 'island') {
    const tree = useSelection ? props.selected : undefined
    if (tree || (props.trees.length <= 3 && !props.moving)) {
      const point = tree ? treePosition(tree) : { x: -16, y: 30 }
      const subjects = tree ? [point] : props.trees.map(treePosition)
      if (!subjects.length) subjects.push(point)
      const left = Math.min(...subjects.map(subject => subject.x)) - 52
      const right = Math.max(...subjects.map(subject => subject.x)) + 52
      const top = Math.min(...subjects.map(subject => subject.y)) - 62
      const bottom = Math.max(...subjects.map(subject => subject.y)) + 32
      center = { x: (left + right) / 2, y: (top + bottom) / 2 }
      scale = Math.min(5.5, width / (right - left), (height - 32) / (bottom - top))
    } else {
      center = { x: 0, y: -12 }
      scale = Math.min(width / 420, height / 278)
    }
  } else {
    let left = props.marks.length ? Infinity : -80
    let right = props.marks.length ? -Infinity : 80
    let top = props.marks.length ? Infinity : -85
    let bottom = props.marks.length ? -Infinity : 85
    for (const mark of props.marks) {
      left = Math.min(left, mark.x - mark.radius)
      right = Math.max(right, mark.x + mark.radius)
      top = Math.min(top, mark.y - mark.radius)
      bottom = Math.max(bottom, mark.y + mark.radius)
    }
    center = { x: (left + right) / 2, y: (top + bottom) / 2 }
    const fit = Math.min(width / (right - left + 90), height / (bottom - top + 96))
    scale = Math.min(fit, 4)
  }
  hasCamera = true
  schedule()
}

function restore(camera: Camera) {
  center = { x: camera.x, y: camera.y }
  scale = camera.scale
  adjusted = true
  hasCamera = true
  schedule()
}

function focusTree() {
  if (!props.selected) return
  if (hasCamera) islandCamera ??= { ...center, scale }
  adjusted = true
  if (props.selected.kind === 'rest' && !reducedMotion.matches)
    animation = { id: props.selected.id, until: performance.now() + 420, enter: false }
  reset(true)
}

function restoreIsland() {
  if (islandCamera && !props.moving) restore(islandCamera)
  else reset()
  islandCamera = undefined
}

function zoom(multiplier: number, at = { x: width / 2, y: height / 2 }) {
  adjusted = true
  const oldScale = scale
  const minimum = props.view === 'world' ? Math.min(0.1, scale) : Math.min(0.25, scale)
  scale = Math.max(minimum, Math.min(7, oldScale * multiplier))
  center.x += (at.x - width / 2) * (1 / oldScale - 1 / scale)
  center.y += (at.y - height / 2) * (1 / oldScale - 1 / scale)
  schedule()
}

function screen(event: PointerEvent | WheelEvent): Point {
  const box = canvas.value?.getBoundingClientRect()
  return { x: event.clientX - (box?.left ?? 0), y: event.clientY - (box?.top ?? 0) }
}
function world(point: Point) {
  return { x: center.x + (point.x - width / 2) / scale, y: center.y + (point.y - height / 2) / scale }
}
function wheel(event: WheelEvent) {
  event.preventDefault()
  zoom(Math.exp(-Math.max(-100, Math.min(100, event.deltaY)) / 320), screen(event))
}
function down(event: PointerEvent) {
  if (event.button !== 0) return
  const point = screen(event)
  if (!pointers.size) {
    origin = point
    moved = false
  } else moved = true
  pointers.set(event.pointerId, point)
  canvas.value?.setPointerCapture(event.pointerId)
}
function move(event: PointerEvent) {
  const previous = pointers.get(event.pointerId)
  if (!previous) return
  const point = screen(event)
  pointers.set(event.pointerId, point)
  const other = [...pointers].find(([id]) => id !== event.pointerId)?.[1]
  if (other) {
    const start = Math.hypot(other.x - previous.x, other.y - previous.y)
    if (start > 0)
      zoom(Math.hypot(other.x - point.x, other.y - point.y) / start, {
        x: (other.x + point.x) / 2,
        y: (other.y + point.y) / 2
      })
  } else {
    if (Math.hypot(point.x - origin.x, point.y - origin.y) > 4) moved = true
    if (moved) {
      adjusted = true
      center.x -= (point.x - previous.x) / scale
      center.y -= (point.y - previous.y) / scale
      schedule()
    }
  }
}
function up(event: PointerEvent) {
  if (!pointers.delete(event.pointerId) || moved) return
  const p = world(screen(event))
  if (props.view !== 'island') {
    const marks = props.marks.map(mark => ({ mark, distance: Math.hypot(p.x - mark.x, p.y - mark.y) }))
    const hit = marks
      .filter(({ mark, distance }) => distance <= Math.max(mark.radius, 20 / scale))
      .sort((a, b) => a.distance - b.distance)[0]
    if (hit) emit('mark', hit.mark.id)
  } else if (props.moving) {
    const options = plotPositions.map((point, slot) => ({ slot, distance: Math.hypot(p.x - point.x, p.y - point.y) }))
    const hit = options
      .filter(point => point.distance < 14 && !props.trees.some(tree => tree.slot === point.slot))
      .sort((a, b) => a.distance - b.distance)[0]
    if (hit) emit('plot', hit.slot)
  } else {
    const options = props.trees.map(tree => {
      const point = treePosition(tree)
      return { tree, distance: Math.hypot(p.x - point.x, (p.y - point.y + 18) * 0.75) }
    })
    const hit = options.filter(point => point.distance < 19).sort((a, b) => a.distance - b.distance)[0]
    if (hit) emit('tree', hit.tree)
  }
}
function cancel(event: PointerEvent) {
  pointers.delete(event.pointerId)
}
function spriteFailed() {
  spriteError.value = true
}

watch(
  () => props.scene,
  (next, previous) => {
    if (hasCamera) cameras.set(previous, islandCamera ?? { ...center, scale })
    islandCamera = undefined
    adjusted = false
    const known = cameras.get(next)
    if (known) restore(known)
    else reset()
  }
)
watch(
  () => [props.marks, props.trees, props.selected, props.filter, props.ornaments, props.moving],
  () => {
    if (!adjusted && !props.selected) reset()
    else schedule()
  }
)
watch(
  () => props.trees,
  (trees, previous) => {
    if (props.view !== 'island' || reducedMotion.matches) return
    const old = previous[previous.length - 1]
    const latest = trees[trees.length - 1]
    if (
      latest &&
      latest.id > (old?.id ?? 0) &&
      (!old || old.island === latest.island) &&
      Math.abs(Date.now() - latest.planted) < 45000
    ) {
      animation = { id: latest.id, until: performance.now() + 420, enter: true }
      schedule()
    }
  }
)
onMounted(() => {
  if (!frame.value || !canvas.value) return
  sprite.addEventListener('load', schedule)
  sprite.addEventListener('error', spriteFailed)
  sprite.src = sprites
  observer = new ResizeObserver(([entry]) => {
    if (!canvas.value || !entry.contentRect.width || !entry.contentRect.height) return
    const dpr = Math.min(2, window.devicePixelRatio || 1)
    width = entry.contentRect.width
    height = entry.contentRect.height
    canvas.value.width = Math.round(width * dpr)
    canvas.value.height = Math.round(height * dpr)
    if (!hasCamera || !adjusted) reset(Boolean(props.selected))
    else schedule()
  })
  observer.observe(frame.value)
})
onUnmounted(() => {
  sprite.removeEventListener('load', schedule)
  sprite.removeEventListener('error', spriteFailed)
  observer?.disconnect()
  window.cancelAnimationFrame(renderID)
})
defineExpose({ focusTree, restoreIsland })
</script>

<style scoped>
.forest-map {
  height: clamp(19rem, 56vw, 36rem);
  background:
    repeating-radial-gradient(ellipse at 60% 20%, transparent, transparent 38px, #cbe4ca2a 39px, transparent 41px),
    radial-gradient(ellipse at 22% 10%, #a9cbbc, #74aeb0 56%, #497f92);
}
:global(html[data-theme-mode='dark']) .forest-map {
  background:
    repeating-radial-gradient(ellipse at 60% 20%, transparent, transparent 38px, #85b7a521 39px, transparent 41px),
    radial-gradient(ellipse at 22% 10%, #497b79, #315865 65%, #203b51);
}
.map-tools {
  background: var(--surface-bright);
}
</style>
