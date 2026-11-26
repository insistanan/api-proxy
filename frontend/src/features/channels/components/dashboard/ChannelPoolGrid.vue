<template>
  <section class="pool-section">
    <header class="section-header">
      <div class="section-heading-mark">
        <UiIcon size="20" color="primary">mdi-swap-horizontal</UiIcon>
        <div class="text-sm font-medium font-bold">渠道分组</div>
        <UiBadge size="x-small" color="primary" variant="tonal">{{ pools.length }}</UiBadge>
        <slot name="heading"></slot>
      </div>
      <p class="pool-guide">拖动渠道调整故障转移顺序，或移入其他已展开的分组。</p>
      <div class="pool-header-actions">
        <UiButton size="small" variant="text" :disabled="!poolViews.length" @click="toggleAllPools">
          {{ allPoolsCollapsed ? '展开全部' : '收起全部' }}
        </UiButton>
        <UiButton size="small" color="primary" variant="tonal" prepend-icon="mdi-plus" @click="openCreateDialog">
          新建分组
        </UiButton>
      </div>
    </header>

    <UiProgress v-if="loading" indeterminate color="primary" class="mb-3" />
    <div v-else class="pool-grid">
      <div v-for="(column, columnIndex) in poolColumns" :key="columnIndex" class="pool-column">
      <section
        v-for="{ pool, order } in column"
        :id="pool.id === FREE_POOL_ID ? freeChannelAnchor : undefined"
        :key="pool.id"
        :tabindex="pool.id === FREE_POOL_ID ? -1 : undefined"
        class="pool-card panel-surface"
        :class="{ 'is-default': pool.id === 'default', 'is-collapsed': collapsedPools.has(pool.id) }"
        :style="{ order }"
      >
        <FreeChannelPool v-if="pool.id === FREE_POOL_ID">
          <template #summary>
            <slot v-for="channel in pool.channels" :key="channel.id || channel.index" name="free-summary" :channel="channel"></slot>
          </template>
          <template #activity>
            <slot v-for="channel in pool.channels" :key="channel.id || channel.index" name="free-activity" :channel="channel"></slot>
          </template>
        </FreeChannelPool>
        <template v-else>
        <header class="pool-card-header">
          <button type="button" class="pool-card-toggle" :aria-expanded="!collapsedPools.has(pool.id)" :aria-controls="`pool-${channelType}-${pool.id}`" @click="togglePool(pool.id)">
            <UiIcon size="18">{{ collapsedPools.has(pool.id) ? 'mdi-chevron-right' : 'mdi-chevron-down' }}</UiIcon>
            <span class="pool-identity">
            <span class="pool-name" :title="pool.name">{{ pool.name }}</span>
            <span class="pool-rule" :title="pool.id === 'default' ? undefined : pool.modelMatcher">
              <template v-if="pool.id === 'default'">未匹配或命中分组不可用时使用</template>
              <template v-else>匹配：{{ pool.modelMatcher }}</template>
            </span>
            </span>
          </button>
          <div class="flex items-center gap-1">
            <span class="pool-channel-count">{{ pool.channels.length }} 个渠道</span>
            <UiButton icon="mdi-plus" size="x-small" variant="text" :title="`添加渠道到 ${pool.name}`" :aria-label="`添加渠道到 ${pool.name}`" @click="emit('addChannel', pool.id)" />
            <UiButton v-if="pool.id !== 'default'" icon size="x-small" variant="text" title="编辑分组" :aria-label="`编辑分组 ${pool.name}`" @click="openEditDialog(pool)">
              <UiIcon size="small">mdi-pencil</UiIcon>
            </UiButton>
            <UiButton
              v-if="pool.id !== 'default'"
              icon
              size="x-small"
              color="error"
              variant="text"
              title="删除分组"
              :aria-label="`删除分组 ${pool.name}`"
              @click="openDeletePoolDialog(pool)"
            >
              <UiIcon size="small">mdi-delete</UiIcon>
            </UiButton>
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
          ghost-class="pool-ghost"
          chosen-class="pool-chosen"
          drag-class="pool-dragging"
          class="pool-channel-list"
          :force-fallback="true"
          :fallback-on-body="true"
          @change="scheduleLayoutSave"
        >
          <template #item="{ element, index }">
            <div :id="element.id ? channelAnchorId(channelType, element.id) : undefined" class="pool-drag-item" tabindex="-1">
              <slot
                name="channel"
                :channel="element"
                :index="index"
                :pool="pool"
                :move_to_top="() => moveChannel(pool, index, 0)"
                :move_to_bottom="() => moveChannel(pool, index, pool.channels.length - 1)"
              >
                <div class="pool-channel-row" @click="emit('edit', element)">
                  <span class="drag-handle"><UiIcon size="small">mdi-drag-vertical</UiIcon></span>
                  <span class="pool-index">{{ index + 1 }}</span>
                  <ChannelStatusBadge :status="element.status || 'active'" />
                  <div class="pool-channel-main">
                    <span class="font-medium">{{ element.name }}</span>
                    <span class="text-xs text-muted-foreground">{{ channelServiceLabel(props.channelType, element.serviceType) }}</span>
                  </div>
                </div>
              </slot>
            </div>
          </template>
        </draggable>
        <EmptyState v-if="!collapsedPools.has(pool.id) && pool.channels.length === 0" variant="inline" icon="mdi-tray-arrow-down" title="暂无渠道" description="可从其他分组拖入" />
        </div>
        </template>
      </section>
      </div>
    </div>
  </section>

  <section v-if="isConversationProtocol(channelType)" class="vision-pool-section">
    <header class="vision-pool-header">
      <div class="flex items-center gap-2">
        <UiIcon size="small" color="primary">mdi-image-search-outline</UiIcon>
        <span class="text-sm font-medium">公用纯图片理解池</span>
        <UiBadge size="x-small">{{ visionChannels.length }}</UiBadge>
      </div>
      <span class="section-inline-label">图片理解渠道</span>
    </header>
    <div class="vision-channel-list">
      <div v-for="channel in visionChannels" :id="channel.id ? channelAnchorId(channel.kind ?? channelType, channel.id) : undefined" :key="`${channel.kind ?? channelType}-${channel.index}`" class="vision-channel-row" tabindex="-1" @click="emit('edit', channel)">
        <UiIcon size="small" color="primary">mdi-image-search-outline</UiIcon>
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
        <span class="font-medium truncate">{{ channel.name }}</span>
        <ChannelWebsiteLink :channel="channel" />
        <UiBadge v-if="channel.kind && channel.kind !== props.channelType" size="x-small" variant="text">
          {{ protocolLabel(channel.kind) }}
        </UiBadge>
        <span class="text-xs text-muted-foreground">{{ channelServiceLabel(channel.kind ?? props.channelType, channel.serviceType) }}</span>
        <span class="flex-1" ></span>
        <UiBadge size="x-small" variant="outlined">
          <UiIcon start size="x-small">mdi-key</UiIcon>{{ channel.apiKeys?.length || 0 }}
        </UiBadge>
        <div class="vision-channel-actions" @click.stop>
          <slot name="vision-actions" :channel="channel" ></slot>
          <UiButton icon size="x-small" color="error" variant="text" title="删除渠道" @click="emit('delete', channel.index, channel.kind)">
            <UiIcon size="small">mdi-delete</UiIcon>
          </UiButton>
        </div>
      </div>
      <EmptyState v-if="visionChannels.length === 0" variant="inline" icon="mdi-image-multiple-outline" title="暂无公用图片理解渠道" description="在渠道的编排设置中把它加入公用池后，会显示在这里。" />
    </div>
  </section>

  <AppDialog v-model="poolDialog" :title="editingPool ? '编辑分组' : '新建分组'" size="sm">
    <UiInput v-model="poolName" label="名称" variant="outlined" density="compact" autofocus />
    <UiSelect
