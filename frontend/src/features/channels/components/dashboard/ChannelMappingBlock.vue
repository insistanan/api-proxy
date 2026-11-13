<template>
  <v-tooltip v-if="preview" location="top" :open-delay="200" :open-on-focus="false">
    <template #activator="{ props: tooltipProps }">
      <button
        v-bind="tooltipProps"
        type="button"
        class="meta-block meta-block--mapping"
        :aria-label="`模型映射 ${preview}，点击编辑渠道`"
        @click.stop="emit('edit')"
      >
        <v-icon size="14" class="meta-block-icon">mdi-swap-horizontal</v-icon>
        <span class="meta-block-label">映射</span>
        <span class="meta-block-value">{{ preview }}</span>
        <span v-if="entryCount > 1" class="meta-block-badge">{{ entryCount }} 条</span>
      </button>
    </template>
    <div class="mapping-tooltip">
      <div class="text-caption font-weight-bold mb-1">模型映射</div>
      <div v-for="(line, index) in fullLines" :key="index" class="mapping-tooltip-line">{{ line }}</div>
    </div>
  </v-tooltip>
  <span v-else class="meta-block meta-block--muted">
    <v-icon size="14" class="meta-block-icon">mdi-swap-horizontal</v-icon>
    <span class="meta-block-label">映射</span>
    <span class="meta-block-value">未配置</span>
  </span>
</template>

<script setup lang="ts">
/**
 * 渠道的模型映射摘要块。渠道行副行、备用资源池、弃用池共用这一份，
 * 三处此前各写一遍同样的 chip + tooltip，改一处忘另一处的成本比抽组件高。
 */
import { computed } from 'vue'
import type { ApiTab, Channel } from '@/api'

const props = withDefaults(
  defineProps<{
    channel: Channel
    channelType: ApiTab
    /** 预览文本字符上限。渠道行副行空间充裕给默认值，池卡片窄可以调小。 */
    previewLimit?: number
  }>(),
  { previewLimit: 40 }
)

const emit = defineEmits<{ (_e: 'edit'): void }>()

/** modelMapping 是 source → targets[]，摊平成 [source, target] 对，丢掉空值。 */
const entries = computed<Array<readonly [string, string]>>(() =>
  Object.entries(props.channel.modelMapping || {}).flatMap(([source, targets]) => {
    const cleanSource = source.trim()
    const targetList = Array.isArray(targets) ? targets : [targets]
    return targetList
      .map(target => [cleanSource, String(target || '').trim()] as const)
      .filter(([mappedSource, target]) => mappedSource && target)
  })
)

const entryCount = computed(() => entries.value.length)

const defaultModel = computed(() => String(props.channel.defaultModel || '').trim())

/** 截断模型名：带路径的优先保留最后一段（通常是版本/变体），否则直接截。 */
const truncateModel = (text: string, maxLength: number): string => {
  if (text.length <= maxLength) return text
  if (text.includes('/')) {
    const parts = text.split('/')
    const last = parts[parts.length - 1]
    if (last.length <= maxLength - 3) return `.../${last}`
  }
  return `${text.slice(0, maxLength - 3)}...`
}

/**
 * 多条映射时挑一条最有代表性的来展示：按渠道协议/服务类型猜用户最关心的模型族，
 * 都没命中就取字典序第一条，保证同一渠道每次渲染选的是同一条。
 */
const preferredEntry = computed<readonly [string, string] | undefined>(() => {
  const lowerService = props.channel.serviceType.toLowerCase()
  const preferredTerms =
    props.channelType === 'messages' || lowerService === 'claude'
      ? ['opus', 'sonnet', 'claude']
      : props.channelType === 'responses' ||
          props.channelType === 'images' ||
          lowerService === 'responses' ||
          lowerService === 'openai' ||
          lowerService === 'chat'
        ? ['gpt', 'codex']
        : ['gemini']

  for (const term of preferredTerms) {
    const match = entries.value.find(([source]) => source.toLowerCase().includes(term))
    if (match) return match
  }
  return [...entries.value].sort((left, right) => left[0].localeCompare(right[0]))[0]
})

const preview = computed(() => {
  // 只有兜底模型、没有映射规则
  if (defaultModel.value && entryCount.value === 0) {
    return `⇣ ${truncateModel(defaultModel.value, Math.max(12, props.previewLimit - 4))}`
  }
  if (entryCount.value === 0) return ''

  const chosen = entryCount.value === 1 ? entries.value[0] : preferredEntry.value
  if (!chosen) return ''
  const [source, target] = chosen
  const text = source === target ? target : `${source} → ${target}`
  // 多条时条数交给右侧徽标显示，预览留给内容本身
  return truncateModel(text, entryCount.value > 1 ? Math.max(12, props.previewLimit - 8) : props.previewLimit)
})

/** tooltip 里的全量列表：兜底模型一行，其后每条映射一行。 */
const fullLines = computed<string[]>(() => {
  const lines: string[] = []
  if (defaultModel.value) lines.push(`⇣ 兜底 → ${defaultModel.value}`)
  for (const [source, target] of entries.value) {
    lines.push(source === target ? source : `${source} → ${target}`)
  }
  return lines.length ? lines : ['无模型映射']
})
</script>

<style scoped>
/* 基类 .meta-block 在 assets/style.css，这里只写映射块自己的配色与 tooltip */
.meta-block--mapping {
  border-color: rgba(var(--v-theme-secondary), 0.5);
  background: color-mix(in srgb, rgb(var(--v-theme-secondary)) 10%, transparent);
  color: rgb(var(--v-theme-on-surface));
  cursor: pointer;
}
.meta-block--mapping:hover,
.meta-block--mapping:focus-visible {
  border-color: rgb(var(--v-theme-secondary));
  background: color-mix(in srgb, rgb(var(--v-theme-secondary)) 18%, transparent);
}
.meta-block--mapping .meta-block-icon,
.meta-block--mapping .meta-block-label {
  color: rgb(var(--v-theme-secondary));
}

.mapping-tooltip {
  min-width: 200px;
  max-width: 420px;
  font-size: 12px;
  line-height: 1.6;
  color: rgb(var(--v-theme-on-surface));
}
.mapping-tooltip-line {
  padding: 3px 0;
  font-family: 'Fira Code', 'JetBrains Mono', monospace;
  font-size: 11px;
  color: rgba(var(--v-theme-on-surface), 0.85);
  word-break: break-all;
}
.mapping-tooltip-line:not(:last-child) {
  border-bottom: 1px solid rgba(var(--v-theme-outline), 0.15);
}
</style>
