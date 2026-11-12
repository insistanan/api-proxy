<template>
  <div>
    <v-select v-model="model" :items="items" item-title="name" item-value="id" label="使用的数据集" multiple chips :loading="loading" :error-messages="error" hint="使用已发布索引；数据集用途和向量模型必须一致" persistent-hint />
    <v-btn variant="text" size="small" to="/datasets" class="mb-3">管理数据集与向量索引</v-btn>
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useDatasetsStore } from '@/stores/datasets'
import { intentErrorText } from '@/services/intent'
const props = defineProps<{ kind: 'intent' | 'safety' }>()
const model = defineModel<string[]>({ default: () => [] })
const store = useDatasetsStore()
const items = computed(() => store.items.filter(item => item.kind === props.kind))
const loading = ref(false)
const error = ref('')
onMounted(async () => { loading.value = true; try { await store.load() } catch (cause) { error.value = intentErrorText(cause) } finally { loading.value = false } })
</script>
