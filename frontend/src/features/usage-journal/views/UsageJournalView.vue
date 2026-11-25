<template>
  <div class="journal mx-auto max-w-6xl space-y-5 px-4 py-6 sm:px-6 sm:py-8">
    <header class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 class="text-3xl font-semibold tracking-tight sm:text-4xl">用量手账</h1>
        <p v-if="status?.enabledAt" class="mt-1 text-sm text-muted-foreground">统计时区 {{ status.timeZone }}</p>
      </div>
      <UiButton v-if="status?.enabledAt || error" variant="outlined" size="small" :loading="loading" @click="refreshAll">刷新</UiButton>
    </header>

    <UiAlert v-if="error" type="error">{{ error }}</UiAlert>
    <UiAlert v-if="status?.syncIssue" type="warning">账本写入暂时异常，部分日期可能存在用量缺口。</UiAlert>
    <p v-if="!status && loading" role="status" class="text-sm text-muted-foreground">正在读取用量账本…</p>

    <section v-if="status && !status.enabledAt" class="journal-start panel-surface p-7 sm:p-10">
      <h2 class="text-xl font-semibold">从今往后记录每天的用量</h2>
      <p class="my-3 max-w-lg text-sm leading-6 text-muted-foreground">启用后，每次上游尝试的用量会存入独立账本，清理请求日志也能保留手账。已有日志不会导入。</p>
      <p class="mb-4 text-sm">统计时区 {{ localTimeZone }}</p>
      <UiButton color="primary" :loading="loading" @click="enable">启用用量手账</UiButton>
    </section>

    <template v-if="status?.enabledAt && year">
      <section class="panel-surface min-w-0 p-4 sm:p-6" aria-labelledby="usage-calendar-heading">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-2">
          <h2 id="usage-calendar-heading" class="text-lg font-semibold">{{ year }} 年用量</h2>
          <div class="flex items-center gap-1">
            <UiButton variant="text" size="small" :disabled="loading || year <= firstYear" @click="changeYear(-1)">上一年</UiButton>
            <UiButton variant="text" size="small" :disabled="loading || year >= currentYear" @click="changeYear(1)">下一年</UiButton>
            <UiButton variant="outlined" size="small" :disabled="loading || detail?.date === status.today" @click="jumpToToday">今天</UiButton>
          </div>
        </div>
        <div ref="calendar" class="calendar-scroll overflow-x-auto pb-2" @keydown="moveFocus">
          <div class="calendar-grid" :style="{ '--columns': columns }">
            <span v-for="item in months" :key="item.month" class="calendar-month text-muted-foreground" :style="{ gridColumnStart: item.column }">{{ item.month }}月</span>
            <button
              v-for="(day, index) in days"
              :key="day.date"
              type="button"
              class="calendar-day"
              :style="{ gridColumnStart: columnAt(index), gridRowStart: (offset + index) % 7 + 2 }"
              :data-date="day.date"
              :data-level="heat(day)"
              :data-coverage="day.coverage"
              :data-replay="replayIndex === index ? 'head' : replayIndex > index && replayIndex - index <= 5 ? 'trail' : undefined"
              :aria-pressed="day.date === detail?.date"
              :aria-label="dayLabel(day)"
              :title="dayLabel(day)"
              :tabindex="day.date === (focusedDate || detail?.date) ? 0 : -1"
              @focus="focusedDate = day.date"
              @click="select(day.date)"
            ></button>
          </div>
        </div>
        <div class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs text-muted-foreground">
          <span class="flex items-center gap-1.5">少 <i v-for="level in 5" :key="level" class="legend-mark" :data-level="level - 1" aria-hidden="true"></i> 多</span>
          <span class="flex items-center gap-1.5"><i class="legend-mark" data-coverage="unrecorded" aria-hidden="true"></i> 未记录</span>
          <span class="flex items-center gap-1.5"><i class="legend-mark" data-coverage="partial" aria-hidden="true"></i> 记录不完整</span>
          <UiButton v-if="replayAllowed" class="sm:ml-auto" variant="text" size="x-small" :disabled="!days.some(day => day.tokens > 0)" @click="toggleReplay">{{ replayIndex >= 0 ? '结束回顾' : '按时间回顾' }}</UiButton>
        </div>
      </section>

      <article v-if="detail" class="ticket panel-surface mx-auto w-full max-w-2xl p-5 sm:p-8" aria-labelledby="usage-ticket-heading">
        <header class="ticket-head mb-5 flex flex-wrap items-center justify-between gap-2 pb-4">
          <div>
            <h2 id="usage-ticket-heading" class="text-lg font-semibold">每日用量票据</h2>
            <p class="mt-1 text-sm text-muted-foreground">{{ detail.date }} · {{ coverageText(detail) }}</p>
          </div>
          <UiButton v-if="detail.coverage !== 'unrecorded'" variant="outlined" size="small" :loading="exporting" @click="download">导出 PNG</UiButton>
        </header>
        <p v-if="detail.coverage === 'unrecorded'" class="text-sm text-muted-foreground">这一天没有已记录的用量。</p>
        <template v-else>
          <p class="ticket-total break-all font-semibold tabular-nums">{{ number.format(detail.tokens) }}</p>
          <p class="mb-5 text-sm text-muted-foreground">上游报告 Token</p>
          <dl class="grid grid-cols-1 gap-x-5 gap-y-3 sm:grid-cols-2">
            <div v-for="[label, value] in rows" :key="label" class="flex items-baseline justify-between gap-2">
              <dt class="text-xs text-muted-foreground">{{ label }}</dt>
              <dd class="break-all text-right text-sm font-medium tabular-nums">{{ value }}</dd>
            </div>
          </dl>
          <section v-if="detail.models.length" class="ticket-head mt-6 pb-3">
            <h3 class="mb-2 text-sm font-semibold">模型用量</h3>
            <ul class="space-y-2 text-sm">
              <li v-for="model in detail.models" :key="model.name" class="flex justify-between gap-3">
                <span class="min-w-0 break-all">{{ model.name || '未报告模型' }}</span>
                <span class="shrink-0 text-right tabular-nums">{{ number.format(model.tokens) }} · {{ tokenShare(model.tokens, detail.tokens) }}</span>
              </li>
            </ul>
          </section>
          <p class="mt-4 text-xs text-muted-foreground">{{ ticketVersion(detail) }}</p>
          <p v-if="detail.updatedAt" class="mt-1 text-xs text-muted-foreground">最近入账 {{ lastUpdated }}</p>
        </template>
        <section v-if="detail.coverage !== 'unrecorded' || treeCount || growthDay?.events.length || growthError" class="mt-5 border-t border-dashed border-border pt-4" aria-labelledby="ticket-forest">
          <h3 id="ticket-forest" class="text-sm font-semibold">当日森林足迹</h3>
          <p v-if="growthError" role="status" class="mt-1 text-xs text-destructive">{{ growthError }}</p>
          <template v-if="growthDay">
            <dl class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs">
              <div v-for="kind in kinds" :key="kind" class="flex items-baseline gap-1.5">
                <dt class="text-muted-foreground">{{ labels[kind] }}</dt>
                <dd class="font-semibold tabular-nums">+{{ number.format(growthDay.counts[kind]) }}</dd>
              </div>
            </dl>
            <details v-if="growthDay.trees.length" class="mt-3 text-sm">
              <summary class="cursor-pointer">查看 {{ number.format(treeCount) }} 棵新树</summary>
              <ul class="mt-1 max-h-48 overflow-y-auto">
                <li v-for="tree in growthDay.trees" :key="tree.id">
                  <UiButton size="small" variant="text" @click="forestPanel?.locate(tree)">{{ treeName(tree) }} · 岛屿 {{ tree.island }} · {{ tree.slot + 1 }} 号地</UiButton>
                </li>
              </ul>
              <UiButton v-if="growthDay.trees.length < treeCount" class="mt-1" size="small" variant="outlined" :loading="moreLoading" @click="loadMore">继续加载新树</UiButton>
            </details>
            <ul v-if="growthDay.events.length" class="mt-3 space-y-2">
              <li v-for="event in growthDay.events" :key="event.id">
                <p class="text-sm font-medium">{{ event.title }}</p>
                <p class="text-xs leading-5 text-muted-foreground">{{ event.story }}</p>
              </li>
            </ul>
          </template>
          <p v-else-if="!growthError" class="mt-1 text-xs text-muted-foreground" role="status">正在读取种植与巡护记录…</p>
        </section>
      </article>
    </template>
    <ForestPanel v-if="status?.enabledAt" ref="forestPanel" :time-zone="status.timeZone" @growth-changed="refreshGrowth" />
  </div>
