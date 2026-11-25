<template>
  <section class="panel-surface p-4 sm:p-7" aria-labelledby="calendar-title">
    <div class="mb-6 flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 id="calendar-title" class="text-lg font-semibold">{{ year }} · 灵感足迹</h2>
        <p class="mt-1 text-xs text-muted-foreground">{{ number.format(total) }} Token · {{ activeDays }} 个活跃日</p>
      </div>
      <div class="flex flex-wrap gap-1">
        <UiButton size="small" variant="text" :disabled="year <= firstYear || busy" @click="emit('year', year - 1)">
          上一年
        </UiButton>
        <UiButton size="small" variant="text" :disabled="year >= currentYear || busy" @click="emit('year', year + 1)">
          下一年
        </UiButton>
        <UiButton size="small" variant="outlined" :disabled="busy" @click="emit('today')">今天</UiButton>
      </div>
    </div>
    <div ref="scroll" class="overflow-x-auto pb-3">
      <svg
        class="calendar-picture"
        :viewBox="`0 0 ${columns * unit + 4} 158`"
        role="group"
        aria-label="每日用量，点击日期查看票据"
        @keydown="moveFocus"
      >
        <text v-for="month in months" :key="month.name" :x="month.x" y="12" class="month-label">{{ month.name }}</text>
        <g
          v-for="(day, i) in days"
          :key="day.date"
          role="button"
          :tabindex="day.date === focusDate ? 0 : -1"
          :aria-label="dayLabel(day)"
          :data-date="day.date"
          @focus="focusDate = day.date"
          @click="select(day.date)"
          @keydown.enter.prevent="select(day.date)"
          @keydown.space.prevent="select(day.date)"
        >
          <title>{{ dayLabel(day) }}</title>
          <rect
            :x="position(i).x - 7"
            :y="position(i).y - 7"
            width="14"
            height="14"
            rx="3"
            class="day-cell"
            :data-level="heat(day)"
            :data-coverage="day.coverage"
            :data-eaten="eaten.has(i)"
            :data-selected="day.date === selected"
          />
          <path
            v-if="day.coverage === 'partial'"
            :d="`M${position(i).x - 5},${position(i).y + 5}l10,-10`"
            class="partial-line"
          />
        </g>
        <g v-if="running && body.length" class="snake" aria-hidden="true">
          <polyline
            :points="body.map(p => `${p.x},${p.y}`).join(' ')"
            fill="none"
            stroke="var(--primary)"
            stroke-width="11"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
          <g :transform="`translate(${head.x},${head.y}) rotate(${heading})`">
            <ellipse rx="8" ry="6.5" fill="var(--primary)" />
            <circle cx="3" cy="-3.2" r="1.7" fill="var(--primary-foreground)" />
            <circle cx="3" cy="3.2" r="1.7" fill="var(--primary-foreground)" />
            <circle cx="3.6" cy="-3.2" r="0.7" fill="var(--foreground)" />
            <circle cx="3.6" cy="3.2" r="0.7" fill="var(--foreground)" />
          </g>
        </g>
      </svg>
    </div>
    <div class="mt-3 flex flex-wrap items-center gap-4 text-xs text-muted-foreground">
      <span class="flex items-center gap-1.5">
        少
        <i v-for="n in 5" :key="n" class="heat-sample" :data-level="n - 1"></i>
        多
      </span>
      <span>空框为未记录 · 斜线为记录不完整</span>
      <UiButton v-if="!reduced" class="ml-auto" size="small" variant="text" :disabled="!activeDays" @click="toggle">
        {{ running ? '暂停小蛇' : '让小蛇出发' }}
      </UiButton>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'
import UiButton from '@/shared/ui/button/Button.vue'
import type { JournalDay } from '@/types/usage-journal'
import { coverageText, number } from '@/features/usage-journal/presentation'

