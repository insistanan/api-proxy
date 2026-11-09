<template>
  <div class="json-body-pane">
    <div v-if="isLarge && !loaded" class="json-body-toolbar">
      <span>正文约 {{ formattedSize }}，载入后再渲染</span>
      <v-btn size="small" variant="tonal" prepend-icon="mdi-chevron-down" @click="loaded = true">
        载入正文
      </v-btn>
    </div>
    <ClampText v-else :text="formattedBody" :wrap="wrap" :max-height="320" />
  </div>
</template>

<script setup lang="ts">
// 原始正文视图：大正文先懒载入，再由 ClampText 统一做限高预览与展开。
import { computed, ref, watch } from 'vue'
import ClampText from './ClampText.vue'

const LARGE_BODY_CHARS = 64 * 1024

const props = defineProps<{
  body?: string
  wrap?: boolean
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
.json-body-pane {
  min-height: 0;
}

.json-body-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  font-size: 13px;
  color: rgb(var(--v-theme-on-surface));
  background: rgba(var(--v-theme-surface-variant), 0.3);
  border-radius: 8px;
}
</style>