</template>

<script setup lang="ts">
import { storeToRefs } from 'pinia'
import { computed, nextTick, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'
import { useAutoRefresh } from '@/shared/composables/useAutoRefresh'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiButton from '@/shared/ui/button/Button.vue'
import type { JournalDay } from '@/types/usage-journal'
import type { ForestDay } from '@/types/forest'
import { coverageText, exportTicket, number, reportRows, ticketVersion, tokenShare } from '@/features/usage-journal/presentation'
import { useUsageJournal } from '@/features/usage-journal/store'
import ForestPanel from '@/features/usage-journal/forest/ForestPanel.vue'
import { forestApi, kinds, labels, treeName } from '@/features/usage-journal/forest/store'

const journal = useUsageJournal()
const { status, days, detail } = storeToRefs(journal)
const loading = ref(false)
const exporting = ref(false)
const error = ref('')
const focusedDate = ref('')
const calendar = ref<HTMLElement>()
const replayIndex = ref(-1)
const motion = window.matchMedia('(prefers-reduced-motion: reduce)')
const replayAllowed = ref(!motion.matches)
let replayTimer = 0
let savedScroll = 0
const forestPanel = ref<InstanceType<typeof ForestPanel>>()
const growthDay = shallowRef<ForestDay>()
const growthError = ref('')
const moreLoading = ref(false)
const treeCount = computed(() => growthDay.value ? Object.values(growthDay.value.counts).reduce((total, count) => total + count, 0) : 0)
let growthRevision = 0
let growthDateRevision = 0
const localTimeZone = Intl.DateTimeFormat().resolvedOptions().timeZone
const year = computed(() => Number(days.value[0]?.date.slice(0, 4)))
const currentYear = computed(() => Number(status.value?.today.slice(0, 4)))
const firstYear = computed(() => Number(status.value?.enabledAt?.slice(0, 4)))
const offset = computed(() => year.value ? (new Date(Date.UTC(year.value, 0, 1)).getUTCDay() + 6) % 7 : 0)
const columnAt = (index: number) => Math.floor((offset.value + index) / 7) + 1
const columns = computed(() => columnAt(days.value.length - 1))
const months = computed(() => days.value.flatMap((day, index) => day.date.endsWith('-01') ? [{ month: Number(day.date.slice(5, 7)), column: columnAt(index) }] : []))
const maxTokens = computed(() => Math.max(1, ...days.value.map(day => day.tokens)))
const rows = computed(() => detail.value && status.value ? reportRows(detail.value, status.value.timeZone) : [])
const lastUpdated = computed(() => detail.value?.updatedAt && new Date(detail.value.updatedAt).toLocaleString('zh-CN', { timeZone: status.value?.timeZone, hour12: false }))

function dayLabel(day: JournalDay) {
  return `${day.date} · ${coverageText(day)}${day.coverage === 'unrecorded' ? '' : ` · ${number.format(day.tokens)} Token`}`
}

function heat(day: JournalDay) {
  if (!day.tokens) return 0
  return Math.max(1, Math.ceil(Math.log1p(day.tokens) / Math.log1p(maxTokens.value) * 4))
}

async function request(action: () => Promise<void>) {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    await action()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    loading.value = false
  }
}