v-model="poolMatcher" searchable
      creatable
      label="模型名称包含"
      :items="matcherOptions"
      variant="outlined"
      density="compact"
      @keyup.enter="savePool"
    />
    <template #actions>
      <UiButton variant="text" @click="poolDialog = false">取消</UiButton>
      <UiButton color="primary" :loading="saving" :disabled="!poolName.trim() || !normalizedPoolMatcher" @click="savePool">保存</UiButton>
    </template>
  </AppDialog>

  <ConfirmDialog
    v-model="deletePoolDialog"
    title="删除分组"
    :message="deletePoolMessage"
    confirm-text="删除"
    :loading="saving"
    @confirm="deletePool"
  />
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiProgress from '@/shared/ui/feedback/Progress.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import draggable from 'vuedraggable'
import { api, ApiError, type Channel, type ChannelPool, type ChannelDeletionImpact, type VisionChannel } from '@/api'
import { deletionImpactWarning } from '@/features/channels/utils/deletionImpact'
import ChannelStatusBadge from '@/features/channels/components/dashboard/ChannelStatusBadge.vue'
import ChannelWebsiteLink from '@/features/channels/components/dashboard/ChannelWebsiteLink.vue'
import { channelServiceLabel } from '@/features/channels/utils/imagesChannelProtocol'
import { API_TAB_LABELS, isConversationProtocol, type ApiTab } from '@/features/channels/services/channel-api'
import { useChannelStore } from '@/features/channels/stores/channel'
import { FREE_POOL_ID } from '@/features/channels/services/free-channels'
import { channelAnchorId } from '@/features/channels/utils/channelSearch'
import FreeChannelPool from '@/features/channels/components/dashboard/FreeChannelPool.vue'
import AppDialog from '@/shared/components/overlay/AppDialog.vue'
import EmptyState from '@/shared/components/feedback/EmptyState.vue'
import ConfirmDialog from '@/shared/components/overlay/ConfirmDialog.vue'

