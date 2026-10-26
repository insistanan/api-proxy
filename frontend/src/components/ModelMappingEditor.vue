<template>
  <v-card variant="outlined" rounded="lg">
    <v-card-title class="d-flex align-center justify-space-between pa-4 pb-2">
      <div class="d-flex align-center ga-2">
        <v-icon color="primary">mdi-swap-horizontal</v-icon>
        <span class="text-body-1 font-weight-bold">模型重定向 (可选)</span>
      </div>
      <v-chip size="small" color="secondary" variant="tonal"> 自动转换模型名称 </v-chip>
    </v-card-title>

    <v-card-text class="pt-2">
      <div class="text-body-2 text-medium-emphasis mb-4">
        {{ hint }}
        <div class="text-caption mt-1">目标模型按列表顺序尝试；拖动左侧手柄可调整优先级。</div>
        <div v-if="allowTargetChannel" class="text-caption mt-1">
          可为每个目标指定目标渠道（同协议）：命中后改走该渠道的指定模型，不再经过本渠道；未指定则走本渠道。
        </div>
      </div>

      <!-- 现有映射列表 -->
      <div v-if="Object.keys(modelValue).length" class="mb-4">
        <v-list density="compact" class="bg-transparent">
          <v-list-item
            v-for="[source, targets] in Object.entries(modelValue)"
            :key="source"
            class="mb-2"
            rounded="lg"
            variant="tonal"
            color="surface-variant"
          >
            <template #prepend>
              <v-icon size="small" color="primary">mdi-arrow-right</v-icon>
            </template>
            <v-list-item-title>
              <div class="d-flex flex-column ga-1">
                <div class="d-flex align-center ga-2">
                  <code class="text-caption font-weight-bold">{{ source }}</code>
                  <v-icon size="small" color="primary">mdi-arrow-right</v-icon>
                  <v-chip size="x-small" color="info" variant="tonal">{{ targets.length }} 个备选</v-chip>
                </div>
                <draggable
                  :list="targets"
                  :item-key="getTargetKey"
                  handle=".mapping-target-drag-handle"
                  class="d-flex flex-wrap ga-1 mt-1"
                  :animation="150"
                  ghost-class="mapping-target-ghost"
                  @change="reorderTargets(source, targets)"
                >
                  <template #item="{ element: target }">
                    <v-chip
                      size="x-small"
                      closable
                      :color="isChannelUnavailable(source, target) ? 'warning' : undefined"
                      @click:close.stop="removeTarget(source, target)"
                    >
                      <v-icon size="12" class="mapping-target-drag-handle mr-1" title="拖拽调整优先级">
                        mdi-drag-vertical
                      </v-icon>
                      <code class="text-caption">{{ target }}</code>
                      <span v-if="allowTargetChannel && getTargetChannelId(source, target)" class="text-caption ml-1">
                        @ {{ getTargetChannelLabel(source, target) }}
                      </span>
                    </v-chip>
                  </template>
                </draggable>
              </div>
            </v-list-item-title>
            <template #append>
              <v-btn size="small" color="error" icon variant="text" title="删除所有映射" @click="removeSource(source)">
                <v-icon size="small" color="error">mdi-delete</v-icon>
              </v-btn>
            </template>
          </v-list-item>
        </v-list>
      </div>

      <!-- 添加新映射 -->
      <div v-if="allowWildcard" class="d-flex align-center ga-2 mb-3">
        <v-btn size="small" color="primary" variant="tonal" @click="localSource = '*'">
          使用 * 匹配任意源模型
        </v-btn>
        <span class="text-caption text-medium-emphasis">选择后，任何请求模型都会映射到右侧目标模型。</span>
      </div>
      <div class="d-flex align-center ga-2">
        <v-select
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
        <v-combobox
          v-else
          v-model="localSource"
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
        <v-icon color="primary">mdi-arrow-right</v-icon>
        <v-select
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
        <v-icon v-if="allowTargetChannel" color="primary">mdi-arrow-right</v-icon>
        <v-select
          v-if="strictTargetSelection && allowTargetChannel"
          v-model="localTarget"
          label="图片生成工具模型"
          :placeholder="targetPlaceholder"
          :items="availableTargetOptions"
          variant="outlined"
          density="comfortable"
          hide-details
          class="flex-1-1"
          clearable
        />
        <v-combobox
          v-else-if="strictTargetSelection"
          v-model="localTarget"
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
          @keyup.enter="addMapping"
        />
        <v-combobox
          v-else
          v-model="localTarget"
          label="目标模型名"
          :placeholder="targetPlaceholder"
          :items="availableTargetOptions"
          :loading="activeTargetStatus?.loading ?? false"
          variant="outlined"
          density="comfortable"
          hide-details
          class="flex-1-1"
          clearable
          @focus="fetchModels"
          @keyup.enter="addMapping"
        />
        <v-btn
          color="secondary"
          variant="elevated"
          :disabled="!isValid"
          @click="addMapping"
        >
          添加
        </v-btn>
      </div>
      <div v-if="targetError" class="text-error text-caption mt-2">
        {{ targetError }}
      </div>
      <div v-else-if="localSource && existingTargets.length" class="text-caption text-medium-emphasis mt-2">
        当前源模型已配置 {{ existingTargets.length }} 个目标，可继续追加。
      </div>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import draggable from 'vuedraggable'

type Option = { title: string; value: string }

const props = defineProps<{
  /** 源模型 -> 目标模型列表 */
  modelValue: Record<string, string[]>
  /** 源模型 -> 目标模型 -> 目标渠道 ID（未登记即沿用本渠道） */
  channelValue: Record<string, Record<string, string>>
  hint: string
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

// 目标渠道资源错误只在选择了具体渠道时展示；本渠道错误由表单上方的 Base URL 区域负责展示
const targetError = computed(() => (selectedChannelId.value ? activeTargetStatus.value?.error ?? '' : ''))

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

const fetchModels = () => {
  emit('fetch-models', selectedChannelId.value)
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
.mapping-target-drag-handle {
  cursor: grab;
}

.mapping-target-drag-handle:active {
  cursor: grabbing;
}

.mapping-target-ghost {
  opacity: 0.45;
}
</style>
