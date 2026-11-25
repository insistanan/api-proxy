<template>
  <section class="space-y-4">
    <UiAlert v-if="loadError" type="error">{{ loadError }}</UiAlert>
    <UiAlert v-if="operationError && !treeOpen && !islandOpen" type="error">{{ operationError }}</UiAlert>
    <template v-if="world">
      <header class="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h2 class="font-serif text-2xl font-semibold">海上的森林</h2>
          <p class="mt-1 text-sm text-muted-foreground">
            {{ number.format(totalTrees) }} 棵树 · {{ world.islands }} 座岛屿
          </p>
          <p v-if="world.ecology.damaged || world.ecology.destroyed" class="mt-1 text-sm text-warning">
            {{ world.ecology.damaged }} 棵等待照料 · {{ world.ecology.destroyed }} 处等待重种
          </p>
        </div>
        <div class="flex flex-wrap items-center gap-3">
          <button class="level-seal" @click="emit('collection')">
            Lv. {{ world.level }}
            <span class="ml-2 text-xs font-normal">
              {{ Math.min(100, Math.floor((world.levelProgress / world.levelGoal) * 100)) }}%
            </span>
            <progress :value="world.levelProgress" :max="world.levelGoal" aria-label="等级进度"></progress>
          </button>
          <UiButton variant="outlined" size="small" @click="timerOpen = true">
            {{ timer?.mode ? '查看计时' : '留一点时间给自己' }}
          </UiButton>
        </div>
      </header>
      <p v-if="world.catchingUp" role="status" class="text-xs text-muted-foreground">新的绿意正在赶来…</p>
      <UiAlert v-if="world.syncIssue" type="warning">森林更新暂时遇到问题，请稍后刷新。</UiAlert>
      <div class="panel-surface overflow-hidden">
        <div class="flex flex-wrap items-center justify-between gap-2 p-3">
          <nav class="flex flex-wrap items-center gap-1" aria-label="海图位置">
            <UiButton size="small" variant="text" :disabled="busy || scene.view === 'world'" @click="open('world', 0)">
              海图
            </UiButton>
            <template v-if="scene.view !== 'world'">
              <span class="text-muted-foreground">/</span>
              <UiButton
                size="small"
                variant="text"
                :disabled="busy || scene.view === 'group'"
                @click="open('group', groupOf(scene.id))"
              >
                {{ groupName(scene.view === 'group' ? scene.id : groupOf(scene.id)) }}
              </UiButton>
            </template>
            <template v-if="scene.view === 'island'">
              <span class="text-muted-foreground">/</span>
              <button class="px-2 text-sm font-medium" :disabled="!trees.length || busy" @click="islandOpen = true">
                {{ caption }}
              </button>
            </template>
          </nav>
          <div class="flex gap-1">
            <UiButton size="small" variant="text" :disabled="busy" @click="directoryOpen = true">
              {{ scene.view === 'island' ? '找一棵树' : '岛屿目录' }}
            </UiButton>
            <UiButton v-if="world.latest" size="small" variant="text" :disabled="busy" @click="locate(world.latest.id)">
              最近的生长
            </UiButton>
          </div>
        </div>
        <div class="relative">
          <ForestMap
            v-if="mapReady"
            ref="map"
            :view="scene.view"
            :scene="sceneKey"
            :marks="marks"
            :trees="trees"
            :selected="selected"
            :ornaments="ornaments"
            :moving="moving"
            :caption="caption"
            @mark="followMark"
            @tree="chooseTree"
            @plot="moveTree"
          />
          <p v-else role="status" class="p-12 text-center text-muted-foreground">正在展开海图…</p>
          <div
            v-if="mapReady && !world.islands"
            class="empty-note pointer-events-none absolute inset-x-6 top-8 mx-auto max-w-sm rounded-card p-5 text-center"
          >
            <h3 class="text-lg font-semibold">第一阵风已经到了</h3>
            <p class="mt-2 text-sm text-muted-foreground">下一次灵感，就从这里萌芽。</p>
          </div>
          <div
            v-if="moving"
            class="map-hint absolute inset-x-3 top-3 flex flex-wrap items-center justify-between gap-2 rounded-control p-3 text-sm"
          >
            <span>为 {{ selected && treeName(selected) }} 选一块亮着的空地</span>
            <select class="field-surface p-1" aria-label="选择空地" :disabled="busy" @change="pickPlot">
              <option value="">选择位置</option>
              <option v-for="slot in freeSlots" :key="slot" :value="slot">{{ slot + 1 }} 号空地</option>
            </select>
            <UiButton
              size="small"
              variant="text"
              @click="moving = false; closeTree()"
            >
              取消移动
            </UiButton>
          </div>
        </div>
        <footer class="flex flex-wrap justify-between gap-2 px-4 py-3 text-xs text-muted-foreground">
          <span>{{ scene.view === 'island' ? '点一棵树，听听它的故事' : '点岛屿继续探索' }} · 拖动平移 · 滚轮缩放</span>
          <span v-if="scene.view === 'island'">
            {{ island?.island.count || 0 }} 棵树
            <span v-if="island?.island.destroyed">· {{ island.island.destroyed }} 处残桩</span>
            · {{ trees.length }} / {{ capacity }} 块林地
          </span>
          <span v-else>{{ marks.length }} {{ scene.view === 'world' ? '片群岛' : '座岛屿' }}</span>
        </footer>
      </div>
    </template>
    <AppDialog v-model="timerOpen" title="给自己一点时间"><ForestTimer @changed="timerChanged" /></AppDialog>
    <AppDialog v-model="directoryOpen" :title="scene.view === 'island' ? caption : '岛屿目录'">
      <template v-if="scene.view === 'island'">
        <label class="block text-sm">
          寻找树木
          <input v-model="search" class="field-surface my-3 w-full p-2" placeholder="输入树的名字" />
        </label>
        <div class="grid grid-cols-2 gap-2 sm:grid-cols-3">
          <button
            v-for="tree in visibleTrees"
            :key="tree.id"
            class="rounded-card border border-border p-2"
            @click="directoryOpen = false; chooseTree(tree)"
          >
            <div class="h-24">
              <ForestArt
                :label="treeName(tree)"
                :kind="tree.kind"
                :tree-style="tree.style"
                :accessory="tree.accessory"
                :variant="tree.variant"
                :condition="tree.condition"
              />
            </div>
            <p class="text-sm">{{ treeName(tree) }}</p>
            <p v-if="tree.condition !== 'healthy'" class="mt-1 text-xs text-warning">
              {{ conditionNames[tree.condition] }}
            </p>
            <p class="mt-1 text-xs text-muted-foreground">{{ tree.slot + 1 }} 号地</p>
          </button>
        </div>
        <p v-if="!visibleTrees.length" class="py-6 text-center text-sm text-muted-foreground">这里暂时没有找到树木。</p>
      </template>
      <div v-else class="grid gap-2 sm:grid-cols-2">
        <button
          v-for="mark in marks"
          :key="mark.id"
          class="rounded-card border border-border p-4 text-left"
          @click="directoryOpen = false; followMark(mark.id)"
        >
          <p class="font-medium">{{ mark.label }}</p>
          <p class="mt-1 text-sm text-muted-foreground">
            {{ mark.count }} {{ scene.view === 'world' ? '座岛' : '棵树' }}
          </p>
          <p v-if="mark.damaged || mark.destroyed" class="mt-1 text-xs text-warning">
            {{ mark.damaged }} 棵受损 · {{ mark.destroyed }} 处残桩
          </p>
        </button>
      </div>
      <div v-if="world?.favorites.length" class="mt-5 border-t border-border pt-4">
        <p class="mb-2 text-sm text-muted-foreground">收藏的岛屿</p>
        <UiButton
          v-for="item in world.favorites"
          :key="item.id"
          size="small"
          variant="text"
          @click="directoryOpen = false; open('island', item.id)"
        >
          {{ islandName(item) }}
        </UiButton>
      </div>
    </AppDialog>
    <AppDialog
      :model-value="treeOpen"
      :title="selected ? treeName(selected) : '林间相遇'"
      :busy="saving"
      @update:model-value="
        value => {
          if (!value) closeTree()
        }
      "
    >
      <template v-if="selected">
        <div class="mx-auto h-52 max-w-xs">
          <ForestArt
            :label="treeName(selected)"
            :kind="selected.kind"
            :tree-style="treeDraft.style"
            :accessory="treeDraft.accessory"
            :variant="selected.variant"
            :condition="selected.condition"
          />
        </div>
        <p class="text-center text-sm text-muted-foreground">
          {{ origins[selected.kind] }} · {{ plantedOn(selected.planted) }}
        </p>
        <div
          v-if="selected.condition !== 'healthy' && recoveryCost"
          class="mt-5 rounded-card border border-warning/30 bg-warning/5 p-4"
        >
          <p class="font-medium">
            {{ selected.condition === 'destroyed' ? '给这片空地一个新的开始' : '这棵树需要一点照料' }}
          </p>
          <p class="mt-2 text-sm text-muted-foreground">
            {{ selected.condition === 'destroyed' ? '带上花种，让熟悉的树荫重新回来。' : '修好折枝，浇一点晨露。' }}
          </p>
          <p class="mt-3 text-sm">需要 {{ resourceText(recoveryCost) }}</p>
          <UiButton class="mt-3" size="small" :loading="saving" :disabled="!canRecover" @click="recover">
            {{ canRecover ? (selected.condition === 'destroyed' ? '重新种下' : '照料这棵树') : '材料还差一点' }}
          </UiButton>
        </div>
        <form id="forest-tree-edit" class="mt-6 space-y-4" @submit.prevent="saveTree">
          <label class="block text-sm">
            名字
            <input
              v-model="treeDraft.name"
              class="field-surface mt-2 w-full p-2"
              maxlength="30"
              :placeholder="styles[selected.kind][selected.style]"
              :disabled="saving"
            />
          </label>
          <div class="grid gap-4 sm:grid-cols-2">
            <label class="block text-sm">
              树的模样
              <select v-model.number="treeDraft.style" class="field-surface mt-2 w-full p-2" :disabled="saving">
                <option v-for="option in availableStyles" :key="option.index" :value="option.index">
                  {{ option.name }}
                </option>
              </select>
            </label>
            <label class="block text-sm">
              随身饰品
              <select v-model="treeDraft.accessory" class="field-surface mt-2 w-full p-2" :disabled="saving">
                <option value="">轻装上阵</option>
                <option v-for="item in accessories" :key="item.id" :value="item.id">{{ item.name }}</option>
              </select>
            </label>
          </div>
          <UiAlert v-if="operationError" type="error">{{ operationError }}</UiAlert>
        </form>
      </template>
      <template #actions>
        <UiButton v-if="freeSlots.length" variant="text" :disabled="saving" @click="startMoving">换个位置</UiButton>
        <UiButton type="submit" form="forest-tree-edit" :loading="saving" :disabled="!treeDirty">保存</UiButton>
      </template>
    </AppDialog>
    <AppDialog v-model="islandOpen" title="这座岛的名字" :busy="saving">
      <form id="forest-island-edit" class="space-y-4" @submit.prevent="saveIsland">
        <label class="block text-sm">
          岛名
          <input v-model="islandDraft.name" class="field-surface mt-2 w-full p-2" maxlength="30" :disabled="saving" />
        </label>
        <label class="flex items-center gap-2 text-sm">
          <input v-model="islandDraft.favorite" type="checkbox" :disabled="saving" />
          收藏在海图上
        </label>
        <UiAlert v-if="operationError" type="error">{{ operationError }}</UiAlert>
      </form>
      <template #actions>
        <UiButton type="submit" form="forest-island-edit" :loading="saving" :disabled="!islandDirty">保存</UiButton>
      </template>
    </AppDialog>
  </section>