const props = defineProps<{
  channels: Channel[]
  channelType: ApiTab
  // 四个会话协议共享的公用纯图片理解池；渠道自带所属协议。
  sharedVisionChannels?: VisionChannel[]
}>()
const emit = defineEmits<{
  addChannel: [poolId: string]
  edit: [channel: Channel]
  delete: [channelId: number, kind?: ApiTab]
  error: [message: string]
  success: [message: string]
  refresh: []
  toggleVisionStatus: [channel: Channel]
}>()

const channelStore = useChannelStore()
const pools = computed(() => channelStore.channelPoolsMap[props.channelType])
const freeChannelAnchor = computed(() => {
  const id = props.channels.find(channel => channel.builtinProvider)?.id
  return id ? channelAnchorId(props.channelType, id) : undefined
})
const loading = ref(false)
const saving = ref(false)
const layoutSaving = ref(false)
const poolDialog = ref(false)
const deletePoolDialog = ref(false)
const editingPool = ref<ChannelPool | null>(null)
const deletingPool = ref<ChannelPool | null>(null)
const deletingPoolKind = ref<ApiTab>(props.channelType)
const deletingPoolImpact = ref<ChannelDeletionImpact | null>(null)
const preparingDelete = ref(false)
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

let poolLoadRequest = 0
let poolLoad: Promise<void> | undefined
const loadPools = () => {
  const request = ++poolLoadRequest
  const protocol = props.channelType
  loading.value = true
  poolLoad = (async () => {
    try {
      await channelStore.refreshChannelPools(protocol)
    } catch (error) {
      if (request === poolLoadRequest) emit('error', `加载渠道分组失败：${errorMessage(error)}`)
    } finally {
      if (request === poolLoadRequest) loading.value = false
    }
  })()
  return poolLoad
}

async function revealChannel(channel: Channel) {
  await poolLoad
  const collapsed = new Set(collapsedPools.value)
  collapsed.delete(channel.poolId || 'default')
  collapsedPools.value = collapsed
  await nextTick()
}

defineExpose({ revealChannel })

