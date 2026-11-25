<template>
  <div class="journal mx-auto max-w-6xl space-y-5 px-4 py-6 sm:px-6 sm:py-8">
    <header class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 class="text-3xl font-semibold tracking-tight">用量</h1>
        <p class="mt-2 text-sm text-muted-foreground">每一点灵感，都留下生长的痕迹。</p>
      </div>
      <UiButton variant="outlined" size="small" :loading="loading" @click="refresh()">刷新</UiButton>
    </header>
    <UiAlert v-if="error" type="error">{{ error }}</UiAlert>
    <UiAlert v-if="status?.syncIssue" type="warning">部分用量尚未成功记录，请查看服务状态。</UiAlert>
    <section v-if="status && !status.enabledAt" class="journal-start panel-surface p-8 sm:p-12">
      <h2 class="text-2xl font-semibold">从今天，留下一点绿意</h2>
      <p class="my-4 max-w-lg text-sm leading-7 text-muted-foreground">
        收藏每天的用量，让灵感、专注与休息在海上长成一片森林。
      </p>
      <UiButton :loading="loading" @click="enable">开启旅程</UiButton>
    </section>
    <p v-if="!status && loading" role="status" class="text-sm text-muted-foreground">正在打开用量…</p>
    <template v-if="status?.enabledAt">
      <nav class="flex gap-1 border-b border-border" role="tablist" aria-label="用量" @keydown="moveTab">
        <button
          v-for="item in tabs"
          :id="`usage-tab-${item.id}`"
          :key="item.id"
          class="usage-tab"
          role="tab"
          :aria-selected="tab === item.id"
          :aria-controls="`usage-panel-${item.id}`"
          :tabindex="tab === item.id ? 0 : -1"
          @click="go(item.id)"
        >
          {{ item.label }}
        </button>
      </nav>
      <div
        :id="`usage-panel-${tab}`"
        role="tabpanel"
        :aria-labelledby="`usage-tab-${tab}`"
        tabindex="0"
        class="space-y-5 focus-visible:outline-primary"
      >
        <template v-if="tab === 'overview'">
          <UsageCalendar
            v-if="days.length"
            :days="days"
            :selected="detail?.date || status.today"
            :first-year="firstYear"
            :current-year="currentYear"
            :busy="loading"
            @select="openTicket"
            @year="changeYear"
            @today="today"
          />
          <div class="grid gap-4 sm:grid-cols-3">
            <section class="panel-surface p-5">
              <p class="text-xs text-muted-foreground">{{ year === currentYear ? '今日用量' : `${year} 年用量` }}</p>
              <p class="mt-3 text-3xl font-semibold tabular-nums">
                {{ compact.format(overviewTokens) }}
                <span class="text-xs font-normal text-muted-foreground">Token</span>
              </p>
              <UiButton
                class="mt-3"
                size="small"
                variant="text"
                :disabled="loading"
                @click="openTicket(year === currentYear ? status.today : detail?.date || status.today)"
              >
                查看每日票据
              </UiButton>
            </section>
            <section class="panel-surface p-5">
              <p class="text-xs text-muted-foreground">{{ detail?.date }} · 缓存命中</p>
              <p class="mt-3 text-3xl font-semibold tabular-nums">
                {{ detail ? tokenShare(detail.upstream.cacheRead, inputTotal(detail.upstream)) : '—' }}
              </p>
              <p class="mt-4 text-xs leading-5 text-muted-foreground">熟悉的上下文，再次派上用场。</p>
            </section>
            <button class="panel-surface p-5 text-left" @click="go('forest')">
              <p class="text-xs text-muted-foreground">海上的绿意</p>
              <p class="mt-3 text-3xl font-semibold tabular-nums">
                {{ number.format(totalTrees) }}
                <span class="text-sm font-normal text-muted-foreground">棵树</span>
              </p>
              <p class="mt-4 text-xs text-primary">{{ world?.islands || 0 }} 座岛屿 · 去森林走走 →</p>
            </button>
          </div>
        </template>
        <ForestPanel
          v-else-if="tab === 'forest'"
          ref="forestPanel"
          :time-zone="status.timeZone"
          :target-tree="targetTree"
          @collection="go('achievements')"
        />
        <ForestCollection
          v-else
          :key="tab"
          :shop="tab === 'shop'"
          :time-zone="status.timeZone"
          @locate="locate"
          @forest="go('forest')"
        />
        <UiAlert v-if="forestError" type="error">{{ forestError }}</UiAlert>
        <UiAlert v-if="syncError || newsError" type="warning">{{ syncError || newsError }}</UiAlert>
      </div>
    </template>

    <AppDialog v-model="ticketOpen" title="每日用量票据" size="md">
      <UiAlert v-if="ticketError" type="error">{{ ticketError }}</UiAlert>
      <UiAlert v-if="growthError" type="warning">{{ growthError }}</UiAlert>
      <p v-if="ticketLoading" role="status">正在整理这一天…</p>
      <article v-else-if="detail && ticketReady" class="ticket space-y-5">
        <header class="flex items-center justify-between border-b border-dashed border-border pb-4">
          <span class="font-mono">{{ detail.date }}</span>
          <span class="text-xs text-muted-foreground">{{ coverageText(detail) }}</span>
        </header>
        <div class="py-3">
          <p class="text-xs tracking-widest text-muted-foreground">TODAY'S FOOTPRINT</p>
          <p class="ticket-total mt-3 font-semibold tabular-nums">{{ number.format(detail.tokens) }}</p>
          <p class="mt-2 text-sm text-muted-foreground">Token</p>
        </div>
        <dl class="space-y-2 border-t border-dashed border-border pt-5">
          <div v-for="[label, value] in rows" :key="label" class="flex justify-between gap-4 text-sm">
            <dt class="text-muted-foreground">{{ label }}</dt>
            <dd class="text-right tabular-nums">{{ value }}</dd>
          </div>
        </dl>
        <div v-if="detail.models.length" class="space-y-3 border-t border-dashed border-border pt-5">
          <div v-for="model in detail.models.slice(0, 5)" :key="model.name">
            <div class="flex justify-between gap-3 text-xs">
              <span class="truncate">{{ model.name || '未报告模型' }}</span>
              <span>{{ tokenShare(model.tokens, detail.tokens) }}</span>
            </div>
            <div class="mt-2 h-1 overflow-hidden rounded-full bg-muted">
              <div
                class="h-full bg-primary"
                :style="{ width: `${detail.tokens ? (model.tokens / detail.tokens) * 100 : 0}%` }"
              ></div>
            </div>
          </div>
        </div>
        <p v-if="growth" class="border-t border-dashed border-border pt-5 text-center text-sm text-primary">
          这一天，海上多了 {{ number.format(dayTrees) }} 棵树。
        </p>
        <p class="text-center text-xs text-muted-foreground">{{ status?.timeZone }}</p>
      </article>
      <template #actions>
        <UiButton
          v-if="growth?.trees.length"
          variant="text"
          @click="locate(growth.trees[0].id); ticketOpen = false"
        >
          去看看
        </UiButton>
        <UiButton :loading="exporting" :disabled="ticketLoading || !ticketReady" @click="download">保存票据</UiButton>
      </template>
    </AppDialog>
    <AppDialog :model-value="eventOpen" :title="eventDetail?.title || '林间来信'" @update:model-value="closeEvent">
      <p v-if="eventLoading" role="status">正在打开这封来信…</p>
      <UiAlert v-else-if="eventError" type="error">{{ eventError }}</UiAlert>
      <template v-else-if="eventDetail">
        <div v-if="eventTree" class="mx-auto h-56 max-w-xs">
          <ForestArt
            :kind="eventTree.kind"
            :tree-style="eventTree.style"
            :accessory="eventTree.accessory"
            :variant="eventTree.variant"
            :condition="eventTree.condition"
            :label="treeName(eventTree)"
          />
        </div>
        <p class="text-center text-lg leading-8">{{ eventDetail.story }}</p>
        <div v-if="eventChanges.length" class="my-5 flex flex-wrap justify-center gap-2">
          <span
            v-for="item in eventChanges"
            :key="item.label"
            class="rounded-control px-3 py-2 text-sm font-medium"
            :class="item.loss ? 'bg-destructive/10 text-destructive' : 'bg-success/10 text-success'"
          >
            {{ item.text }}
          </span>
        </div>
        <p v-if="eventDetail.effects.awayDays" class="mt-3 text-center text-sm text-muted-foreground">
          海湾已经安静了 {{ eventDetail.effects.awayDays }} 天。
        </p>
        <div v-if="eventDetail.affected?.length" class="mt-5 rounded-control border border-border p-3">
          <p class="mb-2 text-sm font-medium">这封来信里的树木</p>
          <div class="flex max-h-40 flex-wrap gap-2 overflow-y-auto">
            <UiButton
              v-for="item in eventDetail.affected"
              :key="item.treeId"
              size="small"
              variant="outlined"
              @click="locate(item.treeId)"
            >
              第 {{ item.treeId }} 棵 · {{ conditionNames[item.after] }}
            </UiButton>
          </div>
        </div>
        <div v-if="eventTree" class="mt-5 text-center">
          <p class="font-medium">{{ treeName(eventTree) }}</p>
          <p class="mt-1 text-sm text-muted-foreground">{{ origins[eventTree.kind] }}</p>
        </div>
        <p class="mt-4 text-center text-xs text-muted-foreground">{{ eventDate }}</p>
      </template>
      <template #actions>
        <UiButton v-if="eventTree" @click="locate(eventTree.id)">去看看这棵树</UiButton>
        <UiButton variant="text" @click="closeEvent(false)">收好</UiButton>
      </template>
    </AppDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { useRoute, useRouter } from 'vue-router'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiButton from '@/shared/ui/button/Button.vue'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import { useAutoRefresh } from '@/shared/composables/useAutoRefresh'
