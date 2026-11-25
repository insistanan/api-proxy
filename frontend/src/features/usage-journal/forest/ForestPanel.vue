<template>
  <section ref="panel" class="atlas panel-surface scroll-mt-24 space-y-4 p-4 sm:p-6" aria-labelledby="forest-heading">
    <header class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h2 id="forest-heading" class="atlas-title text-2xl font-semibold sm:text-3xl">个人森林</h2>
        <p v-if="world" class="mt-1 text-sm text-muted-foreground">{{ number.format(totalTrees) }} 棵树 · {{ world.islands }} 座岛 · {{ Math.ceil(world.islands / islandsPerGroup) }} 片群岛</p>
      </div>
      <div class="flex items-center gap-3">
        <p v-if="world" class="text-sm tabular-nums"><strong>Lv. {{ world.level }}</strong> · {{ number.format(world.points) }} 成就点</p>
        <UiButton variant="outlined" size="small" :disabled="autoBusy" @click="refresh()">刷新森林</UiButton>
      </div>
    </header>
    <UiAlert v-if="loadError" type="error">{{ loadError }}</UiAlert>
    <UiAlert v-if="operationError" type="error">{{ operationError }}</UiAlert>
    <UiAlert v-if="world?.syncIssue" type="warning">成长结算暂时中断，网关会从已入账的位置重试。</UiAlert>
    <p v-else-if="world?.catchingUp" class="text-sm text-muted-foreground" role="status">正在结算已入账的成长，本轮用量会分批种植。</p>
    <p v-if="!world && !loadError" role="status" class="text-sm text-muted-foreground">正在读取森林…</p>
    <template v-if="world">
      <nav aria-label="地图视角" class="flex flex-wrap items-center gap-1.5">
        <UiButton size="small" variant="outlined" :active="scene.view === 'world'" :aria-current="scene.view === 'world' ? 'location' : undefined" :disabled="busy" @click="open('world', 0)">海图</UiButton>
        <UiButton v-if="scene.view !== 'world'" size="small" variant="outlined" :active="scene.view === 'group'" :aria-current="scene.view === 'group' ? 'location' : undefined" :disabled="busy" @click="open('group', scene.view === 'island' ? groupOf(scene.id) : scene.id)">{{ groupName(scene.view === 'island' ? groupOf(scene.id) : scene.id) }}</UiButton>
        <span v-if="scene.view === 'island' && island" class="px-2 text-sm font-medium">{{ islandName(island.island) }}</span>
        <div class="flex flex-wrap gap-1 sm:ml-auto">
          <UiButton variant="text" size="small" :disabled="busy" @click="open('island', 1)">第一座岛</UiButton>
          <UiButton variant="text" size="small" :disabled="busy || !world.latest" @click="world.latest && locate(world.latest)">最新种植</UiButton>
          <label v-if="world.favorites.length" class="sr-only" for="forest-favorites">跳到收藏岛屿</label>
          <select v-if="world.favorites.length" id="forest-favorites" class="field-surface max-w-36 px-2 text-sm" :disabled="busy" @change="openFavorite">
            <option value="">收藏岛屿</option>
            <option v-for="favorite in world.favorites" :key="favorite.id" :value="favorite.id">{{ islandName(favorite) }}</option>
          </select>
        </div>
      </nav>

      <div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_17rem]">
        <div class="relative min-w-0">
          <ForestMap
            v-if="mapReady" ref="map" :view="scene.view" :scene="sceneKey" :marks="marks" :trees="trees"
            :selected="selected" :filter="filter" :ornaments="ornaments" :moving="moving"
            :caption="caption" @mark="followMark" @tree="chooseTree" @plot="moveTree"
          />
          <div v-if="mapReady && scene.view === 'island' && !trees.length" class="empty-note pointer-events-none absolute left-3 top-3 max-w-[85%] rounded-control p-3 text-sm">
            <p class="font-semibold">第一座岛等你靠岸</p>
            <p class="mt-1 text-xs">达到 {{ number.format(world.seedGoal) }} Token，或积满 50 分钟专注、10 分钟已确认休息，即可种下第一棵树。</p>
          </div>
          <p v-if="navigating" class="empty-note absolute left-3 top-3 rounded-control px-3 py-2 text-sm" role="status">正在读取地图…</p>
        </div>
        <aside class="min-w-0 space-y-3 rounded-card border border-border bg-card p-3" aria-label="地图详情">
          <template v-if="scene.view === 'island' && island">
            <template v-if="selected">
              <div class="flex items-start justify-between gap-2">
                <div class="min-w-0">
                  <h3 class="break-all font-semibold">{{ treeName(selected) }}</h3>
                  <p class="text-xs text-muted-foreground">{{ labels[selected.kind] }} · {{ plantedOn(selected.planted) }} 种下</p>
                  <p v-if="selected.assetVersion !== treeAssetVersion" class="text-xs text-muted-foreground">素材版本 {{ selected.assetVersion }} 暂不可用</p>
                </div>
                <UiButton size="small" variant="text" :disabled="busy" @click="closeTree">返回</UiButton>
              </div>
              <form class="space-y-2" @submit.prevent="saveTree">
                <label class="block text-xs">名字<input v-model="treeDraft.name" class="field-surface mt-1 block w-full px-2 py-1.5 text-sm" maxlength="30" :placeholder="styles[selected.kind][selected.style]" :disabled="busy" /></label>
                <label class="block text-xs">外观
                  <select v-model="treeDraft.style" class="field-surface mt-1 block w-full px-2 py-1.5 text-sm" :disabled="busy">
                    <option v-for="(name, index) in styles[selected.kind]" :key="name" :value="index" :disabled="selected.kind === 'usage' && world.level < styleLevels[index]">{{ name }}{{ selected.kind === 'usage' && world.level < styleLevels[index] ? ` · Lv. ${styleLevels[index]}` : '' }}</option>
                  </select>
                </label>
                <div class="flex flex-wrap gap-2">
                  <UiButton type="submit" size="small" :disabled="busy || !treeDirty">保存</UiButton>
                  <UiButton type="button" size="small" variant="outlined" :disabled="busy || !freeSlots.length" :aria-pressed="moving" @click="moving = !moving">{{ moving ? '取消移动' : '移动' }}</UiButton>
                </div>
              </form>
              <div v-if="moving" class="space-y-2">
                <p class="text-xs text-muted-foreground">点地图上的空地，或选择一个空地编号。</p>
                <label class="block text-xs">空地
                  <select class="field-surface mt-1 w-full px-2 py-1.5 text-sm" :disabled="busy" @change="pickPlot">
                    <option value="">请选择</option>
                    <option v-for="slot in freeSlots" :key="slot" :value="slot">空地 {{ slot + 1 }}</option>
                  </select>
                </label>
              </div>
            </template>
            <template v-else>
              <h3 class="break-all font-semibold">{{ islandName(island.island) }}</h3>
              <p class="text-xs text-muted-foreground">{{ trees.length }} / {{ capacity }} 个地块已有树木</p>
              <p v-if="trees.length === capacity" class="text-xs text-muted-foreground">本岛地块已满，后续种植会自动扩岛。</p>
              <form v-if="trees.length" class="space-y-2" @submit.prevent="saveIsland">
                <label class="block text-xs">岛名<input v-model="islandDraft.name" class="field-surface mt-1 block w-full px-2 py-1.5 text-sm" maxlength="30" :placeholder="islandName({ ...island.island, name: '' })" :disabled="busy" /></label>
                <label class="flex items-center gap-2 text-sm"><input v-model="islandDraft.favorite" type="checkbox" :disabled="busy" />收藏这座岛</label>
                <UiButton type="submit" size="small" :disabled="busy || !islandDirty">保存岛屿</UiButton>
              </form>
            </template>
            <div v-if="trees.length" class="border-t border-border pt-3">
              <label class="block text-xs">查找树木
                <select v-model="filter" class="field-surface mt-1 w-full px-2 py-1.5 text-sm">
                  <option :value="undefined">全部树木</option>
                  <option v-for="kind in kinds" :key="kind" :value="kind">{{ labels[kind] }}</option>
                </select>
              </label>
              <ul v-if="visibleTrees.length" class="mt-2 max-h-36 overflow-y-auto">
                <li v-for="tree in visibleTrees" :key="tree.id">
                  <button type="button" class="object-button" :aria-current="selected?.id === tree.id ? 'true' : undefined" :disabled="busy" @click="chooseTree(tree)">{{ treeName(tree) }} <span class="text-xs text-muted-foreground">· {{ tree.slot + 1 }} 号地</span></button>
                </li>
              </ul>
              <p v-else class="mt-2 text-xs text-muted-foreground">这座岛上还没有这一类树。</p>
            </div>
          </template>
          <template v-else>
            <h3 class="font-semibold">{{ scene.view === 'world' ? '群岛一览' : groupName(scene.id) }}</h3>
            <ul v-if="marks.length" class="max-h-80 overflow-y-auto">
              <li v-for="mark in marks" :key="mark.id">
                <button type="button" class="object-button" :disabled="busy" @click="followMark(mark.id)">{{ mark.label }} <span class="text-xs text-muted-foreground">· {{ mark.count }} {{ scene.view === 'world' ? '座岛' : '棵树' }}</span></button>
              </li>
            </ul>
            <p v-else class="text-sm text-muted-foreground">种下第一棵树后，就能在海图上找到第一座岛。</p>
          </template>
        </aside>
      </div>

      <div v-if="harvest" class="flex flex-wrap items-center justify-between gap-2 rounded-control border border-border bg-card px-3 py-2 text-sm">
        <p role="status">{{ harvest }}</p>
        <UiButton variant="text" size="x-small" @click="harvest = ''">收起</UiButton>
      </div>
      <div class="grid gap-4 md:grid-cols-2">
        <section class="rounded-card border border-border bg-card p-4" aria-labelledby="forest-growth">
          <h3 id="forest-growth" class="font-semibold">下一次成长</h3>
          <p v-if="world.level >= 20" class="mt-1 text-xs text-muted-foreground">{{ world.baseline ? `个人基准每日 ${number.format(world.baseline)} Token，已开始的目标保持当前门槛。` : '完整记录日还不足，后续等级目标使用三倍线性门槛。' }}</p>
          <dl class="mt-3 grid gap-3">
            <div v-for="row in progressRows" :key="row.label">
              <div class="flex flex-wrap justify-between gap-1 text-sm">
                <dt>{{ row.label }}</dt>
                <dd class="text-muted-foreground tabular-nums">{{ number.format(row.value) }} / {{ number.format(row.goal) }} {{ row.unit }}</dd>
              </div>
              <progress class="growth-bar" :aria-label="row.label" :value="Math.min(row.value, row.goal)" :max="row.goal"></progress>
            </div>
          </dl>
        </section>
        <ForestTimer @changed="timerChanged" />
      </div>

      <div class="grid gap-4 md:grid-cols-2">
        <section class="rounded-card border border-border bg-card p-4" aria-labelledby="forest-rewards">
          <div class="flex flex-wrap items-baseline justify-between gap-2">
            <h3 id="forest-rewards" class="font-semibold">成就与装饰</h3>
            <span class="text-xs text-muted-foreground">可用 {{ number.format(world.balance) }} 点</span>
          </div>
          <ul class="mt-3 space-y-2 text-sm">
            <li v-for="ornament in world.ornaments" :key="ornament.id" class="flex flex-wrap items-center justify-between gap-2">
              <span>{{ ornament.name }} <span class="text-xs text-muted-foreground">· Lv. {{ ornament.level }} · {{ ornament.price }} 点</span></span>
              <span v-if="ornament.owned" class="text-xs text-muted-foreground">已布置</span>
              <UiButton v-else variant="outlined" size="small" :disabled="busy || world.level < ornament.level || world.balance < ornament.price" @click="mutate(() => forestApi.redeem(ornament.id))">兑换</UiButton>
            </li>
          </ul>
          <details class="mt-3 border-t border-border pt-3 text-sm">
            <summary class="cursor-pointer">树木图鉴</summary>
            <dl class="mt-2 space-y-2">
              <div v-for="kind in kinds" :key="kind">
                <dt class="font-medium">{{ labels[kind] }} · {{ number.format(world.counts[kind]) }} 棵</dt>
                <dd>
                  <ul class="mt-1 space-y-1 text-xs">
                    <li v-for="(name, index) in styles[kind]" :key="name" class="flex flex-wrap justify-between gap-1">
                      <span>{{ name }}</span>
                      <span class="text-muted-foreground">{{ discoveryText(kind, index) }}</span>
                    </li>
                  </ul>
                </dd>
              </div>
            </dl>
          </details>
        </section>
        <section class="rounded-card border border-border bg-card p-4" aria-labelledby="forest-patrol">
          <h3 id="forest-patrol" class="font-semibold">{{ scene.view === 'island' ? '本岛巡护' : '群岛里程碑' }}</h3>
          <ul v-if="events.length" class="mt-3 max-h-56 space-y-3 overflow-y-auto text-sm">
            <li v-for="event in events" :key="event.id">
              <p class="font-medium">{{ event.title }}</p>
              <p class="text-xs leading-5 text-muted-foreground">{{ event.story }}</p>
              <UiButton v-if="scene.view !== 'island' && event.island" variant="text" size="x-small" :disabled="busy" @click="open('island', event.island)">查看岛屿</UiButton>
            </li>
          </ul>
          <p v-else class="mt-2 text-xs text-muted-foreground">这里还没有巡护记录。</p>
        </section>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { storeToRefs } from 'pinia'
