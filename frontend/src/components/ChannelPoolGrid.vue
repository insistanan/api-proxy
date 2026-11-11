<template>
  <section class="pool-section">
    <header class="section-header">
      <div class="section-heading-mark">
        <v-icon size="20" color="primary">mdi-swap-horizontal</v-icon>
        <div class="text-subtitle-2 font-weight-bold">渠道分组</div>
        <v-chip size="x-small" color="primary" variant="tonal">{{ pools.length }}</v-chip>
        <slot name="heading"></slot>
      </div>
      <p class="pool-guide">按模型名称匹配分组，组内顺序即故障转移优先级；拖动手柄可调整顺序或移入其他已展开的分组。</p>
      <div class="pool-header-actions">
        <v-btn size="small" variant="text" :disabled="!poolViews.length" @click="toggleAllPools">
          {{ allPoolsCollapsed ? '展开全部' : '收起全部' }}
        </v-btn>
        <v-btn size="small" color="primary" variant="tonal" prepend-icon="mdi-plus" @click="openCreateDialog">
          新建分组
        </v-btn>
      </div>
    </header>

    <v-progress-linear v-if="loading" indeterminate color="primary" class="mb-3" />
    <div v-else class="pool-grid">
      <div v-for="(column, columnIndex) in poolColumns" :key="columnIndex" class="pool-column">
      <section
        v-for="{ pool, order } in column"
        :key="pool.id"
        class="pool-card"
        :class="{ 'is-default': pool.id === 'default', 'is-collapsed': collapsedPools.has(pool.id) }"
        :style="{ order }"
      >
        <FreeChannelPool v-if="pool.id === FREE_POOL_ID" :channel-type="channelType" :channels="channels" />
        <template v-else>
        <header class="pool-card-header">
          <button type="button" class="pool-card-toggle" :aria-expanded="!collapsedPools.has(pool.id)" :aria-controls="`pool-${channelType}-${pool.id}`" @click="togglePool(pool.id)">
            <v-icon size="18">{{ collapsedPools.has(pool.id) ? 'mdi-chevron-right' : 'mdi-chevron-down' }}</v-icon>
            <span class="min-width-0">
            <span class="pool-name" :title="pool.name">{{ pool.name }}</span>
            <span class="pool-rule" :title="pool.id === 'default' ? undefined : pool.modelMatcher">
              <template v-if="pool.id === 'default'">未匹配或命中分组不可用时使用</template>
              <template v-else>匹配：{{ pool.modelMatcher }}</template>
            </span>
            </span>
          </button>
          <div class="d-flex align-center ga-1">
            <span class="pool-channel-count">{{ pool.channels.length }} 个渠道</span>
            <v-btn v-if="pool.id !== 'default'" icon size="x-small" variant="text" title="编辑分组" :aria-label="`编辑分组 ${pool.name}`" @click="openEditDialog(pool)">
              <v-icon size="small">mdi-pencil</v-icon>
            </v-btn>
            <v-btn
              v-if="pool.id !== 'default'"
              icon
              size="x-small"
              color="error"
              variant="text"
              title="删除分组"
              :aria-label="`删除分组 ${pool.name}`"
              :disabled="hasAssignedChannels(pool.id)"
              @click="openDeletePoolDialog(pool)"
            >
              <v-icon size="small">mdi-delete</v-icon>
            </v-btn>
          </div>
        </header>
        <div :id="`pool-${channelType}-${pool.id}`">
        <draggable
          v-if="!collapsedPools.has(pool.id)"
          :list="pool.channels"
          item-key="id"
          handle=".drag-handle"
          :group="{ name: `channel-pools-${channelType}`, pull: true, put: true }"
          :animation="150"
          :scroll="true"
          :scroll-sensitivity="90"
          :scroll-speed="18"
          :empty-insert-threshold="48"
          ghost-class="ghost"
          chosen-class="chosen"
          drag-class="dragging"
          class="pool-channel-list"
          :force-fallback="true"
          @change="scheduleLayoutSave"
        >
          <template #item="{ element, index }">
            <div class="pool-drag-item">
              <slot
                name="channel"
                :channel="element"
                :index="index"
                :pool="pool"
                :move_to_top="() => moveChannel(pool, index, 0)"
                :move_to_bottom="() => moveChannel(pool, index, pool.channels.length - 1)"
              >
                <div class="pool-channel-row" @click="emit('edit', element)">
                  <span class="drag-handle"><v-icon size="small">mdi-drag-vertical</v-icon></span>
                  <span class="pool-index">{{ index + 1 }}</span>
                  <ChannelStatusBadge :status="element.status || 'active'" />
                  <div class="pool-channel-main">
                    <span class="font-weight-medium">{{ element.name }}</span>
                    <span class="text-caption text-medium-emphasis">{{ channelServiceLabel(props.channelType, element.serviceType) }}</span>
                  </div>
                </div>
              </slot>
            </div>
          </template>
        </draggable>
        <div v-if="!collapsedPools.has(pool.id) && pool.channels.length === 0" class="pool-empty">暂无渠道，可从其他分组拖入</div>
        </div>
        </template>
      </section>
      </div>
    </div>
  </section>

  <section class="vision-pool-section">
    <header class="vision-pool-header">
      <div class="d-flex align-center ga-2">
        <v-icon size="small" color="primary">mdi-image-search-outline</v-icon>
        <span class="text-subtitle-2">公用纯图片理解池</span>
        <v-chip size="x-small">{{ visionChannels.length }}</v-chip>
      </div>
      <span class="section-inline-label">图片理解渠道</span>
    </header>
    <div class="vision-channel-list">
      <div v-for="channel in visionChannels" :key="channel.index" class="vision-channel-row" @click="emit('edit', channel)">
        <v-icon size="small" color="primary">mdi-image-search-outline</v-icon>
        <button
          v-if="canToggleVisionStatus(channel)"
          type="button"
          class="vision-status-toggle"
          :title="channel.status === 'suspended' ? '点击恢复为活跃' : '点击切换为熔断'"
          :aria-label="channel.status === 'suspended' ? `将 ${channel.name} 恢复为活跃` : `将 ${channel.name} 切换为熔断`"
          @click.stop="emit('toggleVisionStatus', channel)"
        >
          <ChannelStatusBadge :status="channel.status || 'active'" />
        </button>
        <ChannelStatusBadge v-else :status="channel.status || 'active'" />
        <span class="font-weight-medium text-truncate">{{ channel.name }}</span>
        <span class="text-caption text-medium-emphasis">{{ channelServiceLabel(props.channelType, channel.serviceType) }}</span>
        <v-spacer />
        <v-chip size="x-small" variant="outlined">
          <v-icon start size="x-small">mdi-key</v-icon>{{ channel.apiKeys?.length || 0 }}
        </v-chip>
        <div class="vision-channel-actions" @click.stop>
          <slot name="vision-actions" :channel="channel" ></slot>
          <v-btn icon size="x-small" color="error" variant="text" title="删除渠道" @click="emit('delete', channel.index)">
            <v-icon size="small">mdi-delete</v-icon>
          </v-btn>
        </div>
      </div>
      <div v-if="visionChannels.length === 0" class="pool-empty">暂无公用图片理解渠道</div>
    </div>
  </section>

  <v-dialog v-model="poolDialog" max-width="460">
    <v-card rounded="lg">
      <v-card-title>{{ editingPool ? '编辑分组' : '新建分组' }}</v-card-title>
      <v-card-text class="pt-3">
        <v-text-field v-model="poolName" label="名称" variant="outlined" density="compact" autofocus />
        <v-combobox
          v-model="poolMatcher"
          label="模型名称包含"
          :items="matcherOptions"
          variant="outlined"
          density="compact"
          @keyup.enter="savePool"
        />
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn variant="text" @click="poolDialog = false">取消</v-btn>
        <v-btn color="primary" :loading="saving" :disabled="!poolName.trim() || !normalizedPoolMatcher" @click="savePool">保存</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <v-dialog v-model="deletePoolDialog" max-width="460">
    <v-card rounded="lg">
      <v-card-title>删除分组</v-card-title>
      <v-card-text>确定删除“{{ deletingPool?.name }}”吗？只有空分组可以删除。</v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn variant="text" @click="deletePoolDialog = false">取消</v-btn>
        <v-btn color="error" :loading="saving" @click="deletePool">删除</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import draggable from 'vuedraggable'
