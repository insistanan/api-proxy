<template>
  <div class="embedding-fields">
    <v-select :model-value="model.source" :items="sources" label="向量计算来源" @update:model-value="update('source', $event)" />
    <v-select v-if="model.source === 'remote'" :model-value="model.channel" :items="channels" item-title="name" item-value="id" label="Embeddings 渠道" :error-messages="error" :loading="loading" @update:model-value="update('channel', $event)" />
    <v-text-field :model-value="model.model" label="向量模型名称" hint="本地需与已安装助手的模型名称一致" persistent-hint @update:model-value="update('model', $event)" />
    <v-text-field :model-value="model.modelVersion" label="模型版本标识" hint="远程模型升级后修改此标识，并重新构建索引" persistent-hint @update:model-value="update('modelVersion', $event)" />
    <v-text-field :model-value="model.queryPrefix" label="查询前缀" @update:model-value="update('queryPrefix', $event)" />
    <v-text-field :model-value="model.examplePrefix" label="样本前缀" @update:model-value="update('examplePrefix', $event)" />
    <v-text-field v-if="!hideTimeout" :model-value="model.timeoutMs" type="number" min="100" max="30000" label="单次向量调用超时（毫秒）" @update:model-value="update('timeoutMs', Number($event))" />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { EmbeddingProfile } from '@/services/datasets'
import { intentApi, intentErrorText } from '@/services/intent'
defineProps<{ hideTimeout?: boolean }>()
const model = defineModel<EmbeddingProfile>({ required: true })
function update<K extends keyof EmbeddingProfile>(key: K, value: EmbeddingProfile[K]) { model.value = { ...model.value, [key]: value } }
const sources = [{ title: '本地 CPU 助手', value: 'local' }, { title: '代理 Embeddings 渠道', value: 'remote' }]
const channels = ref<{ id: string; name: string }[]>([])
const error = ref('')
const loading = ref(false)
onMounted(async () => {
  loading.value = true
  try { channels.value = (await intentApi.options()).embeddings }
  catch (cause) { error.value = intentErrorText(cause) }
  finally { loading.value = false }
})
</script>

<style scoped>
.embedding-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 22px 20px; }
@media (max-width: 600px) { .embedding-fields { grid-template-columns: minmax(0, 1fr); } }
</style>