import { computed, nextTick, onMounted, ref, shallowRef, watch } from 'vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiButton from '@/shared/ui/button/Button.vue'
import type { ForestGroup, ForestIsland, ForestIslandView, ForestTree, TreeKind } from '@/types/forest'
import { number } from '@/features/usage-journal/presentation'
import ForestMap from './ForestMap.vue'
import ForestTimer from './ForestTimer.vue'
import { capacity, islandsPerGroup, landmark, plotPositions, treeAssetVersion, type Mark, type View } from './map'
import { forestApi, groupOf, islandName, kinds, labels, styles, treeName, useForest } from './store'

const props = defineProps<{ timeZone: string }>()
const emit = defineEmits<{ growthChanged: [] }>()
const forest = useForest()
const { world } = storeToRefs(forest)
const styleLevels = [1, 5, 15, 30]
const panel = ref<HTMLElement>()
const map = ref<InstanceType<typeof ForestMap>>()
const scene = shallowRef<{ view: View; id: number }>({ view: 'island', id: 1 })
const sceneKey = computed(() => `${scene.value.view}:${scene.value.id}`)
const groups = shallowRef<ForestGroup[]>([])
const islands = shallowRef<ForestIsland[]>([])
const island = shallowRef<ForestIslandView>()
const selectedId = ref<number>()
const filter = ref<TreeKind>()
const moving = ref(false)
const mapReady = ref(false)
const treeDraft = ref({ name: '', style: 0 })
const islandDraft = ref({ name: '', favorite: false })
const navigating = ref(false)
const refreshing = ref(false)
const saving = ref(false)
const loadError = ref('')
const operationError = ref('')
const harvest = ref('')
const busy = computed(() => navigating.value || saving.value)
const autoBusy = computed(() => busy.value || refreshing.value)
const trees = computed(() => scene.value.view === 'island' ? island.value?.trees ?? [] : [])
const selected = computed(() => trees.value.find(tree => tree.id === selectedId.value))
const visibleTrees = computed(() => trees.value.filter(tree => !filter.value || tree.kind === filter.value))
const treeDirty = computed(() => selected.value && (treeDraft.value.name !== selected.value.name || treeDraft.value.style !== selected.value.style))
const islandDirty = computed(() => island.value && (islandDraft.value.name !== island.value.island.name || islandDraft.value.favorite !== island.value.island.favorite))
const freeSlots = computed(() => plotPositions.flatMap((_, slot) => trees.value.some(tree => tree.slot === slot) ? [] : [slot]))
const totalTrees = computed(() => Object.values(world.value?.counts ?? {}).reduce((total, count) => total + count, 0))
const groupName = (id: number) => `群岛 ${id + 1}`
const plantedOn = (time: number) => new Date(time).toLocaleDateString('zh-CN', { timeZone: props.timeZone })
const discoveries = computed(() => new Map<string, number>(world.value?.discoveries.map(item => [`${item.kind}:${item.style}`, item.firstPlanted])))
function discoveryText(kind: TreeKind, style: number) {
  const found = discoveries.value.get(`${kind}:${style}`)
  if (found !== undefined) return `${plantedOn(found)} 发现`
  if (kind === 'usage' && (world.value?.level ?? 0) < styleLevels[style]) return `Lv. ${styleLevels[style]} 解锁`
  return '尚未发现'
}
const caption = computed(() => scene.value.view === 'world' ? '森林海图' : scene.value.view === 'group' ? groupName(scene.value.id) : island.value ? islandName(island.value.island) : '第一座岛')
const events = computed(() => scene.value.view === 'island' ? island.value?.events ?? [] : world.value?.milestones ?? [])
const owned = computed(() => new Set(world.value?.ornaments.filter(item => item.owned).map(item => item.id)))
const islandIndex = (id: number) => (id - 1) % islandsPerGroup
const hasLighthouse = (id: number) => owned.value.has('lantern') && islandIndex(id) === 0
const hasMonument = (id: number, count: number) => owned.value.has('monument') && islandIndex(id) === islandsPerGroup - 1 && count === capacity
const ornaments = computed(() => [
  ...(owned.value.has('trail') ? ['trail'] : []),
  ...(hasLighthouse(scene.value.id) ? ['lantern'] : []),
  ...(hasMonument(scene.value.id, trees.value.length) ? ['monument'] : []),
  ...events.value.map(event => event.id.split(':')[0])
])
const marks = computed<Mark[]>(() => {
  if (scene.value.view === 'world') return groups.value.map(group => ({
    id: group.id, label: groupName(group.id), count: group.islands, radius: 64, trees: group.trees,
    lighthouse: owned.value.has('lantern'), monument: owned.value.has('monument') && group.trees === capacity * islandsPerGroup,
    ...landmark(group.id, 'world')
  }))
  if (scene.value.view === 'group') return islands.value.map(item => ({
    id: item.id, label: islandName(item), count: item.count, radius: 68, trees: item.count, favorite: item.favorite,
    lighthouse: hasLighthouse(item.id), monument: hasMonument(item.id, item.count), ...landmark(islandIndex(item.id), 'group')
  }))
  return []
})
const progressRows = computed(() => {
  const value = world.value
  if (!value) return []
  return [
    { label: '用量树', value: value.seed, goal: value.seedGoal, unit: 'Token' },
    { label: '专注树', value: Math.floor(value.focus / 60000), goal: 50, unit: '分钟' },
    { label: '休息树', value: Math.floor(value.rest / 60000), goal: 10, unit: '分钟' },
    { label: `等级 ${value.level + 1}`, value: value.levelProgress, goal: value.levelGoal, unit: 'Token' }
  ]
})