import type { ForestDay, ForestEvent, ForestTree } from '@/types/forest'
import { useUsageJournal } from '@/features/usage-journal/store'
import {
  coverageText,
  exportTicket,
  inputTotal,
  number,
  reportRows,
  tokenShare
} from '@/features/usage-journal/presentation'
import UsageCalendar from '@/features/usage-journal/UsageCalendar.vue'
import ForestPanel from '@/features/usage-journal/forest/ForestPanel.vue'
import ForestCollection from '@/features/usage-journal/forest/ForestCollection.vue'
import ForestArt from '@/features/usage-journal/forest/ForestArt.vue'
import {
  conditionNames,
  effectItems,
  forestApi,
  origins,
  treeName,
  useForest
} from '@/features/usage-journal/forest/store'

const tabs = [
  { id: 'overview', label: '总览' },
  { id: 'forest', label: '森林' },
  { id: 'achievements', label: '成就' },
  { id: 'shop', label: '商城' }
] as const
type Tab = (typeof tabs)[number]['id']
const router = useRouter(),
  route = useRoute()
const journal = useUsageJournal(),
  forest = useForest()
const { status, days, detail } = storeToRefs(journal)
const { world, syncError, newsError } = storeToRefs(forest)
const tab = computed<Tab>(() => tabs.find(item => item.id === route.query.tab)?.id || 'overview')
const loading = ref(false),
  error = ref(''),
  forestError = ref('')
