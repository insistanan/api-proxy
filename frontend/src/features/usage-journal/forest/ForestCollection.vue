<template>
  <section v-if="world" class="space-y-6">
    <header class="collection-hero panel-surface relative overflow-hidden p-6 sm:p-9">
      <div class="relative z-10 flex flex-wrap items-end justify-between gap-5">
        <div>
          <p class="text-xs font-medium tracking-[0.25em] text-primary">
            {{ shop ? 'ISLAND BOUTIQUE' : 'THE HALL OF WONDERS' }}
          </p>
          <h2 class="mt-3 text-3xl font-semibold">{{ shop ? '岛屿商店' : '把走过的路，收藏成光' }}</h2>
          <p class="mt-3 text-sm text-muted-foreground">
            {{
              shop
                ? '挑一份喜欢的，让岛上多一点惊喜。'
                : `${unlocked.length} / ${world.achievements.length} 枚徽章已点亮`
            }}
          </p>
        </div>
        <div class="flex flex-wrap gap-2">
          <span class="wallet-chip">✦ {{ number.format(world.balance) }} 成就点</span>
          <template v-if="shop">
            <span class="wallet-chip" title="树木生长时获得">枝木 {{ world.resources.wood }}</span>
            <span class="wallet-chip" title="林地生长与专注时获得">花种 {{ world.resources.seeds }}</span>
            <span class="wallet-chip" title="完成休息后获得">晨露 {{ world.resources.dew }}</span>
          </template>
        </div>
      </div>
    </header>
    <template v-if="shop">
      <nav class="flex flex-wrap gap-2" aria-label="商品分类">
        <UiButton
          v-for="category in categories"
          :key="category.id"
          size="small"
          :variant="filter === category.id ? 'tonal' : 'text'"
          :aria-pressed="filter === category.id"
          @click="filter = category.id"
        >
          {{ category.label }}
        </UiButton>
      </nav>
      <div class="grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
        <button
          v-for="item in products"
          :key="item.id"
          class="collectible panel-surface text-left"
          @click="selectedItem = item"
        >
          <div class="art-stage">
            <span class="rarity">{{ item.rarity }}</span>
            <ForestArt
              :label="item.name"
              :kind="item.kind"
              :tree-style="item.category === 'species' ? item.style : 0"
              :accessory="item.category === 'accessory' ? item.id : ''"
              :landscape="item.category === 'landscape' ? item.id : ''"
            />
            <span v-if="item.owned" class="owned-stamp">已收藏</span>
          </div>
          <div class="p-5">
            <h3 class="text-lg font-semibold">{{ item.name }}</h3>
            <p class="mt-2 min-h-10 text-sm leading-5 text-muted-foreground">{{ item.description }}</p>
            <div class="mt-4 flex flex-wrap items-center justify-between gap-2 text-sm">
              <strong class="text-primary">✦ {{ item.price }}</strong>
              <span class="text-xs text-muted-foreground">{{ costText(item) || `Lv. ${item.level}` }}</span>
            </div>
          </div>
        </button>
      </div>
    </template>
    <template v-else>
      <div class="grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
        <button
          v-for="badge in world.achievements"
          :key="badge.id"
          class="badge-card panel-surface"
          :class="{ earned: badge.unlockedAt }"
          @click="selectedBadge = badge"
        >
          <div class="medallion" :class="{ legendary: badge.rarity === '传说' }">
            <span class="medallion-rays"></span>
            <span class="medallion-ring"></span>
            <span class="medallion-symbol">{{ symbols[badge.id] }}</span>
            <span class="medallion-ribbon">{{ badge.rarity }}</span>
          </div>
          <h3 class="mt-6 text-xl font-semibold">{{ badge.name }}</h3>
          <p class="mt-2 min-h-10 text-sm text-muted-foreground">{{ badge.description }}</p>
          <p v-if="badge.unlockedAt" class="mt-4 text-xs text-primary">{{ date(badge.unlockedAt) }} · 已点亮</p>
          <template v-else>
            <div class="badge-progress">
              <span :style="{ width: `${Math.min(100, (badge.progress / badge.target) * 100)}%` }"></span>
            </div>
            <p class="mt-2 text-xs text-muted-foreground">{{ badge.progress }} / {{ badge.target }}</p>
          </template>
        </button>
      </div>
      <div class="flex items-center justify-between pt-3">
        <h3 class="text-xl font-semibold">林间图鉴</h3>
        <span class="text-sm text-muted-foreground">{{ world.discoveries.length }} 种相遇</span>
      </div>
      <div class="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-6">
        <div
          v-for="tree in discovered"
          :key="`${tree.kind}:${tree.style}`"
          class="panel-surface overflow-hidden p-2 text-center"
        >
          <div class="h-32"><ForestArt :label="tree.name" :kind="tree.kind" :tree-style="tree.style" /></div>
          <p class="pb-3 text-sm">{{ tree.name }}</p>
        </div>
      </div>
    </template>
    <AppDialog
      :model-value="!!selectedItem"
      :title="selectedItem?.name || '商品详情'"
      :busy="buying"
      @update:model-value="
        value => {
          if (!value) selectedItem = undefined
        }
      "
    >
      <template v-if="selectedItem">
        <div class="mx-auto h-60 max-w-xs">
          <ForestArt
            :label="selectedItem.name"
            :kind="selectedItem.kind"
            :tree-style="selectedItem.category === 'species' ? selectedItem.style : 0"
            :accessory="selectedItem.category === 'accessory' ? selectedItem.id : ''"
            :landscape="selectedItem.category === 'landscape' ? selectedItem.id : ''"
          />
        </div>
        <p class="text-center leading-7">{{ selectedItem.description }}</p>
        <p class="mt-4 text-center text-sm text-muted-foreground">
          Lv. {{ selectedItem.level }} · {{ selectedItem.price }} 成就点
          <span v-if="costText(selectedItem)">· {{ costText(selectedItem) }}</span>
        </p>
        <p class="mt-3 text-center text-sm text-primary">
          {{
            selectedItem.category === 'landscape'
              ? '购买后会布置到合适的岛屿上。'
              : selectedItem.category === 'species'
                ? '新树可能长成这个样子，也可为同类树换上。'
                : '购买后，选一棵喜欢的树为它佩戴。'
          }}
        </p>
        <UiAlert v-if="purchaseError" class="mt-4" type="error">{{ purchaseError }}</UiAlert>
      </template>
      <template #actions>
        <UiButton v-if="selectedItem?.owned" @click="goForest">去森林看看</UiButton>
        <UiButton v-else :disabled="!selectedItem || !affordable(selectedItem)" :loading="buying" @click="buy">
          {{
            selectedItem && world.level < selectedItem.level
              ? `Lv. ${selectedItem.level} 开放`
              : selectedItem && !affordable(selectedItem)
                ? '还差一点材料或成就点'
                : '收入收藏'
          }}
        </UiButton>
      </template>
    </AppDialog>
    <AppDialog
      :model-value="!!selectedBadge"
      :title="selectedBadge?.name || '成就'"
      @update:model-value="
        value => {
          if (!value) selectedBadge = undefined
        }
      "
    >
      <div v-if="selectedBadge" class="py-4 text-center">
        <div class="medallion mx-auto" :class="{ legendary: selectedBadge.rarity === '传说' }">
          <span class="medallion-rays"></span>
          <span class="medallion-ring"></span>
          <span class="medallion-symbol">{{ symbols[selectedBadge.id] }}</span>
          <span class="medallion-ribbon">{{ selectedBadge.rarity }}</span>
        </div>
        <p class="mt-10 text-lg">{{ selectedBadge.description }}</p>
        <p class="mt-3 text-primary">✦ {{ selectedBadge.points }} 成就点</p>
        <p class="mt-2 text-sm text-muted-foreground">
          {{
            selectedBadge.unlockedAt
              ? `${date(selectedBadge.unlockedAt)} 点亮`
              : `${selectedBadge.progress} / ${selectedBadge.target}`
          }}
        </p>
      </div>
      <template #actions>
        <UiButton
          v-if="selectedBadge?.treeId"
          @click="emit('locate', selectedBadge.treeId); selectedBadge = undefined"
        >
          去看看那棵树
        </UiButton>
        <UiButton variant="text" @click="selectedBadge = undefined">收好</UiButton>
      </template>
    </AppDialog>
  </section>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import UiButton from '@/shared/ui/button/Button.vue'