function message(cause: unknown) { return cause instanceof Error ? cause.message : String(cause) }

async function loadScene(view: View, id: number, accept: () => boolean) {
  if (view === 'world') {
    const value = await forestApi.groups()
    if (accept()) groups.value = value
  } else if (view === 'group') {
    const value = await forestApi.islands(id)
    if (accept()) islands.value = value
  } else {
    const value = await forestApi.island(id)
    if (!accept()) return
    const preserve = island.value?.island.id === id && islandDirty.value
    const preserveTree = treeDirty.value
    island.value = value
    if (!preserve) islandDraft.value = { name: value.island.name, favorite: value.island.favorite }
    if (selected.value && !preserveTree) treeDraft.value = { name: selected.value.name, style: selected.value.style }
  }
}

async function open(view: View, id: number, treeId?: number) {
  if (busy.value) return
  navigationRevision++
  navigating.value = true
  loadError.value = ''
  operationError.value = ''
  try {
    const changed = view !== scene.value.view || id !== scene.value.id
    await loadScene(view, id, () => true)
    scene.value = { view, id }
    mapReady.value = true
    if (changed) {
      selectedId.value = undefined
      filter.value = undefined
    }
    moving.value = false
    const tree = trees.value.find(item => item.id === treeId)
    if (tree) selectTree(tree)
    else if (treeId !== undefined) throw new Error('这棵树暂时无法定位，请刷新森林后重试。')
  } catch (cause) {
    loadError.value = message(cause)
  } finally {
    navigating.value = false
  }
}