onMounted(() => void loadPools())
onBeforeUnmount(() => {
  poolLoadRequest++
  if (layoutSaveTimer) clearTimeout(layoutSaveTimer)
})
watch(() => props.channelType, () => { deletePoolDialog.value = false; void loadPools() })

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
const visionChannels = computed(() => {
  const source = props.sharedVisionChannels ?? props.channels.filter(channel => channel.excludeFromConversation)
  return source.filter(channel => channel.status !== 'deleted')
})
const protocolLabel = (kind?: ApiTab) => (kind ? API_TAB_LABELS[kind] : '')
const canToggleVisionStatus = (channel: Channel) => ['active', 'suspended'].includes(channel.status || 'active')
const deletePoolMessage = computed(() => {
  const impact = deletingPoolImpact.value
  if (!impact) return ''
  return `确定删除分组“${impact.name}”吗？该分组下的全部 ${impact.channelCount} 个渠道也将一并删除（含暂停、备用、弃用及图片理解渠道），此操作不可撤销。${deletionImpactWarning(impact)}`
})

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
    if (!(error instanceof ApiError && error.committed)) syncPoolViews()
    else {
      emit('error', `${errorMessage(error)}（布局已写入，请刷新核对，请勿重复拖动提交）`)
      await loadPools()
      return
    }
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
  if (saving.value) return
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
    if (error instanceof ApiError && error.committed) {
      poolDialog.value = false
      emit('error', `${errorMessage(error)}（分组已写入，请刷新核对，请勿重复创建）`)
      await loadPools()
      return
    }
    emit('error', `保存分组失败：${errorMessage(error)}`)
  } finally {
    saving.value = false
  }
}

const openDeletePoolDialog = async (pool: ChannelPool) => {
  if (preparingDelete.value || saving.value || deletePoolDialog.value) return
  const kind = props.channelType
  preparingDelete.value = true
  try {
    const impact = await api.getChannelPoolDeletionImpact(kind, pool.id)
    if (kind !== props.channelType) return
    deletingPool.value = pool
    deletingPoolKind.value = kind
    deletingPoolImpact.value = impact
    deletePoolDialog.value = true
  } catch (error) {
    emit('error', `读取分组删除影响失败：${errorMessage(error)}`)
  } finally {
    preparingDelete.value = false
  }
}