import { api, type Channel, type ChannelPool } from '../services/api'
import ChannelStatusBadge from './ChannelStatusBadge.vue'
import { channelServiceLabel } from '@/utils/imagesChannelProtocol'
import type { ApiTab } from '@/services/channel-api'
import { useChannelStore } from '@/stores/channel'
import { FREE_POOL_ID } from '@/services/free-channels'
import FreeChannelPool from './channel/FreeChannelPool.vue'

const props = defineProps<{
  channels: Channel[]
  channelType: ApiTab
}>()
const emit = defineEmits<{
  edit: [channel: Channel]
  delete: [channelId: number]
  error: [message: string]
  success: [message: string]
  refresh: []
  toggleVisionStatus: [channel: Channel]
}>()

const channelStore = useChannelStore()
const pools = computed(() => channelStore.channelPoolsMap[props.channelType])
const loading = ref(false)
const saving = ref(false)
const layoutSaving = ref(false)
const poolDialog = ref(false)
const deletePoolDialog = ref(false)
const editingPool = ref<ChannelPool | null>(null)
const deletingPool = ref<ChannelPool | null>(null)
const poolName = ref('')
const poolMatcher = ref<string | null>('')
type PoolView = ChannelPool & { channels: Channel[] }
const poolViews = ref<PoolView[]>([])
const collapsedPools = ref(new Set<string>())
const collapsiblePools = computed(() => poolViews.value.filter(pool => pool.id !== FREE_POOL_ID))
const allPoolsCollapsed = computed(() => collapsiblePools.value.length > 0 && collapsiblePools.value.every(pool => collapsedPools.value.has(pool.id)))
const toggleAllPools = () => {
  collapsedPools.value = allPoolsCollapsed.value ? new Set() : new Set(collapsiblePools.value.map(pool => pool.id))
}
const togglePool = (id: string) => {
  const collapsed = new Set(collapsedPools.value)
  if (collapsed.has(id)) collapsed.delete(id)
  else collapsed.add(id)
  collapsedPools.value = collapsed
}
let layoutSaveTimer: ReturnType<typeof setTimeout> | null = null
let layoutSavePending = false