async function refresh(initial = false, committed = false) {
  if (!committed && (refreshing.value || busy.value)) return
  const revision = ++refreshRevision
  const navigation = navigationRevision
  const key = sceneKey.value
  const current = scene.value
  const accept = () => revision === refreshRevision && navigation === navigationRevision && key === sceneKey.value && !navigating.value
  refreshing.value = true
  loadError.value = ''
  try {
    const value = await forest.load()
    if (initial && value.islands > 1 && accept()) {
      await open(value.islands > 8 ? 'world' : 'group', 0)
    } else if (accept()) {
      await loadScene(current.view, current.id, accept)
      if (accept()) mapReady.value = true
    }
  } catch (cause) {
    if (revision === refreshRevision) loadError.value = `${committed ? '操作已保存，刷新失败。' : ''}${message(cause)}`
  } finally {
    if (revision === refreshRevision) refreshing.value = false
  }
}

function followMark(id: number) {
  return open(scene.value.view === 'world' ? 'group' : 'island', id)
}

function openFavorite(event: Event) {
  const input = event.target as HTMLSelectElement
  if (input.value) void open('island', Number(input.value))
  input.value = ''
}

function selectTree(tree: ForestTree) {
  if (selectedId.value !== tree.id || !treeDirty.value) treeDraft.value = { name: tree.name, style: tree.style }
  selectedId.value = tree.id
  if (filter.value && filter.value !== tree.kind) filter.value = undefined
  moving.value = false
  void nextTick().then(() => map.value?.focusTree())
}

