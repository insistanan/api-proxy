<template>
  <JsonPreviewTrigger class="json-body-pane" :data="body || ''" :title="title" :meta="meta">
    <div v-if="isLarge && !loaded" class="json-body-toolbar">
      <span>正文约 {{ formattedSize }}，载入后再渲染</span>
      <UiButton size="small" variant="tonal" prepend-icon="mdi-chevron-down" @click="loaded = true">
        载入正文
      </UiButton>
    </div>
    <ClampText v-else :text="formattedBody" :wrap="wrap" :max-height="320" />
  </JsonPreviewTrigger>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
// 原始正文视图：大正文先懒载入，再由 ClampText 统一做限高预览与展开。
import { computed, ref, watch } from 'vue'
import ClampText from '@/features/observability/components/common/ClampText.vue'
import JsonPreviewTrigger from '@/shared/components/data/JsonPreviewTrigger.vue'

const LARGE_BODY_CHARS = 64 * 1024

const props = defineProps<{
  body?: string
  wrap?: boolean
  title?: string
  meta?: string
}>()

const loaded = ref(false)

watch(
  () => props.body,
  () => {
    loaded.value = false
  }
)

const rawLength = computed(() => props.body?.length || 0)
const isLarge = computed(() => rawLength.value > LARGE_BODY_CHARS)

const formattedSize = computed(() => {
  const bytes = rawLength.value
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
})

const formatJsonOrText = (raw: string) => {
  const trimmed = raw.trim()
  if (!trimmed) return ''
  try {
    return JSON.stringify(JSON.parse(trimmed), null, 2)
  } catch {
    return raw
  }
}

const formattedBody = computed(() => {
  if (isLarge.value && !loaded.value) return ''
  return formatJsonOrText(props.body || '')
})

</script>

<style scoped>
@reference "@/styles/index.css";

.json-body-pane {
  @apply min-h-0;
}

.json-body-toolbar {
  @apply flex items-center justify-between gap-3 [padding:12px_14px] [font-size:13px] text-foreground [background:color-mix(in_srgb,_var(--surface-variant)_30%,_transparent)] rounded-control;
}
</style>