</template>

<script setup lang="ts">
import { storeToRefs } from 'pinia'
import { computed, nextTick, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import UiButton from '@/shared/ui/button/Button.vue'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import type { ForestGroup, ForestIsland, ForestIslandView, ForestTree } from '@/types/forest'
import { number } from '@/features/usage-journal/presentation'
import ForestMap from '@/features/usage-journal/forest/ForestMap.vue'
import ForestTimer from '@/features/usage-journal/forest/ForestTimer.vue'
import ForestArt from '@/features/usage-journal/forest/ForestArt.vue'
import {
  capacity,
  islandsPerGroup,
  landmark,
  plotPositions,
  type Mark,
  type View
} from '@/features/usage-journal/forest/map'
import {
  forestApi,
  conditionNames,
  groupOf,
  islandName,
  origins,
  resourceText,
  styles,
  treeName,
  useForest
} from '@/features/usage-journal/forest/store'

const props = defineProps<{ timeZone: string; targetTree: number }>()
const emit = defineEmits<{ collection: [] }>()
const forest = useForest(),
  route = useRoute(),
  router = useRouter()
const { world, timer } = storeToRefs(forest)
const map = ref<InstanceType<typeof ForestMap>>()
const scene = shallowRef<{ view: View; id: number }>({ view: 'island', id: 1 })
const sceneKey = computed(() => `${scene.value.view}:${scene.value.id}`)
const groups = shallowRef<ForestGroup[]>([]),
  islands = shallowRef<ForestIsland[]>([])
const island = shallowRef<ForestIslandView>()
const selectedId = ref<number>()
const treeOpen = ref(false),
  islandOpen = ref(false),
  directoryOpen = ref(false),
  timerOpen = ref(route.query.timer === '1')
const moving = ref(false),
  mapReady = ref(false),
  search = ref('')
const treeDraft = ref({ name: '', style: 0, accessory: '' })
const islandDraft = ref({ name: '', favorite: false })
const navigating = ref(false),
  refreshing = ref(false),
  saving = ref(false)
const loadError = ref(''),
  operationError = ref('')
const busy = computed(() => navigating.value || saving.value)
const trees = computed(() => (scene.value.view === 'island' ? (island.value?.trees ?? []) : []))
const selected = computed(() => trees.value.find(tree => tree.id === selectedId.value))
const visibleTrees = computed(() => trees.value.filter(tree => treeName(tree).includes(search.value.trim())))
const treeDirty = computed(
  () =>
    selected.value &&
    (treeDraft.value.name !== selected.value.name ||
      treeDraft.value.style !== selected.value.style ||
      treeDraft.value.accessory !== selected.value.accessory)
)
const islandDirty = computed(
  () =>
    island.value &&
    (islandDraft.value.name !== island.value.island.name || islandDraft.value.favorite !== island.value.island.favorite)
)
const freeSlots = computed(() =>
  plotPositions.flatMap((_, slot) => (trees.value.some(tree => tree.slot === slot) ? [] : [slot]))
)
const totalTrees = computed(() => Object.values(world.value?.counts ?? {}).reduce((total, count) => total + count, 0))
const recoveryCost = computed(() =>
  !world.value || !selected.value || selected.value.condition === 'healthy'
    ? undefined
    : selected.value.condition === 'destroyed'
      ? world.value.ecology.replantCost
      : world.value.ecology.repairCost
)
const canRecover = computed(() => {
  const cost = recoveryCost.value,
    available = world.value?.resources
  return (
    !!cost && !!available && available.wood >= cost.wood && available.seeds >= cost.seeds && available.dew >= cost.dew
  )
})
const groupName = (id: number) => `群岛 ${id + 1}`
const plantedOn = (time: number) => new Date(time).toLocaleDateString('zh-CN', { timeZone: props.timeZone })
const caption = computed(() =>
  scene.value.view === 'world'
    ? '森林海图'
    : scene.value.view === 'group'
      ? groupName(scene.value.id)
      : island.value
        ? islandName(island.value.island)
        : '第一座岛'
)
const owned = computed(() => new Set(world.value?.ornaments.filter(item => item.owned).map(item => item.id)))
const accessories = computed(
  () => world.value?.ornaments.filter(item => item.category === 'accessory' && item.owned) ?? []
)
const availableStyles = computed(() => {
  const tree = selected.value
  if (!tree || !world.value) return []
  return styles[tree.kind].flatMap((name, index) => {
    const allowed =
      index < 4
        ? tree.kind !== 'usage' || world.value!.level >= [1, 5, 15, 30][index]
        : world.value!.ornaments.some(
            item => item.owned && item.category === 'species' && item.kind === tree.kind && item.style === index
          )
    return allowed ? [{ name, index }] : []
  })
})
const islandIndex = (id: number) => (id - 1) % islandsPerGroup
const hasLighthouse = (id: number) => owned.value.has('lantern') && islandIndex(id) === 0
const hasMonument = (id: number, count: number) => owned.value.has('monument') && islandIndex(id) === 0 && count > 0
const ornaments = computed(() => [
  ...(owned.value.has('trail') ? ['trail'] : []),
  ...(hasLighthouse(scene.value.id) ? ['lantern'] : []),
  ...(hasMonument(scene.value.id, trees.value.length) ? ['monument'] : []),
  ...(island.value?.events.map(event => event.id.split(':')[0]) ?? [])
])
const marks = computed<Mark[]>(() => {
  if (scene.value.view === 'world')
    return groups.value.map(group => ({
      id: group.id,
      label: groupName(group.id),
      count: group.islands,
      radius: 100,
      trees: group.trees,
      destroyed: group.destroyed,
      damaged: group.damaged,
      lighthouse: owned.value.has('lantern'),
      monument: owned.value.has('monument'),
      ...landmark(group.id, 'world')
    }))
  if (scene.value.view === 'group')
    return islands.value.map(item => ({
      id: item.id,
      label: islandName(item),
      count: item.count,
      radius: 68,
      trees: item.count,
      destroyed: item.destroyed,
      damaged: item.damaged,
      favorite: item.favorite,
      lighthouse: hasLighthouse(item.id),
      monument: hasMonument(item.id, item.count),
      ...landmark(islandIndex(item.id), 'group')
    }))
  return []
})
const message = (cause: unknown) => (cause instanceof Error ? cause.message : String(cause))
let navigationRevision = 0,
  refreshRevision = 0,
  locationRevision = 0
let mounted = false
let active = true
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
    const preserve = islandOpen.value && island.value?.island.id === id && islandDirty.value
    const preserveTree = treeOpen.value && treeDirty.value
    island.value = value
    if (!preserve) islandDraft.value = { name: value.island.name, favorite: value.island.favorite }
    if (selected.value && !preserveTree)
      treeDraft.value = { name: selected.value.name, style: selected.value.style, accessory: selected.value.accessory }
  }
}
async function open(view: View, id: number, treeId?: number) {
  if (saving.value) return
  const revision = ++navigationRevision
  navigating.value = true
  loadError.value = ''
  operationError.value = ''
  try {
    await loadScene(view, id, () => revision === navigationRevision)
    if (revision !== navigationRevision) return
    scene.value = { view, id }
    mapReady.value = true
    moving.value = false
    selectedId.value = undefined
    treeOpen.value = false
    const tree = trees.value.find(item => item.id === treeId)
    if (tree) chooseTree(tree)
    else if (treeId !== undefined) throw new Error('这棵树暂时无法定位，请刷新后重试。')
  } catch (cause) {
    if (revision === navigationRevision) loadError.value = message(cause)
  } finally {
    if (revision === navigationRevision) navigating.value = false
  }
}
async function refresh(committed = false) {
  if (!committed && (refreshing.value || busy.value)) return
  const revision = ++refreshRevision,
    navigation = navigationRevision
  const current = scene.value
  refreshing.value = true
  loadError.value = ''
  try {
    await forest.load()
    const accept = () => revision === refreshRevision && navigation === navigationRevision
    if (accept()) await loadScene(current.view, current.id, accept)
    if (accept()) mapReady.value = true
  } catch (cause) {
    if (revision === refreshRevision) loadError.value = `${committed ? '已保存，刷新失败：' : ''}${message(cause)}`
  } finally {
    if (revision === refreshRevision) refreshing.value = false
  }
}
function followMark(id: number) {
  return open(scene.value.view === 'world' ? 'group' : 'island', id)
}
function chooseTree(tree: ForestTree) {
  if (saving.value) return
  selectedId.value = tree.id
  treeDraft.value = { name: tree.name, style: tree.style, accessory: tree.accessory }
  treeOpen.value = true
  moving.value = false
  operationError.value = ''
  void nextTick().then(() => map.value?.focusTree())
}
function closeTree() {
  treeOpen.value = false
  selectedId.value = undefined
  moving.value = false
  map.value?.restoreIsland()
}
function startMoving() {
  treeOpen.value = false
  moving.value = true
  void nextTick().then(() => map.value?.restoreIsland())
}
async function locate(id: number) {
  const revision = ++locationRevision
  try {
    const tree = await forestApi.tree(id)
    if (revision === locationRevision) await open('island', tree.island, tree.id)
  } catch (cause) {
    if (revision === locationRevision) loadError.value = message(cause)
  }
}
async function mutate(action: () => Promise<unknown>, done: () => void) {
  if (busy.value) return
  saving.value = true
  operationError.value = ''
  try {
    await action()
    done()
    await refresh(true)
  } catch (cause) {
    operationError.value = message(cause)
  } finally {
    saving.value = false
  }
}
function saveTree() {
  const tree = selected.value
  if (!tree) return
  return mutate(() => forestApi.treeEdit(tree.id, { ...treeDraft.value, name: treeDraft.value.name.trim() }), closeTree)
}
function saveIsland() {
  return mutate(
    () => forestApi.islandEdit(scene.value.id, { ...islandDraft.value, name: islandDraft.value.name.trim() }),
    () => {
      islandOpen.value = false
    }
  )
}
function recover() {
  const tree = selected.value
  if (!tree) return
  return mutate(() => forestApi.recover(tree), closeTree)
}
function moveTree(slot: number) {
  const id = selected.value?.id
  if (id !== undefined && moving.value) return mutate(() => forestApi.treeEdit(id, { slot }), closeTree)
}
function pickPlot(event: Event) {
  const input = event.target as HTMLSelectElement
  if (input.value !== '') void moveTree(Number(input.value))
  input.value = ''
}
function timerChanged() {
  void refresh()
}
watch(
  () => props.targetTree,
  id => {
    if (mounted && id) void locate(id)
  }
)
watch(islandOpen, value => {
  if (value && island.value)
    islandDraft.value = { name: island.value.island.name, favorite: island.value.island.favorite }
})
watch(
  () => (world.value ? `${world.value.ecology.damaged}:${world.value.ecology.destroyed}` : ''),
  () => {
    if (mounted && !busy.value) void refresh()
  }
)
watch(
  () => route.query.timer,
  value => {
    if (value === '1') timerOpen.value = true
  }
)
watch(timerOpen, async value => {
  if (value || route.query.timer !== '1') return
  try {
    await router.replace({ query: { ...route.query, timer: undefined } })
  } catch (cause) {
    operationError.value = message(cause)
  }
})
onMounted(async () => {
  try {
    const value = await forest.load()
    if (!active) return
    mounted = true
    if (props.targetTree) await locate(props.targetTree)
    else await open('island', value.latest?.island || 1)
  } catch (cause) {
    loadError.value = message(cause)
    mounted = true
  }
})
onUnmounted(() => {
  active = false
  mounted = false
  navigationRevision++
  refreshRevision++
  locationRevision++
})
defineExpose({ refresh })
</script>
<style scoped>
.level-seal {
  min-width: 110px;
  padding: 0.5rem 0.8rem;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  color: var(--primary);
  font-weight: 600;
  text-align: left;
}
.level-seal progress {
  display: block;
  width: 100%;
  height: 3px;
  margin-top: 5px;
  accent-color: var(--primary);
}
.empty-note,
.map-hint {
  background: var(--surface-bright);
}
</style>