function chooseTree(tree: ForestTree) { if (!busy.value) selectTree(tree) }
function closeTree() {
  selectedId.value = undefined
  moving.value = false
  void nextTick().then(() => map.value?.restoreIsland())
}

async function locate(tree: ForestTree) {
  panel.value?.scrollIntoView({ block: 'start' })
  await open('island', tree.island, tree.id)
}

let refreshRevision = 0
let navigationRevision = 0
async function mutate(action: () => Promise<unknown>, done?: () => void) {
  if (busy.value) return
  saving.value = true
  operationError.value = ''
  try {
    await action()
    done?.()
    emit('growthChanged')
    await refresh(false, true)
  } catch (cause) {
    operationError.value = message(cause)
  } finally {
    saving.value = false
  }
}

function saveTree() {
  const tree = selected.value
  if (!tree) return
  const { style } = treeDraft.value
  const name = treeDraft.value.name.trim()
  treeDraft.value.name = name
  return mutate(() => forestApi.treeEdit(tree.id, {
    name: name === tree.name ? undefined : name,
    style: style === tree.style ? undefined : style
  }))
}
function saveIsland() {
  islandDraft.value.name = islandDraft.value.name.trim()
  return mutate(() => forestApi.islandEdit(scene.value.id, { ...islandDraft.value }))
}
function moveTree(slot: number) {
  const id = selected.value?.id
  if (id !== undefined && moving.value) return mutate(() => forestApi.treeEdit(id, { slot }), () => { moving.value = false })
}
function pickPlot(event: Event) {
  const input = event.target as HTMLSelectElement
  if (input.value !== '') void moveTree(Number(input.value))
  input.value = ''
}
function timerChanged() { void refresh(false, true) }