const props = defineProps<{
  days: JournalDay[]
  selected: string
  firstYear: number
  currentYear: number
  busy: boolean
}>()
const emit = defineEmits<{ select: [date: string]; year: [year: number]; today: [] }>()
const unit = 18
const year = computed(() => Number(props.days[0]?.date.slice(0, 4) || props.currentYear))
const offset = computed(() => (new Date(Date.UTC(year.value, 0, 1)).getUTCDay() + 6) % 7)
const columns = computed(() => Math.ceil((props.days.length + offset.value) / 7))
const total = computed(() => props.days.reduce((sum, day) => sum + day.tokens, 0))
const activeDays = computed(() => props.days.filter(day => day.tokens > 0).length)
const maximum = computed(() => Math.max(1, ...props.days.map(day => day.tokens)))
const months = computed(() =>
  props.days.flatMap((day, index) =>
    day.date.endsWith('-01') ? [{ name: `${Number(day.date.slice(5, 7))}月`, x: position(index).x - 7 }] : []
  )
)
const heat = (day: JournalDay) =>
  day.tokens ? Math.max(1, Math.ceil((Math.log1p(day.tokens) / Math.log1p(maximum.value)) * 4)) : 0
const dayLabel = (day: JournalDay) => `${day.date} · ${number.format(day.tokens)} Token · ${coverageText(day)}`
function position(index: number) {
  return { x: Math.floor((index + offset.value) / 7) * unit + 9, y: ((index + offset.value) % 7) * unit + 32 }
}

const scroll = ref<HTMLDivElement>()
const focusDate = ref('')
const running = ref(false)
const reduced = ref(window.matchMedia('(prefers-reduced-motion: reduce)').matches)
const eaten = shallowRef(new Set<number>())
const body = shallowRef<Array<{ x: number; y: number }>>([])
const head = computed(() => body.value[body.value.length - 1] || { x: 0, y: 0 })
const heading = ref(0)
const motion = window.matchMedia('(prefers-reduced-motion: reduce)')
let frame = 0
let started = 0
let path: Array<{ x: number; y: number; index: number }> = []
let lastStep = -1
let pausedByUser = false

function prepare() {
  // 按相邻格子蛇形前进；从第一格有用量的附近出发，避免长时间穿过空白月份。
  const grid = Array.from({ length: columns.value * 7 }, (_, n) => {
    const column = Math.floor(n / 7)
    const row = column % 2 ? 6 - (n % 7) : n % 7
    return { x: column * unit + 9, y: row * unit + 32, index: column * 7 + row - offset.value }
  })
  const foods = grid.flatMap((point, i) => (props.days[point.index]?.tokens > 0 ? [i] : []))
  path = foods.length ? grid.slice(Math.max(0, foods[0] - 5), Math.min(grid.length, foods[foods.length - 1] + 5)) : []
}

