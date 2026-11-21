<template>
  <UiCard variant="outlined" rounded="lg">
    <div class="ui-card-title px-4 pt-4 text-base font-semibold flex items-center justify-between p-4 pb-2">
      <div class="flex items-center gap-2">
        <UiIcon color="primary">mdi-swap-horizontal</UiIcon>
        <span class="text-base font-bold">模型重定向 (可选)</span>
      </div>
    </div>

    <div class="p-4 pt-2">

      <!-- 现有映射列表 -->
      <div v-if="Object.keys(modelValue).length" class="mb-4">
        <UiList density="compact" class="bg-transparent">
          <UiListItem
            v-for="[source, targets] in Object.entries(modelValue)"
            :key="source"
            class="mb-2"
            rounded="lg"
            variant="tonal"
            color="surface-variant"
          >
            <template #prepend>
              <UiIcon size="small" color="primary">mdi-arrow-right</UiIcon>
            </template>
            <span class="block">
              <div class="flex flex-col gap-1">
                <div class="flex items-center gap-2">
                  <code class="text-xs font-bold">{{ source }}</code>
                  <UiIcon size="small" color="primary">mdi-arrow-right</UiIcon>
                  <UiBadge size="x-small" color="info" variant="tonal">{{ targets.length }} 个备选</UiBadge>
                </div>
                <draggable
                  :list="targets"
                  :item-key="getTargetKey"
                  handle=".mapping-target-drag-handle"
                  class="flex flex-wrap gap-1 mt-1"
                  :animation="150"
                  ghost-class="mapping-target-ghost"
                  @change="reorderTargets(source, targets)"
                >
                  <template #item="{ element: target }">
                    <UiBadge
                      size="x-small"
                      closable
                      :color="isChannelUnavailable(source, target) ? 'warning' : undefined"
                      @click:close.stop="removeTarget(source, target)"
                    >
                      <UiIcon size="12" class="mapping-target-drag-handle mr-1" title="拖拽调整优先级">
                        mdi-drag-vertical
                      </UiIcon>
                      <code class="text-xs">{{ target }}</code>
                      <span v-if="allowTargetChannel && getTargetChannelId(source, target)" class="text-xs ml-1">
                        @ {{ getTargetChannelLabel(source, target) }}
                      </span>
                    </UiBadge>
                  </template>
                </draggable>
              </div>
            </span>
            <template #append>
              <UiButton size="small" color="error" icon variant="text" title="删除所有映射" @click="removeSource(source)">
                <UiIcon size="small" color="error">mdi-delete</UiIcon>
              </UiButton>
            </template>
          </UiListItem>
        </UiList>
      </div>

      <!-- 添加新映射 -->
      <div v-if="allowWildcard" class="flex items-center gap-2 mb-3">
        <UiButton size="small" color="primary" variant="tonal" @click="localSource = '*'">
          使用 * 匹配任意源模型
        </UiButton>
      </div>
      <div class="flex items-center gap-2">
        <UiSelect
          v-if="strictSourceSelection"
          v-model="localSource"
          label="客户端图片模型"
          :items="sourceOptions"
          item-title="title"
          item-value="value"
          variant="outlined"
          density="comfortable"
          hide-details
          class="flex-1-1"
          clearable
        />
        <UiSelect
v-else v-model="localSource"
          searchable
          creatable
          label="源模型名"
          :items="sourceOptions"
          item-title="title"
          item-value="value"
          variant="outlined"
          density="comfortable"
          hide-details
          class="flex-1-1"
          placeholder="选择或输入源模型名"
          clearable
          @keyup.enter="addMapping"
        />
        <UiIcon color="primary">mdi-arrow-right</UiIcon>
        <UiSelect
          v-if="allowTargetChannel"
          v-model="localChannelId"
          label="目标渠道（可选）"
          :items="targetChannelOptions"
          item-title="title"
          item-value="value"
          variant="outlined"
          density="comfortable"
          hide-details
          class="flex-1-1"
          placeholder="沿用本渠道"
          clearable
          no-data-text="没有可用的同协议渠道"
        />
        <UiIcon v-if="allowTargetChannel" color="primary">mdi-arrow-right</UiIcon>
        <UiSelect
          v-if="strictTargetSelection && allowTargetChannel"
          v-model="localTarget"
          label="图片生成工具模型"
          :placeholder="targetPlaceholder"
          :items="availableTargetOptions"
          :loading="activeTargetStatus?.loading ?? false"
          variant="outlined"
          density="comfortable"
          hide-details
          class="flex-1-1"
          clearable
        />
        <UiSelect