async function showDate(date: string, focus = false) {
  focusedDate.value = date
  await nextTick()
  const cell = calendar.value?.querySelector<HTMLElement>(`[data-date="${date}"]`)
  if (!cell || !calendar.value) return
  const parent = calendar.value
  if (focus) cell.focus({ preventScroll: true })
  const box = cell.getBoundingClientRect()
  const frame = parent.getBoundingClientRect()
  if (box.left < frame.left || box.right > frame.right) parent.scrollLeft += box.left - frame.left - parent.clientWidth / 2
}

async function refresh(targetYear = year.value || undefined, date = detail.value?.date) {
  await request(async () => {
    await journal.load(targetYear, date)
    if (detail.value) await showDate(detail.value.date)
  })
}

function refreshAll() {
  void refresh()
  void forestPanel.value?.refresh()
}

async function refreshGrowth() {
  const date = detail.value?.date
  if (!date) return
  const revision = ++growthRevision
  growthError.value = ''
  try {
    const result = await forestApi.day(date)
    if (revision !== growthRevision) return
    const last = result.trees[result.trees.length - 1]
    const tail = last ? growthDay.value?.trees.filter(tree => tree.id > last.id) ?? [] : []
    growthDay.value = { ...result, trees: [...result.trees, ...tail] }
  } catch (cause) {
    if (revision === growthRevision) growthError.value = `森林记录读取失败。 ${cause instanceof Error ? cause.message : String(cause)}`
  }
}