function stop() {
  window.cancelAnimationFrame(frame)
  running.value = false
  eaten.value = new Set()
  body.value = []
  started = 0
  lastStep = -1
}
function start() {
  stop()
  prepare()
  if (reduced.value || document.hidden || path.length < 2 || pausedByUser) return
  running.value = true
  frame = window.requestAnimationFrame(tick)
}
function tick(now: number) {
  if (!running.value) return
  started ||= now
  const travel = (now - started) / 110
  const step = Math.min(path.length - 1, Math.floor(travel))
  if (step !== lastStep) {
    const consumed = new Set(eaten.value)
    for (let i = lastStep + 1; i <= step; i++) if (props.days[path[i].index]?.tokens > 0) consumed.add(path[i].index)
    eaten.value = consumed
    lastStep = step
    const point = path[step]
    const viewport = scroll.value
    if (viewport) {
      const svgWidth = viewport.querySelector('svg')?.getBoundingClientRect().width || viewport.clientWidth
      const x = (point.x / (columns.value * unit + 4)) * svgWidth
      if (x > viewport.scrollLeft + viewport.clientWidth - 30 || x < viewport.scrollLeft + 30)
        viewport.scrollLeft = x - viewport.clientWidth / 2
    }
  }
  const from = path[step],
    to = path[Math.min(step + 1, path.length - 1)]
  const fraction = Math.min(1, travel - step)
  const current = { x: from.x + (to.x - from.x) * fraction, y: from.y + (to.y - from.y) * fraction }
  const length = Math.min(10, 3 + eaten.value.size)
  body.value = [...path.slice(Math.max(0, step - length + 1), step + 1), current]
  if (from !== to) heading.value = (Math.atan2(to.y - from.y, to.x - from.x) * 180) / Math.PI
  if (travel > path.length + 15) {
    started = now
    lastStep = -1
    eaten.value = new Set()
  }
  frame = window.requestAnimationFrame(tick)
}
function toggle() {
  pausedByUser = running.value
  if (running.value) stop()
  else start()
}
function select(date: string) {
  if (props.busy) return
  pausedByUser = true
  stop()
  emit('select', date)
}
function visibility() {
  if (document.hidden) stop()
  else start()
}
function motionChanged(event: MediaQueryListEvent) {
  reduced.value = event.matches
  if (event.matches) stop()
  else start()
}
function moveFocus(event: KeyboardEvent) {
  const distance = ({ ArrowLeft: -7, ArrowRight: 7, ArrowUp: -1, ArrowDown: 1 } as Record<string, number>)[event.key]
  const target = event.target as Element
  if (distance === undefined) return
  const index = props.days.findIndex(day => day.date === target.getAttribute('data-date'))
  const day = props.days[index + distance]
  if (!day) return
  event.preventDefault()
  const cell = scroll.value?.querySelector<Element & Pick<HTMLElement, 'focus'>>(`[data-date="${day.date}"]`)
  cell?.focus()
}
watch(
  () => [year.value, props.selected],
  () => {
    focusDate.value = props.days.some(day => day.date === props.selected) ? props.selected : props.days[0]?.date || ''
  },
  { immediate: true }
)
watch(year, () => {
  pausedByUser = false
  start()
})
watch(activeDays, start)
onMounted(() => {
  start()
  document.addEventListener('visibilitychange', visibility)
  motion.addEventListener('change', motionChanged)
})
onUnmounted(() => {
  stop()
  document.removeEventListener('visibilitychange', visibility)
  motion.removeEventListener('change', motionChanged)
})
</script>

<style scoped>
.calendar-picture {
  display: block;
  width: 100%;
  min-width: 700px;
  overflow: visible;
}
.month-label {
  fill: var(--muted-foreground);
  font-size: 8px;
}
.day-cell,
.heat-sample {
  --heat: 0%;
  fill: color-mix(in srgb, var(--success) var(--heat), var(--muted));
  background: color-mix(in srgb, var(--success) var(--heat), var(--muted));
  transition: opacity 160ms;
}
.day-cell {
  cursor: pointer;
  stroke: var(--border);
  stroke-width: 0.6;
}
[data-level='1'] {
  --heat: 28%;
}
[data-level='2'] {
  --heat: 48%;
}
[data-level='3'] {
  --heat: 72%;
}
[data-level='4'] {
  --heat: 100%;
}
[data-coverage='unrecorded'] {
  fill: transparent;
}
[data-eaten='true'] {
  opacity: 0.14;
}
[data-selected='true'],
g:focus .day-cell {
  stroke: var(--primary);
  stroke-width: 1.8;
}
g:focus {
  outline: none;
}
.partial-line {
  stroke: var(--foreground);
  stroke-width: 0.7;
  opacity: 0.35;
  pointer-events: none;
}
.heat-sample {
  width: 11px;
  height: 11px;
  border-radius: 3px;
}
.snake {
  pointer-events: none;
  filter: drop-shadow(0 1px 2px #0003);
}
@media (prefers-reduced-motion: reduce) {
  .day-cell {
    transition: none;
  }
}
</style>