import UiAlert from '@/shared/ui/feedback/Alert.vue'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import type { ForestAchievement, ForestShopItem } from '@/types/forest'
import { number } from '@/features/usage-journal/presentation'
import { forestApi, styles, useForest } from '@/features/usage-journal/forest/store'
import ForestArt from '@/features/usage-journal/forest/ForestArt.vue'
const props = defineProps<{ shop: boolean; timeZone: string }>()
const emit = defineEmits<{ locate: [id: number]; forest: [] }>()
const forest = useForest()
const { world } = storeToRefs(forest)
const selectedItem = ref<ForestShopItem>()
const selectedBadge = ref<ForestAchievement>()
const buying = ref(false),
  purchaseError = ref(''),
  filter = ref('all')
const categories = [
  { id: 'all', label: '全部珍藏' },
  { id: 'species', label: '树种' },
  { id: 'accessory', label: '饰品' },
  { id: 'landscape', label: '岛屿装饰' }
]
const products = computed(
  () => world.value?.ornaments.filter(item => filter.value === 'all' || item.category === filter.value) || []
)
const unlocked = computed(() => world.value?.achievements.filter(item => item.unlockedAt) || [])
const discovered = computed(
  () => world.value?.discoveries.map(item => ({ ...item, name: styles[item.kind][item.style] })) || []
)
const symbols: Record<string, string> = {
  first: '❧',
  grove: '✿',
  island: '♜',
  archipelago: '✦',
  focus: '☽',
  rest: '☀',
  'rest-grove': '♧'
}
const date = (at: number) => new Date(at).toLocaleDateString('zh-CN', { timeZone: props.timeZone })
const costText = (item: ForestShopItem) =>
  [
    item.cost.wood && `${item.cost.wood} 枝木`,
    item.cost.seeds && `${item.cost.seeds} 花种`,
    item.cost.dew && `${item.cost.dew} 晨露`
  ]
    .filter(Boolean)
    .join(' · ')