const errorMessage = (error: unknown) => error instanceof Error ? error.message : String(error)
const normalizedPoolMatcher = computed(() => String(poolMatcher.value ?? '').trim())

const loadPools = async () => {
  loading.value = true
  try {
    await channelStore.refreshChannelPools(props.channelType)
  } catch (error) {
    emit('error', `加载渠道分组失败：${errorMessage(error)}`)
  } finally {
    loading.value = false
  }
}

onMounted(() => void loadPools())
onBeforeUnmount(() => {
  if (layoutSaveTimer) clearTimeout(layoutSaveTimer)
})
watch(() => props.channelType, () => void loadPools())

const syncPoolViews = () => {
  poolViews.value = pools.value.map(pool => ({
    ...pool,
    channels: props.channels
      .filter(channel => !channel.excludeFromConversation && (channel.poolId || 'default') === pool.id && ['active', 'suspended'].includes(channel.status || 'active'))
      .sort((left, right) => (left.priority || left.index + 1) - (right.priority || right.index + 1))
  }))
}
// 分组只依赖成员、状态和顺序，延迟、密钥、映射等变化不需要重建整个拖拽列表。
watch([pools, () => props.channels.map(channel => [
  channel, channel.id, channel.index, channel.poolId,
  channel.status, channel.excludeFromConversation, channel.priority
])], syncPoolViews, { immediate: true })

const poolColumns = computed(() => {
  const orderedPools = poolViews.value.map((pool, order) => ({ pool, order }))
  return [
    orderedPools.filter(({ order }) => order % 2 === 0),
    orderedPools.filter(({ order }) => order % 2 === 1)
  ]
})
const visionChannels = computed(() => props.channels.filter(channel => channel.excludeFromConversation && channel.status !== 'deleted'))
const canToggleVisionStatus = (channel: Channel) => ['active', 'suspended'].includes(channel.status || 'active')
const hasAssignedChannels = (poolId: string) => props.channels.some(channel => channel.status !== 'deleted' && (channel.poolId || 'default') === poolId)

const matcherOptions = computed(() => {
  if (props.channelType === 'messages') return ['claude']
  if (props.channelType === 'responses') return ['gpt']
  if (props.channelType === 'gemini') return ['gemini']
  return []
})

