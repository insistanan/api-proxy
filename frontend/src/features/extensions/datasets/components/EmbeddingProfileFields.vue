<template>
  <div class="embedding-fields">
    <UiSelect v-if="!hideSource" :model-value="model.source" :items="sources" label="向量计算来源" @update:model-value="update('source', $event)" />
    <ChannelPicker v-if="!hideSource && model.source === 'remote'" :model-value="model.channel" :selected-model="model.model" :options="channels" show-model model-label="向量模型" :error="error" :loading="loading" class="embedding-advanced" @selection="selectTarget" />
    <UiInput v-if="!hideSource && model.source === 'local'" :model-value="model.model" label="向量模型名称" hint="需与已安装助手一致，更换后须重建索引" persistent-hint @update:model-value="update('model', $event)" />
    <template v-if="!compact">
      <UiInput :model-value="model.modelVersion" label="模型版本标识" hint="模型升级后修改，并重新构建索引" persistent-hint @update:model-value="update('modelVersion', $event)" />
      <UiInput :model-value="model.queryPrefix" label="查询前缀" hint="模型要求时加在用户文本前，例如 query:；没有要求则留空。修改后重新发布索引。" persistent-hint @update:model-value="update('queryPrefix', $event)" />
      <UiInput :model-value="model.examplePrefix" label="样本前缀" hint="模型要求时加在数据集样本前，例如 passage:；没有要求则留空。修改后重新发布索引。" persistent-hint @update:model-value="update('examplePrefix', $event)" />
      <UiInput v-if="!hideTimeout" :model-value="model.timeoutMs" type="number" min="100" max="30000" label="单次向量调用超时（毫秒）" @update:model-value="update('timeoutMs', Number($event))" />
    </template>
    <UiAccordion v-else variant="accordion" class="embedding-advanced">
      <UiAccordionItem>
        <summary class="cursor-pointer px-4 py-3 font-medium">高级向量参数</summary>
        <div class="px-4 pb-4">
          <div class="embedding-fields">
            <UiInput :model-value="model.modelVersion" label="模型版本标识" hint="模型升级后修改，并重新构建索引" persistent-hint @update:model-value="update('modelVersion', $event)" />
            <UiInput :model-value="model.queryPrefix" label="查询前缀" @update:model-value="update('queryPrefix', $event)" />
            <UiInput :model-value="model.examplePrefix" label="样本前缀" @update:model-value="update('examplePrefix', $event)" />
            <UiInput v-if="!hideTimeout" :model-value="model.timeoutMs" type="number" min="100" max="30000" label="单次向量调用超时（毫秒）" @update:model-value="update('timeoutMs', Number($event))" />
          </div>
        </div>
      </UiAccordionItem>
    </UiAccordion>
  </div>
</template>

<script setup lang="ts">
import UiAccordion from '@/shared/ui/disclosure/Accordion.vue'
import UiAccordionItem from '@/shared/ui/disclosure/AccordionItem.vue'
import UiInput from '@/shared/ui/field/Input.vue'
import UiSelect from '@/shared/ui/select/Select.vue'
import { onMounted, ref } from 'vue'
import type { EmbeddingProfile } from '@/types'
import type { ChannelSelectionOption } from '@/types/channel-selection'
import ChannelPicker from '@/shared/components/domain/ChannelPicker.vue'
import { intentApi, intentErrorText } from '@/features/channels'
const props = defineProps<{ hideTimeout?: boolean; hideSource?: boolean; compact?: boolean }>()
const model = defineModel<EmbeddingProfile>({ required: true })
function update<K extends keyof EmbeddingProfile>(key: K, value: EmbeddingProfile[K]) { model.value = { ...model.value, [key]: value } }
// 两个关联字段一次回传，兼容父层计算属性，避免只修改临时对象或覆盖另一字段。
function selectTarget(channel: string, selectedModel: string) { model.value = { ...model.value, channel, model: selectedModel } }
const sources = [{ title: '本地 CPU 助手', value: 'local' }, { title: '代理 Embeddings 渠道', value: 'remote' }]
const channels = ref<ChannelSelectionOption[]>([])
const error = ref('')
const loading = ref(false)
onMounted(async () => {
  if (props.hideSource) return
  loading.value = true
  try { channels.value = (await intentApi.options()).embeddings.map(option => ({ ...option, kind: 'embeddings' })) }
  catch (cause) { error.value = intentErrorText(cause) }
  finally { loading.value = false }
})
</script>

<style scoped>
@reference "@/styles/index.css";

.embedding-advanced { @apply [grid-column:1_/_-1]; }
.embedding-fields { @apply grid [grid-template-columns:repeat(2,_minmax(0,_1fr))] [gap:22px_20px]; }
@media (max-width: 600px) { .embedding-fields { @apply [grid-template-columns:minmax(0,_1fr)]; } }
</style>