v-else-if="strictTargetSelection" v-model="localTarget"
          searchable
          creatable
          label="图片生成工具模型"
          :placeholder="targetPlaceholder"
          :items="availableTargetOptions"
          item-title="title"
          item-value="value"
          variant="outlined"
          density="comfortable"
          hide-details
          class="flex-1-1"
          clearable
          :loading="activeTargetStatus?.loading ?? false"
          @keyup.enter="addMapping"
        />
        <UiSelect
v-else v-model="localTarget"
          searchable
          creatable
          label="目标模型名"
          :placeholder="targetPlaceholder"
          :items="availableTargetOptions"
          :loading="activeTargetStatus?.loading ?? false"
          variant="outlined"
          density="comfortable"
          hide-details
          class="flex-1-1"
          clearable
          :no-data-text="activeTargetStatus?.loading ? '正在获取模型…' : '暂无模型，可输入模型名'"
          @update:menu="onTargetMenuChange"
          @keyup.enter="addMapping"
        />
        <UiButton
          color="secondary"
          variant="elevated"
          :disabled="!isValid"
          @click="addMapping"
        >
          添加
        </UiButton>
      </div>
      <div v-if="targetError" class="text-destructive text-xs mt-2">
        {{ targetError }}
      </div>
      <div v-else-if="localSource && existingTargets.length" class="text-xs text-muted-foreground mt-2">
        当前源模型已配置 {{ existingTargets.length }} 个目标，可继续追加。
      </div>
    </div>
  </UiCard>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiBadge from '@/shared/ui/feedback/Badge.vue'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import UiList from '@/shared/ui/navigation/List.vue'
import UiListItem from '@/shared/ui/navigation/ListItem.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { ref, computed, watch } from 'vue'
import draggable from 'vuedraggable'

type Option = { title: string; value: string }

const props = defineProps<{
  /** 源模型 -> 目标模型列表 */
  modelValue: Record<string, string[]>
  /** 源模型 -> 目标模型 -> 目标渠道 ID（未登记即沿用本渠道） */
  channelValue: Record<string, Record<string, string>>
  sourceOptions: Option[]
  /** 目标模型候选：键为目标渠道 ID，空串表示本渠道 */
  targetOptionsByChannel: Record<string, Option[]>
  /** 目标渠道候选（同协议、可用） */
  targetChannelOptions: Option[]
  /** 目标模型候选的加载状态与错误：键同 targetOptionsByChannel */
  targetStatusByChannel: Record<string, { loading: boolean; error: string }>
  targetPlaceholder: string
  strictSourceSelection?: boolean
  strictTargetSelection?: boolean
  allowWildcard?: boolean
  allowTargetChannel?: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: Record<string, string[]>]
  'update:channelValue': [value: Record<string, Record<string, string>>]
  /** 目标模型下拉获得焦点；channelId 为空串表示本渠道 */
  'fetch-models': [channelId: string]
}>()

const localSource = ref('')
const localTarget = ref('')
const localChannelId = ref<string | null>(null)

const allowTargetChannel = computed(() => props.allowTargetChannel !== false)
const selectedChannelId = computed(() => localChannelId.value ?? '')

const channelNameById = computed(() => new Map(props.targetChannelOptions.map(option => [option.value, option.title])))

const activeTargetStatus = computed(() => props.targetStatusByChannel[selectedChannelId.value])

const targetError = computed(() => activeTargetStatus.value?.error ?? '')

const existingTargets = computed(() => {
  const src = getStringValue(localSource.value)
  if (!src) return []
  return props.modelValue[src] || []
})