watch(world, (value, previous) => {
  if (!value || !previous) return
  const changes = kinds.flatMap(kind => {
    const count = value.counts[kind] - previous.counts[kind]
    return count > 0 ? [`种下 ${number.format(count)} 棵${labels[kind]}`] : []
  })
  if (value.islands > previous.islands) changes.push(`发现 ${value.islands - previous.islands} 座新岛`)
  if (value.level > previous.level) changes.push(`升到 Lv. ${value.level}`)
  if (value.points > previous.points) changes.push(`获得 ${number.format(value.points - previous.points)} 点成就`)
  if (changes.length) {
    harvest.value = changes.join(' · ')
    emit('growthChanged')
  }
})

onMounted(() => { void refresh(true) })
defineExpose({ locate, refresh: () => refresh() })
</script>

<style scoped>
.atlas { background: radial-gradient(ellipse at left top, color-mix(in srgb, var(--success) 12%, transparent), transparent 56%), var(--surface-bright); }
.atlas-title { font-family: 'Songti SC', 'Noto Serif CJK SC', SimSun, serif; }
.empty-note { background: var(--surface-bright); }
.object-button { width: 100%; border-radius: var(--radius-control); padding: 0.3rem 0.45rem; text-align: left; overflow-wrap: anywhere; font-size: 0.875rem; }
.object-button:hover, .object-button[aria-current] { background: var(--muted); }
.growth-bar { display: block; width: 100%; height: 0.55rem; margin-top: 0.35rem; accent-color: var(--success); }
</style>