async function loadMore() {
  const date = detail.value?.date
  const trees = growthDay.value?.trees ?? []
  const after = trees[trees.length - 1]?.id
  if (!date || after === undefined || moreLoading.value) return
  const dateRevision = growthDateRevision
  const refreshRevision = growthRevision
  moreLoading.value = true
  growthError.value = ''
  try {
    const result = await forestApi.day(date, after)
    if (dateRevision !== growthDateRevision || !growthDay.value) return
    const rows = growthDay.value.trees
    const last = rows[rows.length - 1]?.id ?? 0
    const current = refreshRevision === growthRevision ? result : growthDay.value
    growthDay.value = { ...current, trees: [...rows, ...result.trees.filter(tree => tree.id > last)] }
  } catch (cause) {
    if (dateRevision === growthDateRevision) growthError.value = `更多树木读取失败。 ${cause instanceof Error ? cause.message : String(cause)}`
  } finally {
    moreLoading.value = false
  }
}

watch(detail, (day, previous) => {
  if (day?.date !== previous?.date) {
    growthRevision++
    growthDateRevision++
    growthDay.value = undefined
    growthError.value = ''
  }
  if (day) void refreshGrowth()
})

function changeYear(delta: number) { void refresh(year.value + delta, '') }
function jumpToToday() { void refresh(currentYear.value, status.value?.today) }

function enable() {
  return request(async () => {
    await journal.enable(localTimeZone)
    if (detail.value) await showDate(detail.value.date)
  })
}

function select(date: string) {
  stopReplay(false)
  return request(() => journal.select(date))
}

function moveFocus(event: KeyboardEvent) {
  const distances: Record<string, number> = { ArrowLeft: -7, ArrowRight: 7, ArrowUp: -1, ArrowDown: 1 }
  const distance = distances[event.key]
  if (distance === undefined || !(event.target instanceof HTMLElement) || !event.target.dataset.date) return
  event.preventDefault()
  const current = days.value.findIndex(day => day.date === focusedDate.value)
  const next = days.value[Math.min(days.value.length - 1, Math.max(0, current + distance))]
  if (next) void showDate(next.date, true)
}

function stopReplay(restoreScroll = true) {
  if (replayIndex.value >= 0 && restoreScroll && calendar.value) calendar.value.scrollLeft = savedScroll
  window.clearInterval(replayTimer)
  replayIndex.value = -1
}