const ticketOpen = ref(false),
  ticketLoading = ref(false),
  ticketError = ref(''),
  exporting = ref(false)
const ticketReady = ref(false),
  growthError = ref('')
const growth = ref<ForestDay>()
const forestPanel = ref<InstanceType<typeof ForestPanel>>()
const targetTree = ref(0)
const eventDetail = ref<ForestEvent>(),
  eventTree = ref<ForestTree>()
const eventLoading = ref(false),
  eventError = ref('')
const eventOpen = computed(() => typeof route.query.event === 'string')
const eventChanges = computed(() => (eventDetail.value ? effectItems(eventDetail.value.effects) : []))
const eventDate = computed(
  () =>
    eventDetail.value && new Date(eventDetail.value.at).toLocaleString('zh-CN', { timeZone: status.value?.timeZone })
)
const year = computed(() => Number(days.value[0]?.date.slice(0, 4)))
const currentYear = computed(() => Number(status.value?.today.slice(0, 4)))
const firstYear = computed(() => Number(status.value?.enabledAt?.slice(0, 4)))
const compact = new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 })
const overviewTokens = computed(() =>
  year.value === currentYear.value
    ? days.value.find(day => day.date === status.value?.today)?.tokens || 0
    : days.value.reduce((sum, day) => sum + day.tokens, 0)
)
const totalTrees = computed(() => Object.values(world.value?.counts || {}).reduce((sum, count) => sum + count, 0))
const dayTrees = computed(() => Object.values(growth.value?.counts || {}).reduce((sum, count) => sum + count, 0))
const rows = computed(() => (detail.value && status.value ? reportRows(detail.value, status.value.timeZone) : []))
const message = (cause: unknown) => (cause instanceof Error ? cause.message : String(cause))