const availableTargetOptions = computed(() => {
  const existing = new Set(existingTargets.value)
  const candidates = props.targetOptionsByChannel[selectedChannelId.value] ?? []
  return candidates.filter(opt => !existing.has(opt.value))
})

const isValid = computed(() => {
  const src = getStringValue(localSource.value)
  const tgt = getStringValue(localTarget.value)
  if (!src || !tgt) return false
  return !existingTargets.value.includes(tgt)
})

// 切换目标渠道后，已选目标模型属于旧渠道的候选，必须清空
watch(localChannelId, () => {
  localTarget.value = ''
})

const getStringValue = (val: unknown): string => {
  if (!val) return ''
  if (typeof val === 'string') return val
  if (typeof val === 'object' && 'value' in val) {
    const value = (val as { value?: unknown }).value
    return typeof value === 'string' ? value : ''
  }
  return ''
}

const getTargetKey = (target: string): string => target

const getTargetChannelId = (source: string, target: string): string => props.channelValue[source]?.[target] ?? ''

// 目标渠道不在候选列表中（已禁用、熔断 Open、被删除或不同协议）时视为不可用
const isChannelUnavailable = (source: string, target: string): boolean => {
  const channelId = getTargetChannelId(source, target)
  return !!channelId && !channelNameById.value.has(channelId)
}

const getTargetChannelLabel = (source: string, target: string): string => {
  const channelId = getTargetChannelId(source, target)
  if (!channelId) return ''
  return channelNameById.value.get(channelId) ?? '不可用渠道'
}

const onTargetMenuChange = (open: boolean) => {
  if (open) emit('fetch-models', selectedChannelId.value)
}

/** 返回登记了目标渠道的副本：channelId 为空时移除该目标的登记，并剪掉空的源条目。 */
const withTargetChannel = (
  channels: Record<string, Record<string, string>>,
  source: string,
  target: string,
  channelId: string
): Record<string, Record<string, string>> => {
  const updated = { ...channels }
  const sourceChannels = { ...(updated[source] || {}) }
  if (channelId) {
    sourceChannels[target] = channelId
  } else {
    delete sourceChannels[target]
  }
  if (Object.keys(sourceChannels).length > 0) {
    updated[source] = sourceChannels
  } else {
    delete updated[source]
  }
  return updated
}

const addMapping = () => {
  const src = getStringValue(localSource.value)
  const tgt = getStringValue(localTarget.value)
  if (!src || !tgt) return
  if (existingTargets.value.includes(tgt)) return

  const updated = { ...props.modelValue }
  if (!updated[src]) {
    updated[src] = []
  }
  updated[src] = [...updated[src], tgt]
  emit('update:modelValue', updated)
  if (selectedChannelId.value) {
    emit('update:channelValue', withTargetChannel(props.channelValue, src, tgt, selectedChannelId.value))
  }
  localTarget.value = ''
}

const removeTarget = (source: string, target: string) => {
  const updated = { ...props.modelValue }
  if (!updated[source]) return
  updated[source] = updated[source].filter(t => t !== target)
  if (updated[source].length === 0) {
    delete updated[source]
  }
  emit('update:modelValue', updated)
  emit('update:channelValue', withTargetChannel(props.channelValue, source, target, ''))
}

const reorderTargets = (source: string, targets: string[]) => {
  const updated = { ...props.modelValue, [source]: [...targets] }
  emit('update:modelValue', updated)
}

const removeSource = (source: string) => {
  const updated = { ...props.modelValue }
  delete updated[source]
  emit('update:modelValue', updated)
  const updatedChannels = { ...props.channelValue }
  delete updatedChannels[source]
  emit('update:channelValue', updatedChannels)
}
</script>

<style scoped>
@reference "@/styles/index.css";

.mapping-target-drag-handle {
  @apply cursor-grab;
}

.mapping-target-drag-handle:active {
  @apply cursor-grabbing;
}

.mapping-target-ghost {
  @apply opacity-45;
}
</style>