const saveLayout = async () => {
  if (layoutSaving.value) {
    layoutSavePending = true
    return
  }
  layoutSaving.value = true
  let saved = false
  let failed = false
  try {
    const layout = poolViews.value.map(pool => ({
      poolId: pool.id,
      channelIds: pool.channels.map(channel => {
        if (!channel.id) throw new Error(`渠道 ${channel.name} 缺少稳定 ID`)
        return channel.id
      })
    }))
    await api.saveChannelPoolLayout(props.channelType, layout)
    saved = true
    emit('success', '分组故障转移顺序已保存')
  } catch (error) {
    failed = true
    syncPoolViews()
    emit('error', `保存分组布局失败：${errorMessage(error)}`)
  } finally {
    layoutSaving.value = false
    if (failed) {
      layoutSavePending = false
      if (layoutSaveTimer) {
        clearTimeout(layoutSaveTimer)
        layoutSaveTimer = null
      }
      emit('refresh')
    } else if (layoutSavePending) {
      layoutSavePending = false
      scheduleLayoutSave()
    } else if (saved) {
      emit('refresh')
    }
  }
}

const scheduleLayoutSave = () => {
  if (layoutSaveTimer) clearTimeout(layoutSaveTimer)
  layoutSaveTimer = setTimeout(() => {
    layoutSaveTimer = null
    void saveLayout()
  }, 0)
}

const moveChannel = (pool: PoolView, from: number, to: number) => {
  if (from === to) return
  const [channel] = pool.channels.splice(from, 1)
  pool.channels.splice(to, 0, channel)
  scheduleLayoutSave()
}

const openCreateDialog = () => {
  editingPool.value = null
  poolName.value = ''
  poolMatcher.value = ''
  poolDialog.value = true
}

const openEditDialog = (pool: ChannelPool) => {
  if (pool.id === 'default') return
  editingPool.value = pool
  poolName.value = pool.name
  poolMatcher.value = pool.modelMatcher
  poolDialog.value = true
}

const savePool = async () => {
  const name = poolName.value.trim()
  const modelMatcher = normalizedPoolMatcher.value
  if (!name || !modelMatcher) return
  saving.value = true
  try {
    if (editingPool.value) {
      await api.updateChannelPool(props.channelType, editingPool.value.id, { name, modelMatcher })
      emit('success', '分组配置已更新')
    } else {
      await api.createChannelPool(props.channelType, { name, modelMatcher })
      emit('success', '分组已创建')
    }
    poolDialog.value = false
    await loadPools()
  } catch (error) {
    emit('error', `保存分组失败：${errorMessage(error)}`)
  } finally {
    saving.value = false
  }
}

const openDeletePoolDialog = (pool: ChannelPool) => {
  deletingPool.value = pool
  deletePoolDialog.value = true
}