async function go(value: Tab) {
  try {
    await router.push({ path: '/usage-journal', query: value === 'overview' ? {} : { tab: value } })
  } catch (cause) {
    error.value = message(cause)
  }
}
async function moveTab(event: KeyboardEvent) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  const index = tabs.findIndex(item => item.id === tab.value)
  const next =
    event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? tabs.length - 1
        : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length
  await go(tabs[next].id)
  document.getElementById(`usage-tab-${tabs[next].id}`)?.focus()
}
async function loadForest() {
  forestError.value = ''
  try {
    await forest.load()
  } catch (cause) {
    forestError.value = `森林读取失败：${message(cause)}`
  }
}
async function refresh(targetYear = year.value || undefined, date = detail.value?.date) {
  if (loading.value || ticketLoading.value) return
  loading.value = true
  error.value = ''
  try {
    await journal.load(targetYear, date)
  } catch (cause) {
    error.value = message(cause)
  }
  if (status.value?.enabledAt) {
    forestError.value = ''
    if (forestPanel.value) await forestPanel.value.refresh()
    else await loadForest()
  }
  loading.value = false
}
function changeYear(value: number) {
  return refresh(value, '')
}
function today() {
  return refresh(currentYear.value, status.value?.today)
}
async function enable() {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    await journal.enable(Intl.DateTimeFormat().resolvedOptions().timeZone)
    await loadForest()
  } catch (cause) {
    error.value = message(cause)
  } finally {
    loading.value = false
  }
}
async function openTicket(date: string) {
  if (loading.value || ticketLoading.value) return
  ticketOpen.value = true
  ticketLoading.value = true
  ticketReady.value = false
  ticketError.value = ''
  growthError.value = ''
  growth.value = undefined
  try {
    await journal.select(date)
    ticketReady.value = true
  } catch (cause) {
    ticketError.value = message(cause)
    ticketLoading.value = false
    return
  }
  try {
    growth.value = await forestApi.day(date)
  } catch (cause) {
    growthError.value = `森林记录读取失败：${message(cause)}`
  } finally {
    ticketLoading.value = false
  }
}
async function download() {
  if (!detail.value || !status.value || exporting.value) return
  exporting.value = true
  ticketError.value = ''
  try {
    await exportTicket(detail.value, status.value.timeZone, growth.value)
  } catch (cause) {
    ticketError.value = message(cause)
  } finally {
    exporting.value = false
  }
}
async function locate(id: number) {
  targetTree.value = 0
  await go('forest')
  await nextTick()
  targetTree.value = id
}
async function closeEvent(value: boolean) {
  if (value) return
  try {
    await router.replace({ query: { ...route.query, event: undefined } })
  } catch (cause) {
    eventError.value = message(cause)
  }
}
let eventRevision = 0
watch(
  () => route.query.event,
  async id => {
    const revision = ++eventRevision
    eventDetail.value = undefined
    eventTree.value = undefined
    eventError.value = ''
    if (typeof id !== 'string') {
      eventLoading.value = false
      return
    }
    eventLoading.value = true
    try {
      const item = await forestApi.event(id)
      const tree = item.treeId ? await forestApi.tree(item.treeId) : undefined
      if (revision !== eventRevision) return
      eventDetail.value = item
      eventTree.value = tree
    } catch (cause) {
      if (revision === eventRevision) eventError.value = message(cause)
    } finally {
      if (revision === eventRevision) eventLoading.value = false
    }
  },
  { immediate: true }
)
const autoRefresh = useAutoRefresh(
  () => {
    if (status.value?.enabledAt && !ticketOpen.value) return refresh()
  },
  loading,
  30000
)
onMounted(() => {
  void refresh()
  autoRefresh.start()
})
onUnmounted(() => {
  eventRevision++
})
</script>

<style scoped>
.journal-start {
  background:
    radial-gradient(ellipse at right top, color-mix(in srgb, var(--success) 16%, transparent), transparent 65%),
    var(--surface-bright);
}
.usage-tab {
  padding: 0.8rem 1.1rem;
  border-bottom: 2px solid transparent;
  color: var(--muted-foreground);
  cursor: pointer;
  font-size: 0.9rem;
}
.usage-tab[aria-selected='true'] {
  border-color: var(--primary);
  color: var(--primary);
  font-weight: 600;
}
.ticket-total {
  font-size: clamp(2rem, 7vw, 3.5rem);
  line-height: 1.1;
  letter-spacing: -0.045em;
  overflow-wrap: anywhere;
}
</style>