function toggleReplay() {
  if (replayIndex.value >= 0) { stopReplay(); return }
  if (motion.matches) return
  const start = days.value.findIndex(day => day.tokens > 0)
  const end = days.value.reduce((last, day, index) => day.tokens > 0 ? index : last, -1)
  if (start < 0) return
  savedScroll = calendar.value?.scrollLeft ?? 0
  replayIndex.value = start
  replayTimer = window.setInterval(() => {
    if (replayIndex.value > end + 5) { stopReplay(); return }
    const day = days.value[replayIndex.value]
    const cell = day && calendar.value?.querySelector<HTMLElement>(`[data-date="${day.date}"]`)
    if (cell && calendar.value) {
      const frame = calendar.value.getBoundingClientRect()
      const box = cell.getBoundingClientRect()
      if (box.right > frame.right - 20 || box.left < frame.left + 20) calendar.value.scrollLeft += box.left - frame.left - frame.width / 2
    }
    replayIndex.value++
  }, 42)
}

function motionChanged(event: MediaQueryListEvent) {
  replayAllowed.value = !event.matches
  if (event.matches) stopReplay()
}
watch(year, () => stopReplay(false))

async function download() {
  if (!detail.value || !status.value) return
  exporting.value = true
  error.value = ''
  try {
    await exportTicket(detail.value, status.value.timeZone, growthDay.value)
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    exporting.value = false
  }
}

const autoRefresh = useAutoRefresh(async () => {
  if (!status.value?.enabledAt) return
  await Promise.all([
    forestPanel.value?.refresh(),
    year.value === currentYear.value ? request(() => journal.load(year.value, detail.value?.date)) : undefined
  ])
}, loading, 30_000)

onMounted(() => {
  void refresh()
  autoRefresh.start()
  motion.addEventListener('change', motionChanged)
})
onUnmounted(() => {
  stopReplay(false)
  motion.removeEventListener('change', motionChanged)
})
</script>

<style scoped>
.journal { --journal-green: var(--success); }
.journal-start { background: radial-gradient(ellipse at right top, color-mix(in srgb, var(--journal-green) 15%, transparent), transparent 65%), var(--surface-bright); }
.calendar-grid { display: grid; grid-template-columns: repeat(var(--columns), minmax(0.8rem, 1fr)); grid-template-rows: 1.3rem repeat(7, auto); gap: 0.25rem; min-width: calc(var(--columns) * 1.05rem); }
.calendar-month { grid-row: 1; white-space: nowrap; font-size: 0.72rem; }
.calendar-day, .legend-mark { --heat: 0%; border-radius: 0.2rem; background: color-mix(in srgb, var(--journal-green) var(--heat), var(--muted)); }
.calendar-day { aspect-ratio: 1; cursor: pointer; }
.calendar-day:focus-visible, .calendar-day[aria-pressed='true'] { outline: 2px solid var(--ring); outline-offset: 1px; z-index: 1; }
.calendar-day[data-replay='trail'] { box-shadow: inset 0 0 0 2px var(--success); }
.calendar-day[data-replay='head'] { background: var(--success); box-shadow: 0 0 0 2px var(--ring); z-index: 2; }
.legend-mark { display: inline-block; width: 0.75rem; height: 0.75rem; }
[data-level='1'] { --heat: 27%; }
[data-level='2'] { --heat: 47%; }
[data-level='3'] { --heat: 70%; }
[data-level='4'] { --heat: 100%; }
[data-coverage='partial'] { background-image: repeating-linear-gradient(135deg, transparent 0 3px, color-mix(in srgb, var(--foreground) 21%, transparent) 3px 4px); }
[data-coverage='unrecorded'] { background: transparent; box-shadow: inset 0 0 0 1px var(--border); }
.ticket { background: var(--surface-bright); }
.ticket-head { border-bottom: 1px dashed var(--border); }
.ticket-total { font-size: clamp(2.6rem, 7vw, 4.8rem); line-height: 1.04; letter-spacing: -0.045em; }
</style>