const deletePool = async () => {
  if (!deletingPool.value || saving.value) return
  saving.value = true
  try {
    await api.deleteChannelPool(deletingPoolKind.value, deletingPool.value.id)
    deletePoolDialog.value = false
    emit('success', '分组及其渠道已删除')
    emit('refresh')
    await loadPools()
  } catch (error) {
    if (error instanceof ApiError && error.committed) {
      deletePoolDialog.value = false
      emit('refresh')
      await loadPools()
      emit('error', `${errorMessage(error)}（分组及渠道删除已写入，请刷新核对，请勿重复提交）`)
      return
    }
    emit('error', `删除分组失败：${errorMessage(error)}`)
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
@reference "@/styles/index.css";

.pool-section { @apply [container-type:inline-size] [container-name:channel-pools]; }
.pool-guide { @apply [flex:1_1_320px] min-w-0 m-0 text-muted-foreground [font-size:0.8125rem] [line-height:1.6]; }
.pool-card-toggle { @apply flex items-center gap-2 [flex:1] min-w-0 min-h-9 text-start; }
.section-header, .vision-pool-header {
  @apply flex items-center justify-between gap-4 mb-3;
}
.section-heading-mark { @apply flex items-center gap-2; }
.pool-header-actions { @apply flex items-center gap-2 shrink-0; }
.section-inline-label {
  @apply [color:color-mix(in_srgb,_var(--foreground)_48%,_transparent)] [font-size:11px] [letter-spacing:0.04em] [text-transform:uppercase];
}
.section-header { @apply flex-wrap; }
.pool-grid {
  @apply grid [grid-template-columns:repeat(2,_minmax(0,_1fr))] gap-4.5 items-start;
}
.pool-column { @apply flex flex-col gap-4.5 min-w-0; }
.pool-card {
  @apply min-w-0 overflow-hidden [container-type:inline-size] [transition:border-color_0.2s_ease];
  /* 分组只留轻染色，防止外壳与渠道卡片叠成近乎不透明，并允许卡片采样页面背景。 */
  background-color: var(--group-card);
  backdrop-filter: none;
}
.pool-card :deep(.channel-row) { background-color: var(--card); backdrop-filter: blur(var(--surface-blur)) saturate(var(--surface-saturation)); }
.pool-card:hover {
  @apply [border-color:color-mix(in_srgb,_var(--primary)_40%,_transparent)];
}

.pool-card-header {
  @apply min-h-10.5 flex items-center justify-between gap-2.5 [padding:4px_9px_4px_12px] [border-bottom:1px_solid_color-mix(in_srgb,_var(--outline)_20%,_transparent)] [background:color-mix(in_srgb,_var(--surface-variant)_25%,_transparent)];
}
.pool-card.is-collapsed .pool-card-header { @apply [border-bottom-color:transparent]; }
.pool-channel-count { @apply shrink-0 px-1 text-muted-foreground [font-size:0.75rem] whitespace-nowrap tabular-nums; }
.pool-card.is-default .pool-card-header { @apply [background:color-mix(in_srgb,_var(--primary)_8%,_var(--surface))]; }
[data-theme-mode='dark'] .pool-card-header { @apply [background:color-mix(in_srgb,_var(--foreground)_3.5%,_transparent)]; }
.pool-identity { @apply flex items-baseline gap-2 min-w-0; }
.pool-name {
  @apply shrink-0 [max-width:65%] [font-size:0.875rem] [font-weight:650] [line-height:1.4] [letter-spacing:-0.01em] overflow-hidden text-ellipsis whitespace-nowrap;
}
.pool-rule {
  @apply block [font-size:0.6875rem] font-mono text-muted-foreground overflow-hidden text-ellipsis whitespace-nowrap;
}
.pool-card.is-default .pool-rule::before { @apply [content:'兜底_/_'] text-primary font-bold; }
.pool-channel-list {
  @apply p-2.5 flex flex-col gap-2 min-h-12;
}
.pool-drag-item { @apply min-w-0; }

/* 分组是凹下去的卡槽，酒红标题压在卡槽上沿。 */
[data-surface='antique'] .pool-card {
  border-color: color-mix(in srgb, var(--border) 74%, var(--group-card));
  border-radius: 15px 15px 8px 8px;
  corner-shape: scoop scoop bevel bevel;
  box-shadow: inset 0 2px 8px #17110d50, 0 5px 12px #15100e35;
}
[data-surface='antique'] .pool-card:hover { border-color: var(--border); }
[data-surface='antique'] .pool-card .pool-card-header {
  --foreground: #f3e7d2;
  --muted-foreground: #dfcbb0;
  --primary: #ebca96;
  --primary-foreground: #38252c;
  --ring: var(--primary);
  --destructive: #f1a199;
  --muted: #ffffff19;
  color: var(--foreground);
  padding-inline: 16px;
  border-bottom-color: #b48f5680;
  background-color: var(--antique-band, #49373a);
  background-image: var(--paper-grain);
}
[data-surface='antique'] .pool-name { color: var(--primary); letter-spacing: .035em; }
[data-surface='antique'] .pool-card-toggle:focus-visible { outline-offset: -2px; }
[data-surface='antique'] .pool-channel-list { padding: 12px; gap: 10px; }
[data-surface='antique'] .vision-channel-list { background-color: var(--group-card); }
[data-surface='antique'] .pool-card :deep(.channel-row) { backdrop-filter: none; }
[data-surface='antique'] :is(.pool-channel-row, .vision-channel-row) {
  background-color: var(--card);
  border-radius: 6px;
  corner-shape: bevel;
}
[data-surface='antique'] :is(.pool-channel-row, .vision-channel-row):hover {
  border-color: var(--primary);
}
[data-surface='antique'] .pool-index {
  border-radius: 5px;
  corner-shape: bevel;
  background-color: var(--antique-band, #49373a);
  color: #f3dfbc;
}
/* 拖拽三态：拖起 / 落位占位 / 跟随指针。只改颜色与透明度，避免拖拽过程触发重排。 */
.pool-drag-item.pool-chosen { @apply cursor-grabbing; }
.pool-drag-item.pool-chosen :deep(.channel-row),
.pool-drag-item.pool-chosen .pool-channel-row {
  @apply [border-color:color-mix(in_srgb,_var(--primary)_55%,_transparent)] [background:color-mix(in_srgb,_var(--primary)_6%,_transparent)];
}
.pool-drag-item.pool-ghost { @apply opacity-45; }
.pool-drag-item.pool-ghost :deep(.channel-row),
.pool-drag-item.pool-ghost .pool-channel-row {
  @apply [border:1px_dashed_var(--primary)] [background:color-mix(in_srgb,_var(--primary)_4%,_transparent)];
}
.pool-drag-item.pool-dragging { @apply cursor-grabbing; }
.pool-drag-item.pool-dragging :deep(.channel-row),
.pool-drag-item.pool-dragging .pool-channel-row {
  @apply border-primary shadow-popover;
}
.drag-handle { @apply cursor-grab; }
.drag-handle:active { @apply cursor-grabbing; }
.pool-channel-row, .vision-channel-row {
  @apply flex items-center gap-2 min-w-0 [padding:8px_10px] [border:1px_solid_color-mix(in_srgb,_var(--outline)_20%,_transparent)] rounded-control cursor-pointer [transition:background_0.16s_ease,_border-color_0.16s_ease];
}
.pool-channel-row:hover, .vision-channel-row:hover {
  @apply [background:color-mix(in_srgb,_var(--primary)_7%,_transparent)] [border-color:color-mix(in_srgb,_var(--primary)_40%,_transparent)];
}
.vision-channel-actions { @apply flex items-center gap-0.5 [flex:0_0_auto]; }
.pool-index {
  @apply inline-grid [place-items:center] w-5.5 h-5.5 [flex:0_0_auto] bg-primary text-primary-foreground [font-size:11px] font-extrabold font-mono [border:none] rounded-badge;
}
.pool-channel-main {
  @apply min-w-0 [flex:1] flex items-baseline [gap:7px] overflow-hidden;
}
.pool-channel-main span:first-child {
  @apply overflow-hidden text-ellipsis whitespace-nowrap;
}
.vision-pool-section {
  @apply mt-5.5 [padding:14px_0] [border-top:1px_solid_color-mix(in_srgb,_var(--outline)_20%,_transparent)];
}
.vision-channel-list {
  @apply flex flex-col gap-1.5 p-2.5 [border:1px_solid_color-mix(in_srgb,_var(--outline)_24%,_transparent)] rounded-control [background:var(--surface)];
}
.vision-status-toggle {
  @apply inline-flex p-0 border-0 [background:transparent] cursor-pointer;
}
.vision-status-toggle:focus-visible { @apply [outline:2px_solid_var(--primary)] [outline-offset:2px]; }
.vision-status-toggle :deep(.badge-content) { @apply cursor-pointer; }
@container channel-pools (max-width: 1120px) {
  /* inline-size 容器不提供内容宽度，单列必须拉伸，避免缩放后卡片收缩到零宽。 */
  .pool-grid { @apply flex flex-col items-stretch; }
  .pool-column { @apply [display:contents]; }
  .pool-card { @apply w-full; }
}
@media (max-width: 900px) {
  .section-header, .vision-pool-header { @apply items-start flex-col; }
  /* 列布局下 flex-basis 会变成高度，说明文字不能继续占用桌面的 320px 基准。 */
  .pool-guide { @apply [flex:initial]; }
  .pool-channel-row, .vision-channel-row { @apply flex-wrap; }
}

@media (prefers-reduced-motion: reduce) {
  .pool-card,
  .pool-channel-row,
  .vision-channel-row { @apply [transition:none]!; }
}
</style>