function affordable(item: ForestShopItem) {
  const w = world.value
  return (
    !!w &&
    w.level >= item.level &&
    w.balance >= item.price &&
    w.resources.wood >= item.cost.wood &&
    w.resources.seeds >= item.cost.seeds &&
    w.resources.dew >= item.cost.dew
  )
}
watch(selectedItem, () => {
  purchaseError.value = ''
})
async function buy() {
  if (!selectedItem.value || buying.value) return
  buying.value = true
  purchaseError.value = ''
  try {
    const id = selectedItem.value.id
    await forestApi.redeem(id)
    selectedItem.value = { ...selectedItem.value, owned: true }
    if (world.value) {
      world.value = {
        ...world.value,
        ornaments: world.value.ornaments.map(item => (item.id === id ? { ...item, owned: true } : item))
      }
    }
    try {
      await forest.load()
      selectedItem.value = world.value?.ornaments.find(item => item.id === id)
    } catch (cause) {
      purchaseError.value = `已收入收藏，刷新失败：${cause instanceof Error ? cause.message : String(cause)}`
    }
  } catch (cause) {
    purchaseError.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    buying.value = false
  }
}
function goForest() {
  selectedItem.value = undefined
  emit('forest')
}
</script>
<style scoped>
.collection-hero {
  background:
    radial-gradient(ellipse at 85% 10%, color-mix(in srgb, var(--primary) 20%, transparent), transparent 65%),
    var(--surface);
}
.wallet-chip {
  padding: 0.5rem 0.8rem;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--surface-bright);
  font-size: 0.8rem;
}
.collectible {
  overflow: hidden;
  cursor: pointer;
  transition: transform 180ms;
}
.collectible:hover {
  transform: translateY(-4px);
}
.art-stage {
  height: 210px;
  position: relative;
  background: radial-gradient(ellipse at 50% 75%, color-mix(in srgb, var(--success) 17%, transparent), transparent 65%);
}
.rarity {
  position: absolute;
  top: 1rem;
  left: 1rem;
  color: var(--primary);
  font-size: 0.7rem;
  letter-spacing: 0.2em;
}
.owned-stamp {
  position: absolute;
  bottom: 1rem;
  right: 1rem;
  border: 1px solid var(--primary);
  color: var(--primary);
  padding: 0.2rem 0.6rem;
  border-radius: 999px;
  font-size: 0.7rem;
}
.badge-card {
  padding: 2.5rem 1.5rem 1.5rem;
  text-align: center;
  cursor: pointer;
}
.badge-card:not(.earned) .medallion {
  filter: grayscale(1);
  opacity: 0.45;
}
.badge-card.earned {
  background: radial-gradient(ellipse at 50% 20%, #d6b66722, transparent 60%), var(--surface);
}
.medallion {
  width: 116px;
  height: 116px;
  position: relative;
  display: grid;
  place-items: center;
  margin-inline: auto;
  color: #77511f;
}
.medallion-rays {
  position: absolute;
  inset: -7px;
  background: repeating-conic-gradient(from 0deg, #c9a662 0deg 8deg, #f3dfad 8deg 16deg);
  clip-path: polygon(
    50% 0%,
    61% 12%,
    76% 5%,
    80% 23%,
    96% 25%,
    90% 42%,
    100% 50%,
    88% 61%,
    95% 76%,
    77% 80%,
    75% 96%,
    58% 90%,
    50% 100%,
    39% 88%,
    24% 95%,
    20% 77%,
    4% 75%,
    10% 58%,
    0% 50%,
    12% 39%,
    5% 24%,
    23% 20%,
    25% 4%,
    42% 10%
  );
}
.medallion-ring {
  position: absolute;
  inset: 4px;
  border-radius: 50%;
  background: radial-gradient(circle at 35% 25%, #fff3c7, #e3bf75 60%, #a87735);
  border: 4px double #fff1bd;
  box-shadow:
    inset 0 0 0 4px #ae8442,
    0 4px 12px #77511f30;
}
.medallion-symbol {
  position: relative;
  font-size: 3rem;
  line-height: 1;
  text-shadow: 0 1px #fff1bd;
}
.medallion-ribbon {
  position: absolute;
  bottom: -8px;
  background: #467568;
  color: #fff4d2;
  border: 1px solid #c9a662;
  min-width: 95px;
  padding: 4px 12px;
  font-size: 0.7rem;
  letter-spacing: 0.2em;
  clip-path: polygon(0 0, 100% 0, 94% 50%, 100% 100%, 0 100%, 6% 50%);
}
.legendary .medallion-ring {
  background: radial-gradient(circle at 35% 25%, #edf5fa, #a5bdd0 55%, #647e9f);
  border-color: #e2ecff;
}
.legendary .medallion-ribbon {
  background: #4e426e;
}
.badge-progress {
  height: 4px;
  margin: 20px auto 0;
  max-width: 180px;
  border-radius: 99px;
  background: var(--muted);
  overflow: hidden;
}
.badge-progress span {
  display: block;
  height: 100%;
  background: var(--primary);
}
@media (prefers-reduced-motion: reduce) {
  .collectible {
    transition: none;
  }
  .collectible:hover {
    transform: none;
  }
}
</style>
