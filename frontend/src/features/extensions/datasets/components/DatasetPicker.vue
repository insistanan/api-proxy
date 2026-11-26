<template>
  <div>
    <UiSelect :model-value="selection" :items="items" item-title="title" item-value="id" :label="multiple ? '使用的数据集' : '已发布的意图数据集'" :multiple="multiple" :chips="multiple" clearable :loading="loading" :error-messages="error" :hint="requirePublished ? '只显示已有可用索引的数据集' : '数据集用途与向量模型必须一致'" persistent-hint @update:model-value="select" />
    <UiButton variant="text" size="small" to="/datasets" class="mb-3">管理数据集与向量索引</UiButton>
  </div>
</template>
<script setup lang="ts">
import { apiErrorText } from '@/shared/services/http'
import UiButton from '@/shared/ui/button/Button.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { computed, onMounted, ref, watch } from 'vue'
import type { EmbeddingProfile } from '@/types'
import { useDatasetsStore } from '@/features/extensions/datasets/stores/datasets'

const props = withDefaults(defineProps<{ kind: 'intent' | 'safety'; multiple?: boolean; requirePublished?: boolean; refreshKey?: number; profile?: EmbeddingProfile }>(), { multiple: true, requirePublished: false, refreshKey: 0 })
const emit = defineEmits<{ 'published-profile': [value: EmbeddingProfile | undefined] }>()
const model = defineModel<string[]>({ default: () => [] })
const store = useDatasetsStore()
const items = computed(() => store.items
  .filter(item => item.kind === props.kind)
  .filter(item => !props.requirePublished || item.indexes.length > 0)
  .map(item => ({ ...item, title: props.profile ? `${item.name} · ${item.indexes.some(index => sameProfile(index.profile, props.profile!)) ? '已有当前模型配置的索引' : '需要按当前模型构建索引'}` : props.requirePublished ? `${item.name} · ${item.indexes.length} 个可用索引` : item.name })))
function sameProfile(a: EmbeddingProfile, b: EmbeddingProfile) {
  return a.source === b.source && a.channel === b.channel && a.model === b.model && a.modelVersion === b.modelVersion && a.queryPrefix === b.queryPrefix && a.examplePrefix === b.examplePrefix
}
const selection = computed(() => props.multiple ? model.value : model.value[0] ?? null)
function select(value: string | string[] | null) {
  const ids = Array.isArray(value) ? value : value ? [value] : []
  model.value = ids
  const selected = ids.length === 1 ? store.items.find(item => item.id === ids[0]) : undefined
  const index = selected?.indexes[selected.indexes.length - 1]
  if (!props.profile) emit('published-profile', index?.profile)
}
const loading = ref(false)
const error = ref('')
async function load() {
  loading.value = true
  error.value = ''
  try { await store.load() } catch (cause) { error.value = apiErrorText(cause) }
  finally { loading.value = false }
}
onMounted(load)
watch(() => props.refreshKey, () => { if (props.refreshKey) void load() })
</script>