const deletePool = async () => {
  if (!deletingPool.value) return
  saving.value = true
  try {
    await api.deleteChannelPool(props.channelType, deletingPool.value.id)
    deletePoolDialog.value = false
    emit('success', '分组已删除')
    await loadPools()
  } catch (error) {
    emit('error', `删除分组失败：${errorMessage(error)}`)
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.pool-section { container-type: inline-size; container-name: channel-pools; }
.pool-guide { flex: 1 1 320px; min-width: 0; margin: 0; color: rgb(var(--v-theme-on-surface-variant)); font-size: 0.8125rem; line-height: 1.6; }
.pool-card-toggle { display: flex; align-items: center; gap: 8px; flex: 1; min-width: 0; min-height: 36px; text-align: start; }
.section-header, .vision-pool-header {
  display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 12px;
}
.section-heading-mark { display: flex; align-items: center; gap: 8px; }
.pool-header-actions { display: flex; align-items: center; gap: 8px; flex-shrink: 0; }
.section-inline-label {
  color: rgba(var(--v-theme-on-surface), 0.48);
  font-size: 11px;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}
.section-header { flex-wrap: wrap; }
.pool-grid {
  display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 18px; align-items: start;
}
.pool-column { display: flex; flex-direction: column; gap: 18px; min-width: 0; }
.pool-card {
  background: rgb(var(--v-theme-surface));
  border: 1px solid rgba(var(--v-theme-outline), 0.24);
  border-radius: var(--cut-md);
  min-width: 0;
  overflow: hidden;
  container-type: inline-size;
  transition: border-color 0.2s ease;
}
.pool-card:hover {
  border-color: rgba(var(--v-theme-primary), 0.4);
}

.pool-card-header {
  min-height: 58px;
  display: flex; align-items: center; justify-content: space-between; gap: 10px;
  padding: 11px 14px 10px 18px;
  border-bottom: 1px solid rgba(var(--v-theme-outline), 0.2);
  background: rgba(var(--v-theme-surface-variant), 0.25);
}
.pool-card.is-collapsed .pool-card-header { border-bottom-color: transparent; }
.pool-channel-count { flex-shrink: 0; padding-inline: 4px; color: rgb(var(--v-theme-on-surface-variant)); font-size: 0.75rem; white-space: nowrap; font-variant-numeric: tabular-nums; }
.pool-card.is-default .pool-card-header { background: color-mix(in srgb, rgb(var(--v-theme-primary)) 8%, rgb(var(--v-theme-surface))); }
.v-theme--dark .pool-card-header { background: rgba(255, 255, 255, 0.035); }
.pool-name {
  display: block;
  font-weight: 650; line-height: 1.4;
  letter-spacing: -0.01em;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.pool-rule {
  display: block;
  margin-top: 3px; font-size: 12px;
  font-family: var(--font-mono);
  color: rgb(var(--v-theme-on-surface-variant));
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.pool-card.is-default .pool-rule::before { content: '兜底  /  '; color: rgb(var(--v-theme-primary)); font-weight: 700; }
.pool-channel-list {
  padding: 10px; display: flex; flex-direction: column; gap: 8px; min-height: 72px;
}
.pool-drag-item { min-width: 0; }
.ghost { opacity: .45; background: rgba(var(--v-theme-primary), .12); border-radius: 5px; }
.chosen { cursor: grabbing; }
.dragging { opacity: .9; }
.drag-handle { cursor: grab; }
.pool-channel-row, .vision-channel-row {
  display: flex; align-items: center; gap: 8px; min-width: 0;
  padding: 8px 10px;
  border: 1px solid rgba(var(--v-theme-outline), 0.2);
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.16s ease, border-color 0.16s ease;
}
.pool-channel-row:hover, .vision-channel-row:hover {
  background: rgba(var(--v-theme-primary), 0.07);
  border-color: rgba(var(--v-theme-primary), 0.4);
}
.vision-channel-actions { display: flex; align-items: center; gap: 2px; flex: 0 0 auto; }
.pool-index {
  display: inline-grid; place-items: center;
  width: 22px; height: 22px; flex: 0 0 auto;
  background: rgb(var(--v-theme-primary));
  color: rgb(var(--v-theme-on-primary));
  font-size: 11px; font-weight: 800;
  font-family: var(--font-mono);
  border: none;
  border-radius: 6px;
}
.pool-channel-main {
  min-width: 0; flex: 1;
  display: flex; align-items: baseline; gap: 7px; overflow: hidden;
}
.pool-channel-main span:first-child {
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.pool-empty {
  display: grid; place-items: center; min-height: 48px;
  color: rgba(var(--v-theme-on-surface-variant), 0.8); font-size: 13px;
}
.vision-pool-section {
  margin-top: 22px; padding: 14px 0;
  border-top: 1px solid rgba(var(--v-theme-outline), 0.2);
}
.vision-channel-list {
  display: flex; flex-direction: column; gap: 6px; padding: 10px;
  border: 1px solid rgba(var(--v-theme-outline), 0.24);
  border-radius: var(--cut-md);
  background: rgb(var(--v-theme-surface));
}
.vision-status-toggle {
  display: inline-flex; padding: 0; border: 0; background: transparent; cursor: pointer;
}
.vision-status-toggle:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: 2px; }
.vision-status-toggle :deep(.badge-content) { cursor: pointer; }
.min-width-0 { min-width: 0; }
@container channel-pools (max-width: 1120px) {
  /* inline-size 容器不提供内容宽度，单列必须拉伸，避免缩放后卡片收缩到零宽。 */
  .pool-grid { display: flex; flex-direction: column; align-items: stretch; }
  .pool-column { display: contents; }
  .pool-card { width: 100%; }
}
@media (max-width: 900px) {
  .section-header, .vision-pool-header { align-items: flex-start; flex-direction: column; }
  /* 列布局下 flex-basis 会变成高度，说明文字不能继续占用桌面的 320px 基准。 */
  .pool-guide { flex: initial; }
  .pool-channel-row, .vision-channel-row { flex-wrap: wrap; }
}

@media (prefers-reduced-motion: reduce) {
  .pool-card,
  .pool-channel-row,
  .vision-channel-row { transition: none !important; }
}
</style>
