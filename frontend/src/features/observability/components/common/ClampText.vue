<template>
  <div class="clamp-text">
    <pre
      v-if="text"
      class="clamp-body"
      :class="{ 'font-mono': monospace, wrap, clamped: collapsible && !expanded }"
      :style="bodyStyle"
    >{{ text }}</pre>
    <div v-if="collapsible" class="clamp-footer">
      <span class="clamp-hint">{{ sizeLabel }}</span>
      <UiButton
        size="x-small"
        variant="text"
        :prepend-icon="expanded ? 'mdi-chevron-up' : 'mdi-chevron-down'"
        @click="expanded = !expanded"
      >
        {{ expanded ? '收起' : '展开全文' }}
      </UiButton>
    </div>
  </div>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
// 统一的长正文展示：折叠态只做限高预览且绝不内滚，展开后限 70vh 内滚。
import { computed, ref, watch } from 'vue'

const props = withDefaults(
  defineProps<{
    text: string
    monospace?: boolean
    wrap?: boolean
    /** 折叠态最大高度（px） */
    maxHeight?: number
    /** 超过该字符数即视为可折叠 */
    minChars?: number
  }>(),
  {
    monospace: true,
    wrap: true,
    maxHeight: 260,
    minChars: 700
  }
)

const LINE_HEIGHT = 21

const expanded = ref(false)

watch(() => props.text, () => { expanded.value = false })

const lineCount = computed(() => props.text ? props.text.split(/\r?\n/).length : 0)
const collapsible = computed(() => props.text.length > props.minChars || lineCount.value * LINE_HEIGHT > props.maxHeight)

const sizeLabel = computed(() => {
  const bytes = props.text.length
  const size = bytes < 1024 ? `${bytes} B` : `${(bytes / 1024).toFixed(1)} KB`
  return `${lineCount.value} 行 · ${size}`
})

const bodyStyle = computed(() => ({
  maxHeight: expanded.value ? '70vh' : `${props.maxHeight}px`,
  overflow: expanded.value ? 'auto' : 'hidden'
}))
</script>

<style scoped>
@reference "@/styles/index.css";

.clamp-text {
  @apply min-w-0;
}

.clamp-body {
  @apply m-0 [font-size:12.5px] [line-height:1.65] text-foreground whitespace-pre [word-break:break-word];
}

.clamp-body.wrap {
  @apply whitespace-pre-wrap;
}

.clamp-body.clamped {
  /* 渐隐提示下方还有内容，同时避免出现内嵌滚动条。 */
  @apply [mask-image:linear-gradient(to_bottom,_#000_calc(100%_-_28px),_transparent)] [-webkit-mask-image:linear-gradient(to_bottom,_#000_calc(100%_-_28px),_transparent)];
}

.font-mono {
  @apply [font-family:ui-monospace,_SFMono-Regular,_Menlo,_Consolas,_monospace];
}

.clamp-footer {
  @apply flex items-center justify-between gap-3 mt-1;
}

.clamp-hint {
  @apply [font-size:11px] [color:color-mix(in_srgb,_var(--foreground)_55%,_transparent)];
}
</style>
